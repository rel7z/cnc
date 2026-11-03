package cnc

import (
	"bytes"
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
	"sync/atomic"
	"testing"
	"time"

	"cnc-enum/version"
)

func waitEnumTask(t *testing.T, s *Server, jobID string) *Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		ids := s.jobTasks[jobID]
		var task *Task
		if len(ids) > 0 {
			task = s.tasks[ids[0]]
		}
		s.mu.RUnlock()
		if task != nil {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("enum task was not prepared")
	return nil
}
func launchFixtureEnum(t *testing.T, s *Server, input string, port int) string {
	t.Helper()
	// Exercise omitted scope through real scanner execution, publication, retry and recovery.
	_ = port
	body, _ := json.Marshal(map[string]interface{}{"tool_id": "enum", "input_file": input, "version_rules": []version.VersionRule{{ID: "core", Name: "Owned core fixture", Component: "wordpress:core", Clauses: []version.VersionClause{{Operator: "=", Version: "6.4"}}}}})
	response := httptest.NewRecorder()
	s.handleToolsLaunchAPI(response, signedEnumRequest(t, body))
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(response.Body.Bytes(), &created) != nil || created.JobID == "" {
		t.Fatal(response.Body.String())
	}
	return created.JobID
}

func TestEnumWorkerArtifactToDurableJSONLAndTransferRecovery(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "cnc-enum")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	build.Dir = "../cnc-backend/enum"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build scanner: %v %s", err, output)
	}
	t.Setenv("CNC_ENUM_BINARY", binary)
	t.Setenv("ENUM_MAX_INFLIGHT_REQUESTS", "2")
	var evidenceRequests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evidenceRequests.Add(1)
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><meta name="generator" content="WordPress 6.4"><p>private-body-marker</p></html>`)
			return
		}
		http.NotFound(w, r)
	}))
	defer fixture.Close()
	_, rawPort, _ := net.SplitHostPort(strings.TrimPrefix(fixture.URL, "http://"))
	port, _ := strconv.Atoi(rawPort)
	dataDir, merged := t.TempDir(), t.TempDir()
	config := &ServerConfig{DataDir: dataDir, MaxRetries: 3, GDrive: &GDriveWrapper{WatchDir: merged}}
	s := NewServer(config)
	var current atomic.Pointer[Server]
	current.Store(s)
	var allowPublication atomic.Bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/enum/results/") && !allowPublication.Load() {
			http.Error(w, "fixture transfer outage", 503)
			return
		}
		current.Load().handleEnumAPI(w, r)
	}))
	defer controller.Close()
	input := filepath.Join(t.TempDir(), "targets.txt")
	if err := os.WriteFile(input, []byte(strings.Replace(fixture.URL, "http://", "http://user:password@", 1)+"/a/../?key=secret#private\n"), 0600); err != nil {
		t.Fatal(err)
	}
	jobID := launchFixtureEnum(t, s, input, port)
	task := waitEnumTask(t, s, jobID)
	s.mu.Lock()
	task.Status = TaskStatusAssigned
	task.AssignedTo = "fixture-worker"
	if err := s.persistEnumJobLocked(jobID); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	worker := NewWorkerAgent(&WorkerConfig{WorkerID: "fixture-worker", DataDir: t.TempDir(), MaxTasks: 2})
	defer worker.Stop()
	worker.serverHTTPAddr = controller.URL
	result, err := worker.executeEnumTask(task)
	if err != nil || result.ExitCode != 0 || result.PublicationStatus != "pending" {
		t.Fatalf("%+v %v", result, err)
	}
	if evidenceRequests.Load() != 6 {
		t.Fatalf("unexpected evidence requests %d", evidenceRequests.Load())
	}
	message, _ := NewMessage(MsgTypeTaskResult, TaskResultPayload{TaskID: task.ID, WorkerID: "fixture-worker", Result: result})
	s.handleTaskResult(message)
	if s.jobs[jobID].Status != "completed" || s.tasks[task.ID].EnumPublication != "pending" {
		t.Fatal("scan completion waits on a failed transfer")
	}
	outboxes, _ := filepath.Glob(filepath.Join(worker.enumRoot(), "outbox", "*.json"))
	if len(outboxes) != 1 {
		t.Fatal(outboxes)
	}
	var pending enumOutbox
	outbox, _ := os.ReadFile(outboxes[0])
	if json.Unmarshal(outbox, &pending) != nil {
		t.Fatal(string(outbox))
	}
	// Restart the controller before retrying. Its recovered task must keep the
	// original artifact, worker binding, publication token, and terminal state.
	recovered := NewServer(config)
	if recovered.enumRecoveryErr != nil {
		t.Fatal(recovered.enumRecoveryErr)
	}
	current.Store(recovered)
	allowPublication.Store(true)
	worker.retryEnumPublications(worker.ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(outboxes[0]); os.IsNotExist(err) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(outboxes[0]); !os.IsNotExist(err) {
		t.Fatal("accepted outbox not cleared")
	}
	if evidenceRequests.Load() != 6 {
		t.Fatal("transfer retry reran the scanner")
	}
	internal := filepath.Join(dataDir, "enum-jobs", jobID, "publications", pending.Basename)
	if _, err := os.Stat(filepath.Join(merged, pending.Basename)); !os.IsNotExist(err) {
		t.Fatal("internal JSONL leaked into merged exports")
	}
	data, err := os.ReadFile(internal)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-body-marker")) || bytes.Contains(data, []byte("password")) || bytes.Contains(data, []byte("key=")) {
		t.Fatalf("private data in result: %s", data)
	}
	record, err := version.DecodeResultLine(bytes.TrimSuffix(data, []byte{'\n'}))
	if err != nil || record.Observation.Version == nil || *record.Observation.Version != "6.4.0" {
		t.Fatalf("%+v %v", record, err)
	}
	if recovered.tasks[task.ID].EnumPublication != "accepted" {
		t.Fatal("publication status not updated")
	}
	if err := worker.deliverEnumPublication(worker.ctx, pending); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(internal)
	if !bytes.Equal(data, again) {
		t.Fatal("duplicate delivery changed results")
	}
	// The internal receipt remains idempotent after a retained artifact is archived.
	if err := os.Remove(internal); err != nil {
		t.Fatal(err)
	}
	if err := worker.deliverEnumPublication(worker.ctx, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(internal); !os.IsNotExist(err) {
		t.Fatal("duplicate recreated uploaded results")
	}
	record.Observation.EvidenceCandidates[0].RetrievedAt = record.Observation.EvidenceCandidates[0].RetrievedAt.Add(time.Second)
	observation, err := version.ResolveEvidence(record.Observation.Component, record.Observation.EvidenceCandidates)
	if err != nil {
		t.Fatal(err)
	}
	record.Observation = observation
	changed, _ := json.Marshal(record)
	changed = append(changed, '\n')
	sum := sha256.Sum256(changed)
	req, _ := http.NewRequest("POST", controller.URL+"/api/enum/results/"+task.ID, bytes.NewReader(changed))
	req.Header.Set("X-Worker-ID", pending.WorkerID)
	req.Header.Set("X-Enum-Job-Token", pending.Token)
	req.Header.Set("X-Enum-Filename", pending.Basename)
	req.Header.Set("X-Enum-SHA256", hex.EncodeToString(sum[:]))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 409 || recovered.tasks[task.ID].Status != TaskStatusFailed || recovered.jobs[jobID].Status != "failed" {
		t.Fatal("different bytes reused a publication filename")
	}
}

func TestEnumChunkingAndInterruptedShardRecovery(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	data := strings.Repeat("http://127.0.0.1/\n", 2001)
	os.WriteFile(input, []byte(data), 0600)
	chunks, err := splitEnumInput(input, filepath.Join(dir, "chunks"), 3)
	if err != nil || len(chunks) != 3 {
		t.Fatalf("%v %v", chunks, err)
	}
	var combined bytes.Buffer
	for _, chunk := range chunks {
		body, err := os.ReadFile(chunk)
		if err != nil || bytes.Count(body, []byte{'\n'}) > 1000 {
			t.Fatal("chunk limit")
		}
		combined.Write(body)
	}
	if combined.String() != data {
		t.Fatal("lost or split a line")
	}
	s := NewServer(&ServerConfig{DataDir: t.TempDir()})
	jobID := launchFixtureEnum(t, s, input, 80)
	task := waitEnumTask(t, s, jobID)
	s.mu.Lock()
	task.Status = TaskStatusRunning
	task.AssignedTo = "lost-worker"
	if err := s.persistEnumJobLocked(jobID); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	recovered := NewServer(s.config)
	if recovered.enumRecoveryErr != nil {
		t.Fatal(recovered.enumRecoveryErr)
	}
	if recovered.tasks[task.ID].Status != TaskStatusFailed || !strings.Contains(recovered.tasks[task.ID].Error, "interrupted") {
		t.Fatal("started shard was rerun after restart")
	}
	for _, id := range recovered.jobTasks[jobID] {
		if id != task.ID && recovered.tasks[id].Status != TaskStatusPending {
			t.Fatal("unstarted shard lost")
		}
	}
}
