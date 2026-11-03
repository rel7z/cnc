//go:build linux

package cnc

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/user/cnc-processor/cluster"
)

func TestShellReservationAboveOldLimit(t *testing.T) {
	if !cluster.CgroupAvailable() {
		t.Skip("writable cgroup v2 required")
	}
	agent, _ := workspaceAgent(t)
	limits := cluster.Resources{CPUCores: 1, MemoryMB: 768, Threads: 1}
	code := "import pathlib,json; data=bytearray(600*1024*1024); cg=pathlib.Path('/proc/self/cgroup').read_text().strip().split(':',2)[2]; p=pathlib.Path('/sys/fs/cgroup')/cg.lstrip('/'); print(json.dumps({'child':int((p/'memory.max').read_text()),'parent':int((p.parent/'memory.max').read_text()),'allocated_mb':600}))"
	result, err := agent.executeShellTask(&Task{ID: "larger-fixture", ExecutionResources: &limits, Payload: map[string]interface{}{"command": fmt.Sprintf("python3 -c '%s'", strings.ReplaceAll(code, "'", "'\\''")), "timeout_seconds": float64(15)}})
	if err != nil || result == nil || result.ExitCode != 0 {
		t.Fatalf("larger reservation failed: %+v %v", result, err)
	}
	var proof struct {
		Child, Parent int
		AllocatedMB   int `json:"allocated_mb"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &proof); err != nil || proof.Child != 768<<20 || proof.Parent != cluster.JobMemoryEmergencyMB(cluster.SampleResources().TotalMemoryMB)<<20 || proof.AllocatedMB != 600 {
		t.Fatalf("incorrect memory enforcement: %+v %v", proof, err)
	}
	assertShellWorkspacesClean(t, agent)
}

func TestShellResourceLimitKeepsWorkerAlive(t *testing.T) {
	if !cluster.CgroupAvailable() {
		t.Skip("writable cgroup v2 required")
	}
	agent, wire := workspaceAgent(t)
	limits := cluster.Resources{CPUCores: 1, MemoryMB: 512, Threads: 1}
	result, err := agent.executeShellTask(&Task{ID: "memory-fixture", ExecutionResources: &limits, Payload: map[string]interface{}{
		"command":     "python3 -c 'from pathlib import Path; Path(\"results.txt\").write_text(\"partial\\n\"); data=bytearray(640*1024*1024)'",
		"output_file": "results.txt", "timeout_seconds": float64(10),
	}})
	if err != nil || result == nil || result.ExitCode == 0 {
		t.Fatalf("oversized job was not stopped: %+v %v", result, err)
	}
	if workspaceStreams(t, wire)["memory-fixture"] != "partial\n" {
		t.Fatal("partial output was lost")
	}
	assertShellWorkspacesClean(t, agent)
	result, err = agent.executeShellTask(&Task{ID: "healthy-fixture", Payload: map[string]interface{}{
		"command": "printf 'worker-alive\\n'", "timeout_seconds": float64(10),
	}})
	if err != nil || result == nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "worker-alive") {
		t.Fatalf("worker did not survive oversized job: %+v %v", result, err)
	}
	assertShellWorkspacesClean(t, agent)
	t.Log("oversized shell job stopped; partial output retained; next job succeeded")
}
