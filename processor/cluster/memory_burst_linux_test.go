//go:build linux

package cluster

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryBurstSurvivesSoftThreshold(t *testing.T) {
	if !CgroupAvailable() {
		t.Skip("writable cgroup v2 required")
	}
	root := fmt.Sprintf("/sys/fs/cgroup/cnc-burst-fixture-%d", os.Getpid())
	t.Setenv("CNC_PROCESSOR_CGROUP_ROOT", root)
	defer os.Remove(root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", "import time; data=bytearray(96*1024*1024); print('burst-survived',flush=True); time.sleep(2)")
	cleanup, err := ConfigureCommandLimits(cmd, "burst", Resources{CPUCores: 1, MemoryMB: 64, MemoryBurst: true})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// Scale the production policy down in this isolated test-only cgroup.
	for name, value := range map[string]string{"memory.high": "max", "memory.max": "201326592"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewScanner(stdout)
	if !reader.Scan() || reader.Text() != "burst-survived" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("burst fixture did not reach its live allocation")
	}
	if used := jobMemoryUsageMB(); used <= 64 {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("burst telemetry missed live allocation: %d MiB", used)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("burst fixture did not finish: %v", err)
	}
	events, err := os.ReadFile(filepath.Join(root, "memory.events"))
	if err != nil || !strings.Contains(string(events), "oom_kill 0") {
		t.Fatalf("unexpected burst OOM: %s %v", events, err)
	}
	t.Log("96 MiB allocation exceeded its 64 MiB admission reservation with 192 MiB hard protection")
}
