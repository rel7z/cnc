package cnc

import (
	"bytes"
	"encoding/json"
	"github.com/user/cnc-processor/cluster"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func processorTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(nil)
	t.Cleanup(s.cancel)
	return s
}

func TestBrokerSerializesStableWorkspaceBeforeDispatch(t *testing.T) {
	t.Setenv("CNC_PROCESSOR_TOKEN", strings.Repeat("m", 32))
	s := processorTestServer(t)
	w := &Worker{ID: "worker", Status: WorkerStatusOnline, MaxTasks: 8, CurrentLoad: 1, SendCh: make(chan *Message, 8), ProcessorProtocol: cluster.ProtocolVersion, ProcessorVersions: []string{"version"}, Metadata: map[string]string{"processor_workspace_root": "/root/processor"}, Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 8, TotalMemoryMB: 8192, AvailableMemoryMB: 8192, Cgroups: true}}
	s.workers[w.ID] = w
	s.tasks["active"] = &Task{ID: "active", Type: "processor", AssignedTo: w.ID, Status: TaskStatusRunning, ProcessorAssignment: &cluster.Assignment{ProgramID: "demo--first"}}
	request := func(program string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"worker_id": w.ID, "assignment": cluster.Assignment{AttemptID: "next", WorkerID: w.ID, ProgramID: program, Version: "version"}})
		r := httptest.NewRequest("POST", "/api/processor/cluster/assign", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
		response := httptest.NewRecorder()
		s.handleProcessorBroker(response, r)
		return response
	}
	response := request("demo--second")
	if response.Code != 409 || !strings.Contains(response.Body.String(), "workspace") || len(w.SendCh) != 0 {
		t.Fatal("same workspace dispatched", response.Code, response.Body.String())
	}
	response = request("other--first")
	if response.Code != 200 || len(w.SendCh) != 1 {
		t.Fatal("unrelated workspace blocked", response.Code, response.Body.String())
	}
}
func TestBrokerCountsOrdinaryReservationsAndDeduplicatesProcessor(t *testing.T) {
	t.Setenv("CNC_PROCESSOR_TOKEN", strings.Repeat("m", 32))
	s := processorTestServer(t)
	w := &Worker{ID: "worker", Status: WorkerStatusOnline, MaxTasks: 2, SendCh: make(chan *Message, 10), Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096}, ProcessorProtocol: cluster.ProtocolVersion, ProcessorVersions: []string{"version"}}
	s.workers[w.ID] = w
	s.tasks["ordinary"] = &Task{ID: "ordinary", Type: "shell", AssignedTo: w.ID, Status: TaskStatusAssigned}
	a := cluster.Assignment{AttemptID: "attempt", WorkerID: w.ID, Version: "version", Token: "scoped-secret"}
	assign := func() int {
		body, _ := json.Marshal(map[string]any{"worker_id": w.ID, "assignment": a})
		req := httptest.NewRequest("POST", "/api/processor/cluster/assign", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
		rec := httptest.NewRecorder()
		s.handleProcessorBroker(rec, req)
		return rec.Code
	}
	if code := assign(); code != 200 {
		t.Fatalf("first assignment: %d", code)
	}
	if w.CurrentLoad != 2 || len(w.SendCh) != 1 {
		t.Fatalf("capacity accounting: load %d messages %d", w.CurrentLoad, len(w.SendCh))
	}
	if code := assign(); code != 200 || len(w.SendCh) != 1 {
		t.Fatalf("duplicate delivery enqueued again: %d / %d", code, len(w.SendCh))
	}
	heartbeat, _ := NewMessage(MsgTypeWorkerHeartbeat, WorkerHeartbeatPayload{WorkerID: w.ID, Status: WorkerStatusOnline, CurrentLoad: 0, Resources: cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096}, ProcessorProtocol: cluster.ProtocolVersion, ProcessorVersions: []string{"version"}})
	s.handleWorkerHeartbeat(heartbeat)
	a.AttemptID = "second"
	if code := assign(); code != 409 {
		t.Fatalf("stale heartbeat released reservations: %d", code)
	}
	data, _ := json.Marshal(s.tasks["attempt"])
	if strings.Contains(string(data), "scoped-secret") {
		t.Fatal("task JSON exposes scoped credential")
	}
	received := <-w.SendCh
	var payload AssignTaskPayload
	if err := received.UnmarshalPayload(&payload); err != nil || payload.ProcessorAssignment == nil || payload.ProcessorAssignment.Token != "scoped-secret" {
		t.Fatal("worker did not receive scoped assignment")
	}
	result, _ := NewMessage(MsgTypeTaskResult, TaskResultPayload{TaskID: "attempt", WorkerID: w.ID, Result: &TaskResult{ExitCode: 0}})
	s.handleTaskResult(result)
	after := w.CurrentLoad
	s.handleTaskResult(result)
	if w.CurrentLoad != after {
		t.Fatal("duplicate result decremented capacity twice")
	}
}
func TestGenericDispatcherUsesCapacityRatioAndCountsExactlyOnce(t *testing.T) {
	s := processorTestServer(t)
	s.workers["small"] = &Worker{ID: "small", Status: WorkerStatusOnline, Resources: memoryFixtureSnapshot(), CurrentLoad: 1, MaxTasks: 2, SendCh: make(chan *Message, 5)}
	s.workers["large"] = &Worker{ID: "large", Status: WorkerStatusOnline, Resources: memoryFixtureSnapshot(), CurrentLoad: 2, MaxTasks: 8, SendCh: make(chan *Message, 5)}
	task := &Task{ID: "ordinary", Type: "shell", Status: TaskStatusPending}
	s.tasks[task.ID] = task
	s.dispatchTask(task)
	if task.AssignedTo != "large" || s.workers["large"].CurrentLoad != 3 {
		t.Fatalf("ratio/count: assigned %s load %d", task.AssignedTo, s.workers["large"].CurrentLoad)
	}
	full := &Worker{ID: "full", Status: WorkerStatusOnline, CurrentLoad: 2, MaxTasks: 2, SendCh: make(chan *Message, 5)}
	s.workers[full.ID] = full
	broadcast := &Task{ID: "broadcast", Type: "shell", Status: TaskStatusPending, AssignedTo: full.ID}
	s.tasks[broadcast.ID] = broadcast
	s.dispatchTask(broadcast)
	if broadcast.Status != TaskStatusPending || len(full.SendCh) != 0 {
		t.Fatal("preassigned broadcast bypassed worker capacity")
	}
}
func TestOfflineProcessorNeverEntersOrdinaryRetryQueue(t *testing.T) {
	s := processorTestServer(t)
	now := time.Now()
	task := &Task{ID: "attempt", Type: "processor", AssignedTo: "worker", Status: TaskStatusAssigned, StartedAt: &now}
	s.tasks[task.ID] = task
	s.requeueWorkerTasks("worker")
	if task.Status != TaskStatusFailed || len(s.taskQueue) != 0 {
		t.Fatal("CNC retried processor outside SQLite lease control")
	}
}
func TestWorkerCapacityRejectsAndDuplicateDeliveryIsIgnored(t *testing.T) {
	worker := NewWorkerAgent(&WorkerConfig{WorkerID: "worker", MaxTasks: 1, DataDir: t.TempDir()})
	defer worker.cancel()
	worker.tasks["active"] = &Task{ID: "active", Type: "processor"}
	for _, id := range []string{"active", "extra"} {
		msg, _ := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: Task{ID: id, Type: "processor"}})
		worker.handleAssignTask(msg)
	}
	if len(worker.tasks) != 1 || worker.tasks["extra"] != nil {
		t.Fatal("worker accepted duplicate or over-capacity execution")
	}
}
