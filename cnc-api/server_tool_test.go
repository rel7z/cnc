package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolsRegistryServerTools(t *testing.T) {
	thc, ok := ToolsRegistry["reverseip-thc"]
	if !ok {
		t.Fatalf("expected reverseip-thc to be registered")
	}
	if thc.Scope != "worker" {
		t.Errorf("expected reverseip-thc scope to be 'worker', got '%s'", thc.Scope)
	}
	if thc.InputFlag != "-f" {
		t.Errorf("expected reverseip-thc input flag to be '-f', got '%s'", thc.InputFlag)
	}
	if thc.OutputFlag != "-o" {
		t.Errorf("expected reverseip-thc output flag to be '-o', got '%s'", thc.OutputFlag)
	}

	domain, ok := ToolsRegistry["reverseip-domain"]
	if !ok {
		t.Fatalf("expected reverseip-domain to be registered")
	}
	if domain.Scope != "server" {
		t.Errorf("expected reverseip-domain scope to be 'server', got '%s'", domain.Scope)
	}
	if domain.InputFlag != "-l" {
		t.Errorf("expected reverseip-domain input flag to be '-l', got '%s'", domain.InputFlag)
	}
	if domain.OutputFlag != "-out" {
		t.Errorf("expected reverseip-domain output flag to be '-out', got '%s'", domain.OutputFlag)
	}
}

func TestGenerateToolCommandServerTools(t *testing.T) {
	// 1. reverseip-thc
	thcCmd, err := GenerateToolCommand("reverseip-thc", "targets.txt", map[string]interface{}{
		"threads":     3,
		"delay":       "300ms",
		"output_file": "thc_out.txt",
		"format":      "domains",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(thcCmd, "reverseip-thc") {
		t.Errorf("expected command to start with reverseip-thc, got: %s", thcCmd)
	}
	if !strings.Contains(thcCmd, "-f targets.txt") {
		t.Errorf("expected -f targets.txt, got: %s", thcCmd)
	}
	if !strings.Contains(thcCmd, "-o thc_out.txt") {
		t.Errorf("expected -o thc_out.txt, got: %s", thcCmd)
	}
	if !strings.Contains(thcCmd, "-w 3") {
		t.Errorf("expected -w 3, got: %s", thcCmd)
	}
	if !strings.Contains(thcCmd, "-delay 300ms") {
		t.Errorf("expected -delay 300ms, got: %s", thcCmd)
	}

	// 2. reverseip-domain
	domainCmd, err := GenerateToolCommand("reverseip-domain", "ips.txt", map[string]interface{}{
		"threads":     10,
		"api_key":     "testkey123",
		"output_file": "domain_out.txt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(domainCmd, "reverseip-domain") {
		t.Errorf("expected command to start with reverseip-domain, got: %s", domainCmd)
	}
	if !strings.Contains(domainCmd, "-l ips.txt") {
		t.Errorf("expected -l ips.txt, got: %s", domainCmd)
	}
	if !strings.Contains(domainCmd, "-out domain_out.txt") {
		t.Errorf("expected -out domain_out.txt, got: %s", domainCmd)
	}
	if !strings.Contains(domainCmd, "-c 10") {
		t.Errorf("expected -c 10, got: %s", domainCmd)
	}
	if !strings.Contains(domainCmd, "-key testkey123") {
		t.Errorf("expected -key testkey123, got: %s", domainCmd)
	}
}

func TestServerJobExecution(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cnc_server_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	watchDir := filepath.Join(tempDir, "merged")
	_ = os.MkdirAll(watchDir, 0755)

	cfg := &ServerConfig{
		HTTPAddr: ":0",
		TCPAddr:  ":0",
		DataDir:  tempDir,
		GDrive: &GDriveWrapper{
			Enabled:  true,
			WatchDir: watchDir,
		},
	}

	srv := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	srv.ctx = ctx
	srv.cancel = cancel

	// Submit a server-mode job that writes output
	job := &Job{
		Name:           "Test Echo Server Job",
		Command:        "echo 'example.com' && echo 'google.com'",
		Mode:           JobModeServer,
		OutputFile:     "domains_result.txt",
		TimeoutSeconds: 5,
	}

	srv.submitJob(job)

	// Wait for server job to finish (asynchronous)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		status := job.Status
		srv.mu.RUnlock()

		if status == "completed" || status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	srv.mu.RLock()
	finalStatus := job.Status
	completed := job.Completed
	failed := job.Failed
	srv.mu.RUnlock()

	if finalStatus != "completed" {
		t.Fatalf("expected job status 'completed', got '%s'", finalStatus)
	}
	if completed != 1 || failed != 0 {
		t.Errorf("expected 1 completed and 0 failed, got completed=%d, failed=%d", completed, failed)
	}

	// Verify output in watcher directory
	outPath := filepath.Join(watchDir, "domains_result.txt")
	outData, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read expected output file in watchDir: %v", err)
	}
	if !strings.Contains(string(outData), "example.com") {
		t.Errorf("expected output to contain example.com, got: %s", string(outData))
	}
}

func TestToolsLaunchAPIServerMode(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cnc_server_api_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cfg := &ServerConfig{
		HTTPAddr: ":0",
		TCPAddr:  ":0",
		DataDir:  tempDir,
	}

	srv := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	srv.ctx = ctx
	srv.cancel = cancel

	payload := map[string]interface{}{
		"tool_id":     "reverseip-thc",
		"input_file":  "/tmp/dummy_ips.txt",
		"output_file": "dummy_out.txt",
		"options": map[string]interface{}{
			"threads": 2,
			"delay":   "100ms",
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/tools/launch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleToolsLaunchAPI(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}

	if resp["mode"] != "server" {
		t.Errorf("expected mode 'server', got %v", resp["mode"])
	}
	jobID, ok := resp["job_id"].(string)
	if !ok || jobID == "" {
		t.Errorf("expected valid job_id in response")
	}
}

func TestToolsRegistryDork2(t *testing.T) {
	dork, ok := ToolsRegistry["dork2"]
	if !ok {
		t.Fatalf("expected dork2 to be registered in ToolsRegistry")
	}
	if dork.Scope != "worker" {
		t.Errorf("expected dork2 scope to be 'worker', got '%s'", dork.Scope)
	}
	if dork.DefaultMode != JobModeSpread {
		t.Errorf("expected dork2 default_mode to be '%s', got '%s'", JobModeSpread, dork.DefaultMode)
	}
	if dork.InputFlag != "-f" {
		t.Errorf("expected dork2 input flag to be '-f', got '%s'", dork.InputFlag)
	}
	if dork.OutputFlag != "-o" {
		t.Errorf("expected dork2 output flag to be '-o', got '%s'", dork.OutputFlag)
	}
}

func TestGenerateToolCommandDork2(t *testing.T) {
	cmd, err := GenerateToolCommand("dork2", "{input}", map[string]interface{}{
		"pages":       3,
		"country":     "us",
		"lang":        "en",
		"output_file": "dork_out.txt",
	})
	if err != nil {
		t.Fatalf("unexpected error generating dork2 command: %v", err)
	}
	if !strings.Contains(cmd, "dork2") {
		t.Errorf("expected command to start with dork2, got: %s", cmd)
	}
	if !strings.Contains(cmd, "-f {input}") {
		t.Errorf("expected command to contain '-f {input}', got: %s", cmd)
	}
	if !strings.Contains(cmd, "-o dork_out.txt") {
		t.Errorf("expected command to contain '-o dork_out.txt', got: %s", cmd)
	}
	if !strings.Contains(cmd, "-p 3") {
		t.Errorf("expected command to contain '-p 3', got: %s", cmd)
	}
	if !strings.Contains(cmd, "-c us") {
		t.Errorf("expected command to contain '-c us', got: %s", cmd)
	}
}

func TestToolsLaunchAPISpreadMode(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cnc_spread_api_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create a dummy queries input file
	dummyInput := filepath.Join(tempDir, "queries.txt")
	if err := os.WriteFile(dummyInput, []byte("inurl:admin\ninurl:login\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &ServerConfig{
		HTTPAddr: ":0",
		TCPAddr:  ":0",
		DataDir:  tempDir,
	}

	srv := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	srv.ctx = ctx
	srv.cancel = cancel

	payload := map[string]interface{}{
		"tool_id":     "dork2",
		"input_file":  dummyInput,
		"output_file": "dork_results.txt",
		"options": map[string]interface{}{
			"pages":   2,
			"country": "us",
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/tools/launch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleToolsLaunchAPI(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}

	if resp["mode"] != "spread" {
		t.Errorf("expected mode 'spread', got %v", resp["mode"])
	}
	cmd, ok := resp["command"].(string)
	if !ok || !strings.Contains(cmd, "{input}") {
		t.Errorf("expected command with {input} placeholder, got %v", resp["command"])
	}
}

func TestRemovedCredentialTool(t *testing.T) {
	if _, exists := ToolsRegistry["wp-bruter"]; exists {
		t.Fatal("removed tool remains registered")
	}
	if _, err := GenerateToolCommand("wp-bruter", "{input}", nil); err == nil {
		t.Fatal("removed tool can still generate commands")
	}
}

func TestRemovedCredentialToolLaunchRejected(t *testing.T) {
	srv := NewServer(&ServerConfig{HTTPAddr: ":0", TCPAddr: ":0", DataDir: t.TempDir()})
	req := httptest.NewRequest(http.MethodPost, "/api/tools/launch", strings.NewReader(`{"tool_id":"wp-bruter","passlist":"/tmp/passwords","options":{}}`))
	response := httptest.NewRecorder()
	srv.handleToolsLaunchAPI(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("removed tool returned %d: %s", response.Code, response.Body.String())
	}
	if len(srv.jobs) != 0 {
		t.Fatal("removed tool created a job")
	}
}
