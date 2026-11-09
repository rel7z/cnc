package cnc

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/cnc-processor/cluster"
)

type drainRecorder struct {
	mu       sync.Mutex
	messages []Message
}

func (r *drainRecorder) Write(raw []byte) (int, error) {
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.messages = append(r.messages, m)
	r.mu.Unlock()
	return len(raw), nil
}

func TestDrainKeepsCurrentTaskAliveAndWithdrawsCapacity(t *testing.T) {
	w := NewWorkerAgent(&WorkerConfig{WorkerID: "dummy", DataDir: t.TempDir(), MaxTasks: 2})
	w.drainQuiet = 30 * time.Millisecond
	w.drainPoll = 5 * time.Millisecond
	recorder := &drainRecorder{}
	w.encoder = json.NewEncoder(recorder)
	w.tasks["dummy"] = &Task{ID: "dummy"}
	done := make(chan error, 1)
	go func() { done <- w.DrainAndStop(context.Background()) }()
	time.Sleep(70 * time.Millisecond)
	if w.ctx.Err() != nil {
		t.Fatal("drain cancelled a running task")
	}
	select {
	case err := <-done:
		t.Fatal("drain exited with work still running", err)
	default:
	}
	recorder.mu.Lock()
	var registered RegisterWorkerPayload
	for _, msg := range recorder.messages {
		if msg.Type == MsgTypeRegisterWorker {
			_ = msg.UnmarshalPayload(&registered)
		}
	}
	recorder.mu.Unlock()
	if registered.Worker.MaxTasks != 0 || registered.Worker.CurrentLoad != 1 {
		t.Fatal("drain did not withdraw capacity while retaining active load", registered.Worker)
	}
	// Simulate final result delivery before releasing the task reservation.
	w.deliverTaskResult("dummy", &TaskResult{ExitCode: 0}, "")
	w.mu.Lock()
	delete(w.tasks, "dummy")
	w.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle drain never finished")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.messages) < 2 || recorder.messages[len(recorder.messages)-1].Type != MsgTypeTaskResult {
		t.Fatal("final result was not delivered before shutdown")
	}
}

func TestCancelledDrainObserverDoesNotCancelWorker(t *testing.T) {
	w := NewWorkerAgent(&WorkerConfig{WorkerID: "dummy", DataDir: t.TempDir(), MaxTasks: 1})
	w.encoder = json.NewEncoder(io.Discard)
	w.tasks["dummy"] = &Task{ID: "dummy"}
	w.drainPoll = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := w.DrainAndStop(ctx); err == nil {
		t.Fatal("expected observer deadline")
	}
	if w.ctx.Err() != nil {
		t.Fatal("observer timeout killed existing work")
	}
}

func TestLegacyGuardCountsChildrenAcrossAllGoThreads(t *testing.T) {
	proc := t.TempDir()
	for thread, children := range map[string]string{"42": "", "43": "100 101", "44": "101 102"} {
		p := filepath.Join(proc, "42/task", thread)
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "children"), []byte(children), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := workerChildCount(proc, 42)
	if err != nil || n != 3 {
		t.Fatalf("lost child processes on another OS thread: %d %v", n, err)
	}
	fields := append([]string{"S"}, strings.Fields(strings.Repeat("0 ", 18))...)
	fields = append(fields, "start-token")
	if err = os.WriteFile(filepath.Join(proc, "42/stat"), []byte("42 (worker with spaces) "+strings.Join(fields, " ")), 0600); err != nil {
		t.Fatal(err)
	}
	start, err := workerProcessStart(proc, 42)
	if err != nil || start != "start-token" {
		t.Fatal("PID-reuse protection failed", start, err)
	}
}

func TestZeroCapacityRegistrationBlocksProcessorDispatch(t *testing.T) {
	s := &Server{config: &ServerConfig{HTTPAddr: "127.0.0.1:8080"}, workers: map[string]*Worker{}, tasks: map[string]*Task{}}
	msg, _ := NewMessage(MsgTypeRegisterWorker, RegisterWorkerPayload{Worker: Worker{ID: "dummy", MaxTasks: 0, ProcessorProtocol: cluster.ProtocolVersion}})
	s.handleRegisterWorker(msg, make(chan *Message, 4), "127.0.0.1:1234")
	if s.workers["dummy"].MaxTasks != 0 {
		t.Fatal("controller restored capacity during drain")
	}
	token := strings.Repeat("s", 32)
	t.Setenv("CNC_PROCESSOR_TOKEN", token)
	req := httptest.NewRequest("POST", "/api/processor/cluster/assign", strings.NewReader(`{"worker_id":"dummy","install":{"version":"`+strings.Repeat("a", 64)+`"}}`))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleProcessorBroker(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "waiting for a CNC task slot") || len(s.tasks) != 0 {
		t.Fatal("a draining worker accepted new work", w.Code, w.Body.String())
	}
}
