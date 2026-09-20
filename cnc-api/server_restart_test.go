package cnc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusAPI(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &ServerConfig{
		HTTPAddr:   "127.0.0.1:0",
		TCPAddr:    "127.0.0.1:0",
		DataDir:    filepath.Join(tempDir, "data"),
		MaxRetries: 1,
	}

	server := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rr := httptest.NewRecorder()

	server.handleStatusAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Fatalf("expected status: ok, got %v", resp["status"])
	}
}

func TestRestartAPI(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &ServerConfig{
		HTTPAddr:   "127.0.0.1:0",
		TCPAddr:    "127.0.0.1:0",
		DataDir:    filepath.Join(tempDir, "data"),
		MaxRetries: 1,
	}

	server := NewServer(cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/server/restart", nil)
	rr := httptest.NewRecorder()

	server.handleServerRestartAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "restarting" {
		t.Fatalf("expected status: restarting, got %v", resp["status"])
	}

	// Give the goroutine time to trigger restart
	time.Sleep(300 * time.Millisecond)

	if !server.IsRestarting() {
		t.Fatalf("expected server.IsRestarting() to be true")
	}
}
