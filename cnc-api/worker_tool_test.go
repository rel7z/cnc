package cnc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerExecuteTaskToolsResolution(t *testing.T) {
	agent := &WorkerAgent{
		ctx:         context.Background(),
		taskCancels: make(map[string]context.CancelFunc),
		config:      &WorkerConfig{WorkerID: "test-worker"},
	}

	// 1. Test running an unknown command produces exit 127 with diagnostic hint
	taskUnknown := &Task{
		ID: "task_test_unknown",
		Payload: map[string]interface{}{
			"command": "nonexistent-tool-xyz -a -b",
		},
	}

	res, err := agent.executeShellTask(taskUnknown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.ExitCode != 127 {
		t.Errorf("expected exit code 127 for nonexistent tool, got %d", res.ExitCode)
	}

	if !strings.Contains(res.Stderr, "[diagnostic]") {
		t.Errorf("expected diagnostic note in stderr for exit 127, got: %s", res.Stderr)
	}

	// Verify resolution with an isolated executable, independent of platform
	// binaries and real network targets.
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	toolsDir := filepath.Join(tempDir, "tools")
	if err := os.Mkdir(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "fixture-tool"), []byte("#!/bin/sh\nprintf 'resolved:%s\\n' \"$1\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	resTool, err := agent.executeShellTask(&Task{ID: "tool-resolution", Payload: map[string]interface{}{"command": "fixture-tool dummy.invalid"}})
	if err != nil || resTool.ExitCode != 0 || strings.TrimSpace(resTool.Stdout) != "resolved:dummy.invalid" {
		t.Fatalf("tool resolution: %+v %v", resTool, err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "missing-runtime"), []byte("#!/usr/bin/env cnc-fixture-runtime-does-not-exist\n"), 0755); err != nil {
		t.Fatal(err)
	}
	missing, err := agent.executeShellTask(&Task{ID: "missing-runtime", Payload: map[string]interface{}{"command": "./tools/missing-runtime"}})
	if err != nil || missing.ExitCode != 127 || !strings.Contains(taskFailureSummary(missing), "interpreter is missing") {
		t.Fatalf("missing interpreter diagnostic: %+v %v", missing, err)
	}
}
