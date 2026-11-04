package cnc

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerLoginPersistenceAndAccess(t *testing.T) {
	dir := t.TempDir()
	s := &Server{config: &ServerConfig{DataDir: dir, HTTPAddr: "192.168.1.1:8080"}, workers: map[string]*Worker{"worker-1": {}}}
	request := func(body, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/workers/login", strings.NewReader(body))
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		s.handleWorkerLoginAPI(w, r)
		return w
	}
	if w := request(`{"worker_id":"worker-1","action":"reveal"}`, "127.0.0.1:1234"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	secret := "fixture worker password"
	body, _ := json.Marshal(map[string]string{"worker_id": "worker-1", "action": "save", "password": secret})
	w := request(string(body), "127.0.0.1:1234")
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatal("save response leaked password or failed")
	}
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Join(dir, "worker-logins"), 0700}, {filepath.Join(dir, "worker-logins", "worker-1"), 0600}} {
		stat, err := os.Stat(p.path)
		if err != nil || stat.Mode().Perm() != p.mode {
			t.Fatal("unsafe password permissions")
		}
	}
	// A fresh controller reads the persisted credential.
	s = &Server{config: s.config, workers: s.workers}
	w = request(`{"worker_id":"worker-1","action":"reveal"}`, "127.0.0.1:1234")
	var got map[string]string
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["password"] != secret || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("reveal failed")
	}
	for _, tc := range []struct {
		body, remote string
		status       int
	}{
		{`{"worker_id":"worker-1","action":"reveal"}`, "192.168.1.2:1234", 403},
		{`{"worker_id":"worker-2","action":"reveal"}`, "127.0.0.1:1234", 404},
		{`{"worker_id":"../outside","action":"reveal"}`, "127.0.0.1:1234", 400},
		{`{"worker_id":"worker-1","action":"save","password":""}`, "127.0.0.1:1234", 400},
		{`{"worker_id":"worker-1","action":"reset"}`, "127.0.0.1:1234", 400},
	} {
		w = request(tc.body, tc.remote)
		if w.Code != tc.status || strings.Contains(w.Body.String(), secret) {
			t.Fatalf("status %d want %d", w.Code, tc.status)
		}
	}
	r := httptest.NewRequest("GET", "/api/workers/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w = httptest.NewRecorder()
	s.handleWorkerLoginAPI(w, r)
	if w.Code != 405 {
		t.Fatal("GET allowed")
	}
}

func TestWorkerDefaultPasswordPolicy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CNC_WORKER_ROOT_PASSWORD_FILE", "")
	if got, err := workerRootPassword(dir); err != nil || got != "" {
		t.Fatal("missing policy changed default behavior")
	}
	fixture := "test-root-policy-123!"
	if err := os.WriteFile(filepath.Join(dir, "worker-default-password"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := workerRootPassword(dir)
	if err != nil || got != fixture {
		t.Fatal("policy not loaded")
	}
	setup, err := workerPasswordSetup(got)
	if err != nil || strings.Contains(setup, fixture) || !strings.Contains(setup, "chpasswd -e") || !strings.Contains(setup, "PermitRootLogin prohibit-password") || !strings.Contains(setup, "PasswordAuthentication no") || strings.Contains(setup, "PasswordAuthentication yes") {
		t.Fatal("invalid password bootstrap")
	}
	if err := os.WriteFile(filepath.Join(dir, "worker-default-password"), []byte("bad\npolicy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workerRootPassword(dir); err == nil {
		t.Fatal("invalid policy accepted")
	}
}
