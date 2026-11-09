package cnc

import (
	"fmt"
	"testing"
)

func TestDorkCapacityQueuesAcrossJobsAndReleases(t *testing.T) {
	s := processorTestServer(t)
	w := &Worker{ID: "available", Status: WorkerStatusOnline, MaxTasks: 8, SendCh: make(chan *Message, 8), Resources: memoryFixtureSnapshot()}
	s.workers[w.ID] = w
	for i := 0; i < 5; i++ {
		id := fmt.Sprint(i)
		s.tasks[id] = &Task{ID: id, JobID: "different-job-" + id, Status: TaskStatusAssigned, Payload: map[string]interface{}{"command": "./tools/dork2 -f local.txt"}}
	}
	pending := &Task{ID: "pending", Status: TaskStatusPending, AssignedTo: w.ID, Payload: map[string]interface{}{"command": "./tools/dork2 -f other.txt"}}
	s.tasks[pending.ID] = pending
	s.dispatchTask(pending)
	if pending.Status != TaskStatusPending || pending.QueueReason == "" || len(w.SendCh) != 0 {
		t.Fatal("provider capacity not enforced")
	}
	ordinary := &Task{ID: "ordinary", Status: TaskStatusPending, AssignedTo: w.ID, Payload: map[string]interface{}{"command": "printf local"}}
	s.tasks[ordinary.ID] = ordinary
	s.dispatchTask(ordinary)
	if ordinary.Status != TaskStatusAssigned {
		t.Fatal("unrelated command blocked")
	}
	s.tasks["0"].Status = TaskStatusCompleted
	s.dispatchTask(pending)
	if pending.Status != TaskStatusAssigned || pending.QueueReason != "" {
		t.Fatal("capacity not released")
	}
}
