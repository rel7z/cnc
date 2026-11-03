//go:build linux

package cluster

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellCommandMemoryLimitProtectsHost(t *testing.T) {
	if !CgroupAvailable() {
		t.Skip("writable cgroup v2 is required for enforcement verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", "import pathlib,resource; print(pathlib.Path('/proc/self/cgroup').read_text(),flush=True); data=bytearray(256*1024*1024); print('unexpected success rss=',resource.getrusage(resource.RUSAGE_SELF).ru_maxrss)")
	id := fmt.Sprintf("shell-limit-fixture-%d", os.Getpid())
	cleanup, err := ConfigureCommandLimits(cmd, id, Resources{CPUCores: 1, MemoryMB: 64, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	dir := filepath.Join(cgroupRoot(), id)
	total, _, err := hostMemoryMB()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"memory.high": "max", "memory.max": fmt.Sprint(int64(JobMemoryEmergencyMB(total)) << 20), "memory.swap.max": "0"} {
		data, err := os.ReadFile(filepath.Join(cgroupRoot(), name))
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Fatalf("shared %s=%q, want %s: %v", name, data, want, err)
		}
	}
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || strings.Contains(string(self), cgroupRoot()) {
		t.Fatal("host test process must remain outside job cgroup")
	}
	for name, want := range map[string]string{"cpu.max": "100000 100000", "memory.max": "67108864", "memory.swap.max": "0"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Fatalf("%s=%q, want %s: %v", name, data, want, err)
		}
	}
	if data, err := cmd.CombinedOutput(); err == nil || ctx.Err() != nil {
		t.Fatalf("oversized fixture must be killed by its memory limit: %s %v", data, err)
	}
	events, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil || !strings.Contains(string(events), "oom_kill 1") {
		t.Fatalf("missing per-job OOM evidence: %s %v", events, err)
	}
	// The test process and its host remain alive after the child is killed.
	t.Log("fixture exceeded 64 MiB; only its job cgroup was killed")
}

func TestShellCommandFailsClosedWithoutCgroups(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-cgroup", "jobs")
	t.Setenv("CNC_PROCESSOR_CGROUP_ROOT", root)
	cmd := exec.Command("true")
	if cleanup, err := ConfigureCommandLimits(cmd, "fixture", Resources{CPUCores: 1, MemoryMB: 64}); err == nil {
		cleanup()
		t.Fatal("unenforced Linux job was admitted")
	}
}
