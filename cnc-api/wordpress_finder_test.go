package cnc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cnc-enum/version"
	"github.com/user/cnc-processor/cluster"
)

type finderTestTransport func(*http.Request) (*http.Response, error)

func (transport finderTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return transport(r)
}

func TestWordPressFinderPublicCandidates(t *testing.T) {
	for _, invalid := range []string{"localhost", "http://127.0.0.1/", "https://[::1]/", "http://user:pass@public.example/", "https://public.example:80/", "file:///tmp/x"} {
		if _, host := version.WordPressHomepage(invalid); host != "" {
			t.Errorf("accepted %s", invalid)
		}
	}
	for _, raw := range []string{"127.0.0.1", "192.168.4.1", "169.254.169.254", "100.100.100.200", "::ffff:10.0.0.1", "fc00::1", "2001:db8::1", "2002:7f00:1::1"} {
		if version.PublicHomepageIP(net.ParseIP(raw)) {
			t.Errorf("allowed nonpublic IP %s", raw)
		}
	}
	if !version.PublicHomepageIP(net.ParseIP("1.1.1.1")) || !version.PublicHomepageIP(net.ParseIP("2606:4700:4700::1111")) {
		t.Fatal("public DNS addresses were refused")
	}
	client, cleanup, err := version.NewWordPressHomepageClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	initial := httptest.NewRequest(http.MethodGet, "https://public.example/", nil)
	for _, destination := range []string{"https://169.254.169.254/", "https://other.example/", "https://public.example/wp-login.php", "https://public.example:8443/"} {
		if err := client.CheckRedirect(httptest.NewRequest(http.MethodGet, destination, nil), []*http.Request{initial}); err != http.ErrUseLastResponse {
			t.Errorf("followed blocked redirect %s", destination)
		}
	}
}

func TestWordPressFinderPrioritizesVerifiedRecentResults(t *testing.T) {
	failRecent := false
	apify := &http.Client{Transport: finderTestTransport(func(req *http.Request) (*http.Response, error) {
		var input struct {
			Queries          string
			MaxPagesPerQuery int
		}
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			return nil, err
		}
		if req.Header.Get("Authorization") != "Bearer fixture-token" || input.MaxPagesPerQuery != 2 || strings.Contains(input.Queries, "\n") {
			t.Error("unexpected Apify request")
		}
		response := `[{"organicResults":[{"url":"https://older.example/about"},{"url":"https://notwp.example/"}]}]`
		if strings.Contains(input.Queries, "after:2026-10-09") {
			if failRecent {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			response = `[{"organicResults":[{"url":"https://www.new.example/post"}]}]`
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	hits, err := finderSearch(context.Background(), apify, "https://apify.example/", "fixture-token",
		wordpressFinderInput{Keyword: "studio", Recent: true, Pages: 2}, time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: finderTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/" || req.Header.Get("Authorization") != "" {
			t.Error("verification should fetch only the public homepage")
		}
		body := `<link rel="stylesheet" href="/wp-content/themes/site/style.css">`
		if req.URL.Host == "notwp.example" {
			body = "<html>Other site</html>"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	var result wordpressFinderResult
	verifyFinderCandidates(context.Background(), client, finderUnique("new.example\nhttps://www.new.example/\n", hits), &result)
	if got := strings.Join(result.URLs, "\n"); got != "https://new.example/\nhttps://older.example/" || result.Checked != 3 || result.Recent != 1 {
		t.Fatalf("unexpected results: %+v", result)
	}

	failRecent = true
	hits, err = finderSearch(context.Background(), apify, "https://apify.example/", "fixture-token",
		wordpressFinderInput{Keyword: "studio", Recent: true, Pages: 2}, time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC))
	if err == nil || len(hits) != 2 {
		t.Fatalf("broad results were lost when the recent query failed: %v %v", hits, err)
	}
}

func TestWordPressFinderLaunchRequiresSignedDashboard(t *testing.T) {
	t.Setenv("APIFY_TOKEN", "")
	s := &Server{}
	body := []byte(`{"tool_id":"wordpress-finder","keyword":"studio"}`)
	recorder := httptest.NewRecorder()
	s.handleToolsLaunchAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/tools/launch", strings.NewReader(string(body))))
	if recorder.Code != http.StatusForbidden {
		t.Fatal(recorder.Code)
	}
	recorder = httptest.NewRecorder()
	s.handleToolsLaunchAPI(recorder, signedEnumRequest(t, body))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "APIFY_TOKEN") {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestWordPressBulkLaunchPersistsHomepageOnlyJob(t *testing.T) {
	s := NewServer(&ServerConfig{DataDir: t.TempDir()})
	resources := cluster.ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 8192, AvailableMemoryMB: 7000, Cgroups: true}
	legacy := &Worker{ID: "legacy-1", Status: WorkerStatusOnline, SendCh: make(chan *Message, 1), MaxTasks: 2, Resources: resources}
	s.workers[legacy.ID] = legacy
	noReceipts := &Worker{ID: "bulk-no-receipts", Status: WorkerStatusOnline, SendCh: make(chan *Message, 1), MaxTasks: 2, Resources: resources,
		WordPressBulk: true, EnumCheckpoint: true}
	s.workers[noReceipts.ID] = noReceipts
	bulk := &Worker{ID: "bulk", Status: WorkerStatusOnline, SendCh: make(chan *Message, 1), MaxTasks: 2, Resources: resources,
		WordPressBulk: true, EnumCheckpoint: true, EnumStreamReceipts: true}
	s.workers[bulk.ID] = bulk
	input := filepath.Join(t.TempDir(), "bulk.txt")
	if err := os.WriteFile(input, []byte("example.org/path\nexample.com\nhttp://127.0.0.1/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"tool_id": "wordpress-bulk", "input_file": input})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	s.handleToolsLaunchAPI(recorder, signedEnumRequest(t, body))
	var response struct {
		JobID   string `json:"job_id"`
		Skipped int    `json:"skipped_inputs"`
	}
	if recorder.Code != http.StatusCreated || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response.Skipped != 1 {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	task := waitEnumTask(t, s, response.JobID)
	s.mu.RLock()
	job := *s.jobs[response.JobID]
	s.mu.RUnlock()
	if job.Enum == nil || !job.Enum.WordPressHomepages || task.Payload["wordpress_bulk"] != true || job.OutputFile != "wordpress-"+job.ID+".txt" {
		t.Fatal("bulk job was not configured for the homepage verifier")
	}
	if job.Workers != 1 || job.TotalTasks != 1 || job.Enum.Threads != 64 || job.Enum.RequestTimeoutMS != 5000 {
		t.Fatalf("unexpected bulk capacity: workers=%d tasks=%d threads=%d timeout=%d",
			job.Workers, job.TotalTasks, job.Enum.Threads, job.Enum.RequestTimeoutMS)
	}
	path := filepath.Join(s.config.DataDir, "enum-jobs", job.ID, job.Enum.ArtifactFilename)
	artifact, err := version.LoadArtifact(path, job.Enum.ArtifactSHA256, job.ID)
	if err != nil || !artifact.WordPressHomepages || len(artifact.Rules) != 0 {
		t.Fatalf("incorrect bulk scan artifact: %+v %v", artifact, err)
	}
	s.dispatchTask(task)
	select {
	case message := <-bulk.SendCh:
		var assignment AssignTaskPayload
		if err := message.UnmarshalPayload(&assignment); err != nil || assignment.Task.Payload["enum_stream_receipts"] != true {
			t.Fatalf("bulk worker did not receive a reliable assignment: %v", err)
		}
	default:
		t.Fatal("bulk worker did not receive the task")
	}
	if len(legacy.SendCh) != 0 || len(noReceipts.SendCh) != 0 {
		t.Fatal("worker without reliable bulk support received a bulk assignment")
	}
	result := TaskStreamPayload{TaskID: task.ID, WorkerID: bulk.ID, ReceiptID: "batch-1",
		Chunk: "[FILE:" + job.OutputFile + "] https://example.org/\n[FILE:other.txt] unrelated\n"}
	if !s.applyTaskStream(result) || !s.applyTaskStream(result) {
		t.Fatal("worker result or replay was not acknowledged")
	}
	merged := filepath.Join(s.config.DataDir, "merged")
	content, err := os.ReadFile(filepath.Join(merged, job.OutputFile))
	if err != nil || string(content) != "https://example.org/\n" {
		t.Fatalf("unexpected URL export: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(merged, "other.txt")); !os.IsNotExist(err) {
		t.Fatal("unrelated output leaked into Merged")
	}
	message, _ := NewMessage(MsgTypeTaskResult, TaskResultPayload{WorkerID: bulk.ID, TaskID: task.ID, Result: &TaskResult{ExitCode: 2}})
	s.handleTaskResult(message)
	if s.jobs[job.ID].Status != "completed_with_errors" {
		t.Fatal("inconclusive homepage checks did not preserve the partial result")
	}
}

func TestWordPressFreshLaunchIsIdempotentAfterRestart(t *testing.T) {
	config := &ServerConfig{DataDir: t.TempDir()}
	input := filepath.Join(t.TempDir(), "fresh.txt")
	if err := os.WriteFile(input, []byte("example.org\n"), 0600); err != nil {
		t.Fatal(err)
	}
	batch := "000102030405060708090a0b0c0d0e0f"
	body, err := json.Marshal(map[string]string{"tool_id": "wordpress-bulk", "input_file": input, "fresh_batch_id": batch})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(config)
	r := httptest.NewRecorder()
	s.handleToolsLaunchAPI(r, signedEnumRequest(t, body))
	if r.Code != http.StatusCreated {
		t.Fatal(r.Body.String())
	}
	id := version.WordPressFreshJobPrefix + batch
	waitEnumTask(t, s, id)
	recovered := NewServer(config)
	if recovered.enumRecoveryErr != nil {
		t.Fatal(recovered.enumRecoveryErr)
	}
	r = httptest.NewRecorder()
	recovered.handleToolsLaunchAPI(r, signedEnumRequest(t, body))
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), id) || len(recovered.jobs) != 1 {
		t.Fatalf("retry created another job: %s", r.Body.String())
	}
	if got := recovered.jobs[id].OutputFile; got != version.WordPressOutputName(id) {
		t.Fatalf("incorrect Merged output name %q", got)
	}
}

func TestWordPressFreshStreamDeduplicatesReplay(t *testing.T) {
	existing := "https://example.org/\n"
	incoming := "http://www.example.org/\nhttps://other.example/\nhttps://www.other.example/\n"
	if got := freshStreamLines(existing, incoming); got != "https://other.example/\n" {
		t.Fatalf("incorrect replay output %q", got)
	}
}

func TestWordPressFreshDispatchYieldsToWaitingCNCJob(t *testing.T) {
	s := NewServer(&ServerConfig{DataDir: t.TempDir()})
	fresh := &Task{ID: "fresh", JobID: version.WordPressFreshJobPrefix + "batch", Status: TaskStatusPending,
		Type: "enum", Payload: map[string]interface{}{"wordpress_bulk": true}}
	ordinary := &Task{ID: "ordinary", JobID: "other", Status: TaskStatusPending, Type: "shell"}
	s.tasks[fresh.ID] = fresh
	s.tasks[ordinary.ID] = ordinary
	s.dispatchTask(fresh)
	if fresh.Status != TaskStatusPending || fresh.QueueReason != "waiting for queued CNC work" {
		t.Fatalf("fresh job did not yield: %+v", fresh)
	}
}
