package cnc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var workerLoginID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// Passwords are controller-local data, separate from public worker metadata.
func saveWorkerPassword(dataDir, id, password string) error {
	if !workerLoginID.MatchString(id) || password == "" || len(password) > 1024 || strings.ContainsAny(password, "\r\n\x00") {
		return fmt.Errorf("invalid worker password")
	}
	dir := filepath.Join(dataDir, "worker-logins")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot save worker password")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("cannot secure worker password storage")
	}
	f, err := os.CreateTemp(dir, ".password-")
	if err != nil {
		return fmt.Errorf("cannot save worker password")
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(password); err != nil {
		f.Close()
		return fmt.Errorf("cannot save worker password")
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("cannot save worker password")
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, id)); err != nil {
		return fmt.Errorf("cannot save worker password")
	}
	return nil
}

func (s *Server) handleWorkerLoginAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, message string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	local, _, _ := net.SplitHostPort(s.config.HTTPAddr)
	if !linodeLocalCaller(remote, local) {
		fail(403, "worker passwords require the local dashboard proxy")
		return
	}
	if r.Method != "POST" {
		fail(405, "method not allowed")
		return
	}
	var input struct {
		WorkerID string `json:"worker_id"`
		Action   string `json:"action"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || !workerLoginID.MatchString(input.WorkerID) {
		fail(400, "invalid worker login request")
		return
	}
	s.mu.Lock()
	_, exists := s.workers[input.WorkerID]
	s.mu.Unlock()
	if !exists {
		fail(404, "worker not found")
		return
	}
	switch input.Action {
	case "save":
		if err := saveWorkerPassword(s.config.DataDir, input.WorkerID, input.Password); err != nil {
			fail(400, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"saved": true})
	case "reveal":
		password, err := os.ReadFile(filepath.Join(s.config.DataDir, "worker-logins", input.WorkerID))
		if os.IsNotExist(err) {
			fail(404, "No password is saved for this worker. Use your SSH key, or save its known password below.")
			return
		}
		if err != nil {
			fail(500, "cannot read worker password")
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"password": string(password)})
	default:
		fail(400, "invalid worker login action")
	}
}

// A deployment-owned file survives application releases. Missing policy keeps
// existing random/key-only provisioning behavior; an invalid policy fails closed.
func workerRootPassword(dataDir string) (string, error) {
	path := os.Getenv("CNC_WORKER_ROOT_PASSWORD_FILE")
	if path == "" {
		path = filepath.Join(dataDir, "worker-default-password")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read worker password policy")
	}
	password := strings.TrimSuffix(string(data), "\n")
	if password == "" || len(password) > 1024 || strings.ContainsAny(password, "\r\n\x00") {
		return "", fmt.Errorf("invalid worker password policy")
	}
	return password, nil
}

func workerPasswordSetup(password string) (string, error) {
	if password == "" {
		return "", nil
	}
	cmd := exec.Command("openssl", "passwd", "-6", "-stdin")
	cmd.Stdin = bytes.NewBufferString(password + "\n")
	output, err := cmd.Output()
	hash := strings.TrimSpace(string(output))
	if err != nil || !strings.HasPrefix(hash, "$6$") || strings.ContainsAny(hash, "'\r\n") {
		return "", fmt.Errorf("cannot prepare worker password policy")
	}
	return "\nprintf '%s\\n' 'root:" + hash + "' | chpasswd -e\ninstall -d -m 755 /etc/ssh/sshd_config.d\nprintf '%s\\n' 'PermitRootLogin prohibit-password' 'PubkeyAuthentication yes' 'PasswordAuthentication no' 'KbdInteractiveAuthentication no' 'AuthenticationMethods publickey' > /etc/ssh/sshd_config.d/00-cnc-worker-login.conf\n/usr/sbin/sshd -t\nsystemctl reload ssh\n", nil
}
