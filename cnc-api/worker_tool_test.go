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
		ctx: context.Background(),
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

	// 2. Test running cms-scan if built in tools/
	if _, err := os.Stat("tools/cms-scan"); err == nil {
		tempDir := t.TempDir()
		targetsFile := filepath.Join(tempDir, "targets.txt")
		os.WriteFile(targetsFile, []byte("example.com\n"), 0644)

		taskTool := &Task{
			ID: "task_test_cms_scan",
			Payload: map[string]interface{}{
				"command": "cms-scan -l " + targetsFile + " -t 1 -o " + filepath.Join(tempDir, "out"),
			},
		}

		resTool, err := agent.executeShellTask(taskTool)
		if err != nil {
			t.Fatalf("unexpected error running cms-scan: %v", err)
		}

		if resTool.ExitCode != 0 {
			t.Errorf("expected exit code 0 for cms-scan, got %d. Stderr: %s", resTool.ExitCode, resTool.Stderr)
		}

		if !strings.Contains(resTool.Stdout, "Starting CMS detection") {
			t.Errorf("expected stdout to contain scan header, got: %s", resTool.Stdout)
		}
	}
}
