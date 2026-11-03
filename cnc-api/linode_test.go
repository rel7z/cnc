package cnc

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinodeBootstrapValidation(t *testing.T) {
	for _, address := range []string{"example.com:9090", "1.2.3.4:9090;id", "1.2.3.4:0", "1.2.3.4:99999"} {
		if _, e := linodeWorkerScript("worker-1", address, "192.168.1.1:8080"); e == nil {
			t.Fatalf("accepted unsafe address %s", address)
		}
	}
	script, e := linodeWorkerScript("worker-1", "192.168.1.1:9090", "192.168.1.1:8080")
	if e != nil {
		t.Fatal(e)
	}
	for _, needle := range []string{"systemctl enable --now cnc-worker", "\"worker_id\":\"worker-1\"", "/download/cnc-worker-tools", "SHASUMS256.txt", "Node checksum mismatch", "Worker tools package is incomplete", "filename.removesuffix('.tar.xz')"} {
		if !strings.Contains(script, needle) {
			t.Fatalf("missing %s", needle)
		}
	}
}
func TestLinodeCredentialRedaction(t *testing.T) {
	l := newLinodeIntegration(t.TempDir())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer 0123456789abcdef0123456789abcdef" {
			t.Error("missing provider token")
		}
		w.Write([]byte(`{"username":"test"}`))
	}))
	defer upstream.Close()
	l.base = upstream.URL + "/"
	w := httptest.NewRecorder()
	l.handle(w, httptest.NewRequest("POST", "/api/linode/settings", strings.NewReader(`{"token":"0123456789abcdef0123456789abcdef"}`)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	info, e := os.Stat(filepath.Join(l.dir, "token"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	w = httptest.NewRecorder()
	l.handle(w, httptest.NewRequest("GET", "/api/linode/settings", nil))
	if strings.Contains(w.Body.String(), "012345") || !strings.Contains(w.Body.String(), "true") {
		t.Fatal("token exposed or disconnected")
	}
}
func TestLinodeNoPostRetry(t *testing.T) {
	attempts := 0
	l := newLinodeIntegration(t.TempDir())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(503)
		w.Write([]byte(`secret sensitive provider response`))
	}))
	defer upstream.Close()
	l.base = upstream.URL + "/"
	var out any
	e := l.call(t.Context(), "secret", "linode/instances", map[string]string{"label": "test"}, &out)
	if e == nil || attempts != 1 || strings.Contains(e.Error(), "secret") {
		t.Fatal("creation retried or secret exposed")
	}
}

func TestLinodeManagementOnlyLocalProxy(t *testing.T) {
	for _, test := range []struct {
		remote, local string
		allowed       bool
	}{{"127.0.0.1", "192.168.145.81", true}, {"192.168.145.81", "192.168.145.81", true}, {"192.168.145.171", "192.168.145.81", false}, {"172.236.141.250", "192.168.145.81", false}, {"0.0.0.0", "0.0.0.0", false}, {"", "", false}} {
		if linodeLocalCaller(test.remote, test.local) != test.allowed {
			t.Fatalf("unexpected access for %q", test.remote)
		}
	}
}

func TestLocalAdminProtectsSecretsAndKeepsWorkerDownloads(t *testing.T) {
	s := &Server{config: &ServerConfig{HTTPAddr: "192.168.145.81:8080", LocalAdminOnly: true}}
	handler := s.localAdminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/api/linode/settings", "/api/fs/read?file=/opt/cnc/data/linode/token", "/api/server/exec", "/api/jobs"} {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.168.145.171:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("management accessible: %s", path)
		}
	}
	for _, path := range []string{"/download/cnc-worker-linux", "/download/cnc-worker-tools", "/api/files/wordlist/wp_plugins.txt"} {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.168.145.171:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("worker download blocked: %s", path)
		}
	}
}

func TestWorkerDownloadCannotTraverseToCredentials(t *testing.T) {
	s := &Server{config: &ServerConfig{DataDir: t.TempDir()}}
	w := httptest.NewRecorder()
	s.handleFilesAPI(w, httptest.NewRequest("GET", "/api/files/chunk?part=0%2F..%2F..%2Fopt%2Fcnc%2Fdata%2Flinode%2Ftoken", nil))
	if w.Code != 400 {
		t.Fatal("unsafe chunk path accepted")
	}
}

func TestLinodeWorkerCreationAndDuplicateReconciliation(t *testing.T) {
	l := newLinodeIntegration(t.TempDir())
	if err := os.MkdirAll(l.dir, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(l.dir, "token"), []byte("test-provider-token"), 0600)
	public, _ := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, 32)))
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
	creates := 0
	created := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/regions":
			w.Write([]byte(`{"pages":1,"data":[{"id":"sg-sin-2","capabilities":["Metadata"]}]}`))
		case "/linode/types":
			w.Write([]byte(`{"pages":1,"data":[{"id":"g6-standard-2"}]}`))
		case "/images":
			w.Write([]byte(`{"pages":1,"data":[{"id":"linode/ubuntu24.04"}]}`))
		case "/linode/instances":
			if r.Method == "GET" {
				if created {
					w.Write([]byte(`{"pages":1,"data":[{"label":"test-worker"}]}`))
				} else {
					w.Write([]byte(`{"pages":1,"data":[]}`))
				}
				return
			}
			creates++
			created = true
			var payload struct {
				Type     string   `json:"type"`
				Region   string   `json:"region"`
				Backups  bool     `json:"backups_enabled"`
				Keys     []string `json:"authorized_keys"`
				Metadata struct {
					Data string `json:"user_data"`
				} `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Type != "g6-standard-2" || payload.Region != "sg-sin-2" || !payload.Backups || len(payload.Keys) == 0 {
				t.Error("provisioning selections not forwarded")
			}
			script, err := base64.StdEncoding.DecodeString(payload.Metadata.Data)
			if err != nil || !strings.Contains(string(script), "/download/cnc-release") || !strings.Contains(string(script), "192.168.145.81:9090") {
				t.Error("invalid cloud-init worker integration")
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"id":123,"label":"test-worker"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	l.base = upstream.URL + "/"
	payload, _ := json.Marshal(map[string]any{"label": "test-worker", "region": "sg-sin-2", "type": "g6-standard-2", "image": "linode/ubuntu24.04", "server_addr": "192.168.145.81:9090", "http_addr": "192.168.145.81:8080", "backups": true, "public_key": key})
	l.prepareTools = func(context.Context) error { return fmt.Errorf("fixture incomplete package") }
	blocked := httptest.NewRecorder()
	l.handle(blocked, httptest.NewRequest("POST", "/api/linode/workers", strings.NewReader(string(payload))))
	if blocked.Code != 503 || creates != 0 {
		t.Fatalf("incomplete package allowed charged creation: status=%d creates=%d", blocked.Code, creates)
	}
	l.prepareTools = nil
	for index, status := range []int{201, 409} {
		w := httptest.NewRecorder()
		l.handle(w, httptest.NewRequest("POST", "/api/linode/workers", strings.NewReader(string(payload))))
		if w.Code != status {
			t.Fatalf("request %d: status %d, %s", index, w.Code, w.Body.String())
		}
	}
	if creates != 1 {
		t.Fatalf("duplicate charged creation: %d", creates)
	}
}

func TestLinodeBatchCreation(t *testing.T) {
	for _, test := range []struct {
		name                       string
		count, failAt              int
		collision                  bool
		status, creates, confirmed int
	}{
		{"two workers", 2, 0, false, 201, 2, 2},
		{"stop after uncertain second result", 3, 2, false, 207, 2, 1},
		{"preflight entire batch", 3, 0, true, 409, 0, 0},
		{"reject zero count", 0, 0, false, 400, 0, 0},
		{"reject excessive count", 21, 0, false, 400, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			l := newLinodeIntegration(t.TempDir())
			os.MkdirAll(l.dir, 0700)
			if test.name == "two workers" {
				os.WriteFile(filepath.Join(filepath.Dir(l.dir), "worker-default-password"), []byte("fixture-policy-root-123!"), 0600)
			}
			os.WriteFile(filepath.Join(l.dir, "token"), []byte("test-provider-token"), 0600)
			key, _ := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, 32)))
			attempts := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					data := []any{}
					switch r.URL.Path {
					case "/regions":
						data = []any{map[string]any{"id": "sg-sin-2", "capabilities": []string{"Metadata"}}}
					case "/linode/types":
						data = []any{map[string]any{"id": "g6-standard-2"}}
					case "/images":
						data = []any{map[string]any{"id": "linode/ubuntu24.04"}}
					case "/linode/instances":
						if test.collision {
							data = []any{map[string]any{"label": "batch-worker-3"}}
						}
					default:
						t.Errorf("unexpected path %s", r.URL.Path)
					}
					json.NewEncoder(w).Encode(map[string]any{"data": data, "pages": 1})
					return
				}
				attempts++
				var body struct {
					Label    string `json:"label"`
					RootPass string `json:"root_pass"`
					Metadata struct {
						Data string `json:"user_data"`
					} `json:"metadata"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				want := fmt.Sprintf("batch-worker-%d", attempts)
				stored, saveErr := os.ReadFile(filepath.Join(filepath.Dir(l.dir), "worker-logins", body.Label))
				if saveErr != nil || body.RootPass == "" || string(stored) != body.RootPass {
					t.Error("provisioned worker password was not saved before creation")
				}
				if body.Label != want {
					t.Errorf("label %s, want %s", body.Label, want)
				}
				script, err := base64.StdEncoding.DecodeString(body.Metadata.Data)
				if test.name == "two workers" && (body.RootPass != "fixture-policy-root-123!" || !strings.Contains(string(script), "PermitRootLogin yes") || strings.Contains(string(script), body.RootPass)) {
					t.Error("default password policy was not applied privately")
				}
				if err != nil || !strings.Contains(string(script), `"worker_id":"`+want+`"`) {
					t.Error("workers share an identity or invalid bootstrap")
				}
				if attempts == test.failAt {
					w.WriteHeader(503)
					return
				}
				w.WriteHeader(201)
				json.NewEncoder(w).Encode(map[string]any{"id": 100 + attempts, "label": body.Label})
			}))
			defer upstream.Close()
			l.base = upstream.URL + "/"
			input, _ := json.Marshal(map[string]any{"label": "batch-worker", "count": test.count, "region": "sg-sin-2", "type": "g6-standard-2", "image": "linode/ubuntu24.04", "server_addr": "192.168.145.81:9090", "http_addr": "192.168.145.81:8080", "public_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))})
			w := httptest.NewRecorder()
			l.handle(w, httptest.NewRequest("POST", "/api/linode/workers", strings.NewReader(string(input))))
			if w.Code != test.status || attempts != test.creates {
				t.Fatalf("status %d, creates %d; %s", w.Code, attempts, w.Body.String())
			}
			if w.Code == 201 || w.Code == 207 {
				var result struct {
					Created      []json.RawMessage `json:"created"`
					Uncertain    string            `json:"uncertain_label"`
					NotAttempted []string          `json:"not_attempted"`
				}
				json.Unmarshal(w.Body.Bytes(), &result)
				if len(result.Created) != test.confirmed {
					t.Fatal("partial results lost")
				}
				if test.failAt > 0 && (result.Uncertain != "batch-worker-2" || len(result.NotAttempted) != 1 || result.NotAttempted[0] != "batch-worker-3") {
					t.Fatal("incorrect failure reconciliation")
				}
			}
		})
	}
}
func TestLinodeBatchLabelSuffixLimit(t *testing.T) {
	if _, err := linodeWorkerLabels(strings.Repeat("a", 64), 2); err == nil {
		t.Fatal("numbered label exceeds provider length")
	}
}
