package cnc

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed worker_tools_manifest.py
var workerToolsManifestCheck string

// Provider API tokens are local runtime data, never returned by the API.
type linodeIntegration struct {
	mu           sync.Mutex
	dir          string
	client       *http.Client
	base         string
	tcpAddr      string
	httpAddr     string
	prepareTools func(context.Context) error
}

func newLinodeIntegration(dir string) *linodeIntegration {
	return &linodeIntegration{dir: filepath.Join(dir, "linode"), client: &http.Client{Timeout: 45 * time.Second}, base: "https://api.linode.com/v4/"}
}
func (l *linodeIntegration) token() (string, error) {
	b, e := os.ReadFile(filepath.Join(l.dir, "token"))
	if e != nil {
		return "", fmt.Errorf("configure a Linode API token first")
	}
	return strings.TrimSpace(string(b)), nil
}
func (l *linodeIntegration) call(ctx context.Context, token, path string, body any, out any) error {
	var reader io.Reader
	method := "GET"
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		reader = bytes.NewReader(b)
		method = "POST"
	}
	req, e := http.NewRequestWithContext(ctx, method, l.base+path, reader)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, e := l.client.Do(req)
	if e != nil {
		return fmt.Errorf("Linode request failed")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("Linode rejected the request (HTTP %d); check permissions, quota, and selected region", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}
func (l *linodeIntegration) list(ctx context.Context, token, path string) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	for page := 1; page <= 100; page++ {
		var response struct {
			Data  []json.RawMessage `json:"data"`
			Pages int               `json:"pages"`
		}
		if e := l.call(ctx, token, fmt.Sprintf("%s?page_size=100&page=%d", path, page), nil, &response); e != nil {
			return nil, e
		}
		result = append(result, response.Data...)
		if page >= response.Pages {
			return result, nil
		}
	}
	return nil, fmt.Errorf("Linode pagination limit exceeded")
}
func linodeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (l *linodeIntegration) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, e error) { linodeJSON(w, status, map[string]string{"error": e.Error()}) }
	action := strings.TrimPrefix(r.URL.Path, "/api/linode/")
	if action == "settings" {
		if r.Method == "GET" {
			_, e := l.token()
			linodeJSON(w, 200, map[string]bool{"configured": e == nil})
			return
		}
		if r.Method != "POST" {
			fail(405, fmt.Errorf("method not allowed"))
			return
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		var input struct {
			Token string `json:"token"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); e != nil {
			fail(400, fmt.Errorf("invalid settings"))
			return
		}
		input.Token = strings.TrimSpace(input.Token)
		if len(input.Token) < 16 || strings.ContainsAny(input.Token, "\r\n ") {
			fail(400, fmt.Errorf("invalid API token"))
			return
		}
		var profile any
		if e := l.call(r.Context(), input.Token, "profile", nil, &profile); e != nil {
			fail(400, e)
			return
		}
		if e := os.MkdirAll(l.dir, 0700); e != nil {
			fail(500, fmt.Errorf("cannot save credential"))
			return
		}
		os.Chmod(l.dir, 0700)
		tmp := filepath.Join(l.dir, "token.tmp")
		if e := os.WriteFile(tmp, []byte(input.Token), 0600); e != nil {
			fail(500, fmt.Errorf("cannot save credential"))
			return
		}
		os.Chmod(tmp, 0600)
		if e := os.Rename(tmp, filepath.Join(l.dir, "token")); e != nil {
			fail(500, fmt.Errorf("cannot save credential"))
			return
		}
		linodeJSON(w, 200, map[string]bool{"configured": true})
		return
	}
	token, e := l.token()
	if e != nil {
		fail(409, e)
		return
	}
	if r.Method == "GET" && action == "bootstrap" {
		serverAddr, httpAddr, err := l.workerAddresses(r.Context(), token, r.URL.Query().Get("server_addr"), r.URL.Query().Get("http_addr"), "")
		if err != nil {
			fail(409, err)
			return
		}
		script, err := linodeWorkerScript(r.URL.Query().Get("label"), serverAddr, httpAddr)
		if err != nil {
			fail(400, err)
			return
		}
		linodeJSON(w, 200, map[string]string{"script": script})
		return
	}
	if r.Method == "GET" && action == "connection" {
		connection, err := l.connection(r.Context(), token)
		if err != nil {
			fail(409, err)
			return
		}
		linodeJSON(w, 200, connection)
		return
	}
	if r.Method == "GET" && action == "catalog" {
		out := map[string]any{}
		for key, path := range map[string]string{"regions": "regions", "types": "linode/types", "images": "images"} {
			items, e := l.list(r.Context(), token, path)
			if e != nil {
				fail(502, e)
				return
			}
			out[key] = items
		}
		linodeJSON(w, 200, out)
		return
	}
	if r.Method == "GET" && action == "instances" {
		items, e := l.list(r.Context(), token, "linode/instances")
		if e != nil {
			fail(502, e)
			return
		}
		linodeJSON(w, 200, items)
		return
	}
	if r.Method == "POST" && action == "workers" {
		// Serialize creation; a lost response must be reconciled by label, never replayed blindly.
		l.mu.Lock()
		defer l.mu.Unlock()
		var input struct {
			Label      string `json:"label"`
			Region     string `json:"region"`
			Type       string `json:"type"`
			Image      string `json:"image"`
			ServerAddr string `json:"server_addr"`
			HTTPAddr   string `json:"http_addr"`
			Backups    bool   `json:"backups"`
			PublicKey  string `json:"public_key"`
			Count      *int   `json:"count"`
		}
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input); e != nil {
			fail(400, fmt.Errorf("invalid worker request"))
			return
		}
		count := 1
		if input.Count != nil {
			count = *input.Count
		}
		labels, err := linodeWorkerLabels(input.Label, count)
		if err != nil {
			fail(400, err)
			return
		}
		if input.Image != "linode/ubuntu24.04" {
			fail(400, fmt.Errorf("worker bootstrap supports Ubuntu 24.04"))
			return
		}
		if !validLinodeLabel(input.Label) {
			fail(400, fmt.Errorf("label must contain 3–64 letters, digits, hyphens, or underscores"))
			return
		}
		input.ServerAddr, input.HTTPAddr, e = l.workerAddresses(r.Context(), token, input.ServerAddr, input.HTTPAddr, input.Region)
		if e != nil {
			fail(409, e)
			return
		}
		script, e := linodeWorkerScript(input.Label, input.ServerAddr, input.HTTPAddr)
		if e != nil {
			fail(400, e)
			return
		}
		for path, id := range map[string]string{"regions": input.Region, "linode/types": input.Type, "images": input.Image} {
			items, e := l.list(r.Context(), token, path)
			if e != nil {
				fail(502, e)
				return
			}
			found := false
			for _, raw := range items {
				var item struct {
					ID           string   `json:"id"`
					Capabilities []string `json:"capabilities"`
				}
				json.Unmarshal(raw, &item)
				if item.ID == id {
					found = true
					if path == "regions" {
						supported := false
						for _, capability := range item.Capabilities {
							if capability == "Metadata" {
								supported = true
							}
						}
						if !supported {
							fail(400, fmt.Errorf("region does not support cloud-init Metadata"))
							return
						}
					}
				}
			}
			if !found {
				fail(400, fmt.Errorf("unknown %s selection", path))
				return
			}
		}
		items, e := l.list(r.Context(), token, "linode/instances")
		if e != nil {
			fail(502, e)
			return
		}
		for _, raw := range items {
			var item struct {
				Label string `json:"label"`
			}
			json.Unmarshal(raw, &item)
			if slicesContainLabel(labels, item.Label) {
				fail(409, fmt.Errorf("label already exists; inspect its status instead of creating again"))
				return
			}
		}
		keys, err := linodeAuthorizedKeys(input.PublicKey)
		if err != nil {
			fail(400, err)
			return
		}
		if l.prepareTools != nil {
			if err := l.prepareTools(r.Context()); err != nil {
				fail(503, err)
				return
			}
		}
		created := []json.RawMessage{}
		for index, label := range labels {
			password := make([]byte, 36)
			_, err := rand.Read(password)
			if err == nil {
				script, err = l.privateWorkerScript(r.Context(), label, input.ServerAddr, input.HTTPAddr)
			}
			var out json.RawMessage
			if err == nil {
				rootPassword := base64.RawURLEncoding.EncodeToString(password) + "aA1!"
				policyPassword, policyErr := workerRootPassword(filepath.Dir(l.dir))
				if policyErr != nil {
					fail(503, policyErr)
					return
				}
				if policyPassword != "" {
					rootPassword = policyPassword
				}
				setup, setupErr := workerPasswordSetup(rootPassword)
				if setupErr != nil {
					fail(503, setupErr)
					return
				}
				script += setup
				// Persist before provisioning so even a lost provider response keeps login access.
				if saveErr := saveWorkerPassword(filepath.Dir(l.dir), label, rootPassword); saveErr != nil {
					linodeJSON(w, 503, map[string]any{"requested": count, "created": created, "error": "Cannot save worker password; this worker was not created.", "not_attempted": labels[index:]})
					return
				}
				payload := map[string]any{"label": label, "region": input.Region, "type": input.Type, "image": input.Image, "root_pass": rootPassword, "private_ip": true, "booted": true, "authorized_keys": keys, "backups_enabled": input.Backups, "tags": []string{"cnc", "cnc-worker"}, "metadata": map[string]string{"user_data": base64.StdEncoding.EncodeToString([]byte(script))}}
				err = l.call(r.Context(), token, "linode/instances", payload, &out)
			}
			if err != nil {
				if count == 1 {
					fail(502, err)
					return
				}
				// Creation may have succeeded before a lost response. Never retry it automatically.
				linodeJSON(w, 207, map[string]any{"requested": count, "created": created, "error": err.Error(), "uncertain_label": label, "not_attempted": labels[index+1:]})
				return
			}
			created = append(created, out)
			if err := l.allowWorkerNetwork(r.Context(), out); err != nil {
				linodeJSON(w, 207, map[string]any{"requested": count, "created": created, "error": err.Error(), "not_attempted": labels[index+1:]})
				return
			}
		}
		if count == 1 {
			linodeJSON(w, 201, created[0])
		} else {
			linodeJSON(w, 201, map[string]any{"requested": count, "created": created, "not_attempted": []string{}})
		}
		return
	}
	fail(405, fmt.Errorf("method or endpoint not supported"))
}
func validLinodeLabel(label string) bool {
	if len(label) < 3 || len(label) > 64 {
		return false
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func linodeWorkerScript(label, serverAddr, httpAddr string) (string, error) {
	for _, addr := range []string{serverAddr, httpAddr} {
		host, port, e := net.SplitHostPort(addr)
		if e != nil || net.ParseIP(host) == nil || port == "" {
			return "", fmt.Errorf("server addresses must be IP:port")
		}
		n := 0
		for _, c := range port {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("invalid port")
			}
			n = n*10 + int(c-'0')
			if n > 65535 {
				return "", fmt.Errorf("invalid port")
			}
		}
		if n < 1 {
			return "", fmt.Errorf("invalid port")
		}
	}
	if !validLinodeLabel(label) {
		return "", fmt.Errorf("invalid label")
	}
	config, _ := json.Marshal(map[string]any{"server_addr": serverAddr, "worker_id": label, "max_tasks": 2, "data_dir": "/opt/cnc/data", "processor_workspace_root": "/root/processor"})
	// This is fetched over the cluster's private network; dashboard authentication remains intact.
	return fmt.Sprintf(`#!/bin/bash
set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq python3 python3-venv curl ca-certificates
install -d -m 700 /opt/cnc/data
for attempt in $(seq 1 180); do
 if curl --fail --connect-timeout 10 --max-time 120 'http://%s/download/cnc-worker-linux' -o /opt/cnc/cnc-worker.new && curl --fail --connect-timeout 10 --max-time 120 'http://%s/download/cnc-worker-tools' -o /opt/cnc/tools.tar.gz; then break; fi
 if test "$attempt" -eq 180; then echo 'Controller downloads unavailable' >&2; exit 1; fi
 sleep 10
done
chmod 700 /opt/cnc/cnc-worker.new
mv /opt/cnc/cnc-worker.new /opt/cnc/cnc-worker
tar -xzf /opt/cnc/tools.tar.gz -C /opt/cnc
python3 - <<'TOOLS_READY'
%s
TOOLS_READY
curl --fail --connect-timeout 10 --max-time 30 'http://%s/download/cnc-release' -o /opt/cnc/RELEASE
cat > /opt/cnc/config.json <<'CONFIG'
%s
CONFIG
python3 - <<'NODE_RUNTIME'
import urllib.request, hashlib, tarfile
from pathlib import Path
installed = Path('/opt/cnc/node-path')
if not (installed.exists() and (Path(installed.read_text().strip()) / 'bin/node').is_file()):
 base = 'https://nodejs.org/dist/latest-v22.x/'
 with urllib.request.urlopen(base + 'SHASUMS256.txt', timeout=60) as response: sums = response.read().decode()
 checksum, filename = next(line.split() for line in sums.splitlines() if line.endswith('-linux-x64.tar.xz'))
 archive_path = Path('/opt/cnc/node.tar.xz')
 with urllib.request.urlopen(base + filename, timeout=120) as response: archive_path.write_bytes(response.read())
 if hashlib.sha256(archive_path.read_bytes()).hexdigest() != checksum: raise RuntimeError('Node checksum mismatch')
 with tarfile.open(archive_path) as archive: archive.extractall('/opt/cnc/node-runtime', filter='data')
 installed.write_text(str(Path('/opt/cnc/node-runtime') / filename.removesuffix('.tar.xz')))
NODE_RUNTIME
/opt/cnc/cnc-worker -drain-support >/dev/null
cp /opt/cnc/cnc-worker /opt/cnc/worker-stop-guard.new
chmod 700 /opt/cnc/worker-stop-guard.new
mv /opt/cnc/worker-stop-guard.new /opt/cnc/worker-stop-guard
NODE_BIN_DIR="$(cat /opt/cnc/node-path)/bin"
cat > /etc/systemd/system/cnc-worker.service <<UNIT
[Unit]
Description=CNC worker
After=network-online.target
Wants=network-online.target
[Service]
User=root
WorkingDirectory=/opt/cnc
Environment=PATH=${NODE_BIN_DIR}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=/opt/cnc/cnc-worker -config=/opt/cnc/config.json
ExecStop=/opt/cnc/worker-stop-guard -config=/opt/cnc/config.json -drain-stop \$MAINPID
TimeoutStopSec=infinity
KillMode=mixed
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now cnc-worker
`, httpAddr, httpAddr, workerToolsManifestCheck, httpAddr, config), nil
}

func linodeLocalCaller(remote, local string) bool {
	ip := net.ParseIP(remote)
	return ip != nil && (ip.IsLoopback() || (remote == local && !ip.IsUnspecified()))
}

// Workers need downloads, while management stays behind the local authenticated UI.
func (s *Server) localAdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.LocalAdminOnly {
			remote, _, _ := net.SplitHostPort(r.RemoteAddr)
			local, _, _ := net.SplitHostPort(s.config.HTTPAddr)
			download := r.Method == http.MethodGet && (strings.HasPrefix(r.URL.Path, "/api/files/") || r.URL.Path == "/download/cnc-worker-linux" || r.URL.Path == "/download/cnc-worker-tools" || r.URL.Path == "/download/cnc-release")
			scopedProcessor := strings.HasPrefix(r.URL.Path, "/api/processor/attempts/") || strings.HasPrefix(r.URL.Path, "/api/processor/bundles/")
			scopedEnum := strings.HasPrefix(r.URL.Path, "/api/enum/")
			if !download && !scopedProcessor && !scopedEnum && !linodeLocalCaller(remote, local) {
				http.Error(w, "management requires the local dashboard proxy", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func linodeAuthorizedKeys(extra string) ([]string, error) {
	keys := []string{}
	home, _ := os.UserHomeDir()
	data, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	for len(data) > 0 {
		key, _, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			break
		}
		keys = append(keys, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
		data = rest
	}
	if strings.TrimSpace(extra) != "" {
		key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(extra))
		if err != nil || len(bytes.TrimSpace(rest)) != 0 {
			return nil, fmt.Errorf("provide one valid SSH public key")
		}
		keys = append(keys, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("provide an SSH public key for worker administration")
	}
	return keys, nil
}

func linodeWorkerLabels(prefix string, count int) ([]string, error) {
	if count < 1 || count > 20 {
		return nil, fmt.Errorf("worker count must be between 1 and 20")
	}
	labels := make([]string, count)
	for i := range labels {
		label := prefix
		if count > 1 {
			label = fmt.Sprintf("%s-%d", prefix, i+1)
		}
		if !validLinodeLabel(label) {
			return nil, fmt.Errorf("worker labels must contain 3–64 letters, digits, hyphens, or underscores, including numbered suffixes")
		}
		labels[i] = label
	}
	return labels, nil
}
func slicesContainLabel(labels []string, candidate string) bool {
	for _, label := range labels {
		if label == candidate {
			return true
		}
	}
	return false
}
