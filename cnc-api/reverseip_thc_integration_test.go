package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReverseIPTHCLoadBalancedEnd2End(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cnc_integration_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Setup
	cfg := &ServerConfig{
		HTTPAddr: ":0",
		TCPAddr:  ":0",
		DataDir:  tempDir,
	}
	srv := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	srv.ctx = ctx
	srv.cancel = cancel

	// Create input file with IPs
	inputFile := filepath.Join(tempDir, "ips.txt")
	if err := os.WriteFile(inputFile, []byte("8.8.8.8\n1.1.1.1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test 1: Tool registry has correct configuration
	tool, ok := ToolsRegistry["reverseip-thc"]
	if !ok {
		t.Fatal("reverseip-thc not found in registry")
	}
	if tool.Scope != "worker" {
		t.Errorf("Expected scope 'worker', got '%s'", tool.Scope)
	}
	if tool.DefaultMode != "spread" {
		t.Errorf("Expected DefaultMode 'spread', got '%s'", tool.DefaultMode)
	}

	// Test 2: Command generation works correctly
	cmd, err := GenerateToolCommand("reverseip-thc", "{input}", map[string]interface{}{
		"threads": 2,
		"delay":   "100ms",
	})
	if err != nil {
		t.Fatalf("Command generation failed: %v", err)
	}
	if !strings.Contains(cmd, "reverseip-thc") {
		t.Errorf("Command should contain 'reverseip-thc', got: %s", cmd)
	}
	if !strings.Contains(cmd, "-w 2") {
		t.Errorf("Command should contain '-w 2', got: %s", cmd)
	}

	// Test 3: Job launch in spread mode
	payload := map[string]interface{}{
		"tool_id":     "reverseip-thc",
		"input_file":  inputFile,
		"output_file": "reverseip_results.txt",
		"options": map[string]interface{}{
			"threads": 2,
			"delay":   "100ms",
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/tools/launch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleToolsLaunchAPI(w, req)

	if w.Code != 201 {
		t.Fatalf("Expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Invalid response: %v", err)
	}

	if resp["mode"] != "spread" {
		t.Errorf("Expected mode 'spread', got %v", resp["mode"])
	}

	t.Logf("✓ reverseip-thc load-balanced integration test passed")
	t.Logf("  Tool Scope: %s (load-balanced)", tool.Scope)
	t.Logf("  Job Mode: %s (distributed)", resp["mode"])
	t.Logf("  Job ID: %v", resp["job_id"])
}
