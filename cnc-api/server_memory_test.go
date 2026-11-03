package cnc

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/cnc-processor/cluster"
)

func TestBrokerAdmitsSmallTaskBesideLegacyAutomaticReservation(t *testing.T) {
	t.Setenv("CNC_PROCESSOR_TOKEN", strings.Repeat("m", 32))
	s := processorTestServer(t)
	w := &Worker{ID: "live-worker", Status: WorkerStatusOnline, MaxTasks: 2, CurrentLoad: 1, SendCh: make(chan *Message, 2), ProcessorProtocol: cluster.ProtocolVersion, ProcessorVersions: []string{"version"}, Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 2, TotalMemoryMB: 3812, AvailableMemoryMB: 3197, JobMemoryMB: 51, Cgroups: true}}
	s.workers[w.ID] = w
	old := &Task{ID: "existing-enum", Type: "enum", Status: TaskStatusRunning, AssignedTo: w.ID, ExecutionResources: &cluster.Resources{CPUCores: 1, MemoryMB: 2116, MemoryBurst: true}}
	s.tasks[old.ID] = old
	a := cluster.Assignment{AttemptID: "small-attempt", WorkerID: w.ID, Version: "version", Token: "scoped-secret", Resources: cluster.Program{}.Resources()}
	body, _ := json.Marshal(map[string]any{"worker_id": w.ID, "assignment": a})
	r := httptest.NewRequest("POST", "/api/processor/cluster/assign", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	response := httptest.NewRecorder()
	s.handleProcessorBroker(response, r)
	if response.Code != 200 {
		t.Fatalf("live snapshot blocked: %d %s", response.Code, response.Body)
	}
	if old.Status != TaskStatusRunning || old.ExecutionResources.MemoryMB != 2116 {
		t.Fatal("running task altered")
	}
	if len(w.SendCh) != 1 {
		t.Fatal("unexpected existing-task redelivery")
	}
}

func memoryFixtureSnapshot() cluster.ResourceSnapshot {
	return cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096, Cgroups: true}
}

func TestDispatcherSharesMemoryAcrossShellAndProcessor(t *testing.T) {
	s := processorTestServer(t)
	w := &Worker{ID: "memory-worker", Status: WorkerStatusOnline, MaxTasks: 8, SendCh: make(chan *Message, 8), Resources: memoryFixtureSnapshot()}
	s.workers[w.ID] = w
	processor := &Task{ID: "processor", Type: "processor", Status: TaskStatusAssigned, AssignedTo: w.ID, ProcessorAssignment: &cluster.Assignment{Resources: cluster.Program{}.Resources()}}
	s.tasks[processor.ID] = processor
	first := &Task{ID: "first", Type: "shell", Status: TaskStatusPending, AssignedTo: w.ID}
	s.tasks[first.ID] = first
	s.dispatchTask(first)
	share := cluster.FairMemoryShareMB(cluster.Worker{MaxTasks: w.MaxTasks, Resources: w.Resources})
	if first.Status != TaskStatusAssigned || first.ExecutionResources == nil || first.ExecutionResources.MemoryMB != share {
		t.Fatalf("shell did not receive fair share: %+v", first)
	}
	reserved, load := s.processorReservations(w.ID)
	if reserved.MemoryMB != share+512 || load != 1 {
		t.Fatalf("combined reservations: %+v processor load=%d", reserved, load)
	}
	second := &Task{ID: "second", Type: "shell", Status: TaskStatusPending, AssignedTo: w.ID}
	s.tasks[second.ID] = second
	s.dispatchTask(second)
	if second.Status != TaskStatusAssigned || second.ExecutionResources.MemoryMB != share || len(w.SendCh) != 2 {
		t.Fatal("free slot was blocked by the first task")
	}
	third := &Task{ID: "third", Type: "shell", Status: TaskStatusPending, AssignedTo: w.ID}
	s.tasks[third.ID] = third
	s.dispatchTask(third)
	blocked := &Task{ID: "blocked", Type: "shell", Status: TaskStatusPending, AssignedTo: w.ID}
	s.tasks[blocked.ID] = blocked
	s.dispatchTask(blocked)
	if third.Status != TaskStatusAssigned || blocked.Status != TaskStatusPending || blocked.QueueReason == "" {
		t.Fatal("CPU capacity was not enforced")
	}
	first.Status = TaskStatusCompleted
	w.CurrentLoad = 3
	s.dispatchTask(blocked)
	if blocked.Status != TaskStatusAssigned || blocked.ExecutionResources.MemoryMB != share || blocked.QueueReason != "" {
		t.Fatal("finished reservation was not released")
	}
}
