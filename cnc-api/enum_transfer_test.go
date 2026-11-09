package cnc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnumScannerCompletionDoesNotWaitForSlowPublication(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "cnc-enum")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	build.Dir = "../cnc-backend/enum"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	t.Setenv("CNC_ENUM_BINARY", binary)
	var probes atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<meta name="generator" content="WordPress 6.4">`)
			return
		}
		http.NotFound(w, r)
	}))
	defer fixture.Close()
	_, rawPort, _ := net.SplitHostPort(strings.TrimPrefix(fixture.URL, "http://"))
	port, _ := strconv.Atoi(rawPort)
	s := NewServer(&ServerConfig{DataDir: t.TempDir(), GDrive: &GDriveWrapper{WatchDir: t.TempDir()}})
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/enum/results/") {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		s.handleEnumAPI(w, r)
	}))
	defer controller.Close()
	defer unblock()
	input := filepath.Join(t.TempDir(), "targets.txt")
	if err := os.WriteFile(input, []byte(fixture.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	jobID := launchFixtureEnum(t, s, input, port)
	task := waitEnumTask(t, s, jobID)
	s.mu.Lock()
	task.AssignedTo = "slow-transfer-worker"
	task.Status = TaskStatusAssigned
	s.mu.Unlock()
	worker := NewWorkerAgent(&WorkerConfig{WorkerID: "slow-transfer-worker", DataDir: t.TempDir(), MaxTasks: 1})
	defer worker.Stop()
	worker.serverHTTPAddr = controller.URL
	type answer struct {
		result *TaskResult
		err    error
	}
	finished := make(chan answer, 1)
	go func() { result, err := worker.executeEnumTask(task); finished <- answer{result, err} }()
	var result *TaskResult
	select {
	case got := <-finished:
		if got.err != nil || got.result.ExitCode != 0 || got.result.PublicationStatus != "pending" {
			t.Fatalf("%+v %v", got.result, got.err)
		}
		result = got.result
	case <-time.After(3 * time.Second):
		t.Fatal("execution completion waited for upload")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background transfer not started")
	}
	message, _ := NewMessage(MsgTypeTaskResult, TaskResultPayload{TaskID: task.ID, WorkerID: worker.config.WorkerID, Result: result})
	s.handleTaskResult(message)
	s.mu.RLock()
	status := s.jobs[jobID].Status
	publication := task.EnumPublication
	s.mu.RUnlock()
	if status != "completed" || publication != "pending" {
		t.Fatal(status, publication)
	}
	unblock()
	worker.wg.Wait()
	if probes.Load() != 6 {
		t.Fatal("publication reran the scanner")
	}
	s.mu.RLock()
	publication = task.EnumPublication
	s.mu.RUnlock()
	if publication != "accepted" {
		t.Fatal("completed background transfer not accepted")
	}
}

func TestEnumTransfersBoundConcurrencyAndRetainOutboxOnOutage(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	entered := make(chan struct{}, 10)
	var active, peak atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if p >= n || peak.CompareAndSwap(p, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			http.Error(w, "owned outage", 503)
		case <-r.Context().Done():
		}
	}))
	defer controller.Close()
	defer unblock()
	worker := NewWorkerAgent(&WorkerConfig{WorkerID: "bounded", DataDir: t.TempDir()})
	defer worker.Stop()
	worker.serverHTTPAddr = controller.URL
	var paths []string
	var entries []enumOutbox
	for i := 0; i < 3; i++ {
		path := filepath.Join(t.TempDir(), "result.jsonl")
		os.WriteFile(path, []byte("fixture\n"), 0600)
		sum := sha256.Sum256([]byte("fixture\n"))
		entry := enumOutbox{TaskID: fmt.Sprintf("task-%d", i), JobID: "job", Path: path, WorkerID: "bounded", Token: "owned", Basename: fmt.Sprintf("result-%d.jsonl", i), SHA256: hex.EncodeToString(sum[:])}
		outbox, err := worker.persistEnumPublication(entry)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
		paths = append(paths, outbox)
	}
	if !worker.startEnumPublication(worker.ctx, paths[0], entries[0]) || !worker.startEnumPublication(worker.ctx, paths[1], entries[1]) {
		t.Fatal("two transfers not admitted")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("transfer not started")
		}
	}
	if worker.startEnumPublication(worker.ctx, paths[2], entries[2]) || worker.startEnumPublication(worker.ctx, paths[0], entries[0]) {
		t.Fatal("capacity or duplicate gate bypassed")
	}
	unblock()
	worker.wg.Wait()
	if peak.Load() != 2 {
		t.Fatal("transfer concurrency not bounded", peak.Load())
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		var entry enumOutbox
		if err != nil || json.Unmarshal(data, &entry) != nil {
			t.Fatal("outbox lost during outage", err)
		}
	}
}
