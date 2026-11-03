package cnc

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReverseIPProvidersUseSpreadAndSplitInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, id := range []string{"reverseip-domain", "reverseip-thc", "reverseip-dnsrift"} {
		tool := ToolsRegistry[id]
		if tool.Scope != "worker" || tool.DefaultMode != JobModeSpread {
			t.Fatalf("%s is not load balanced", id)
		}
	}
	s := processorTestServer(t)
	s.config.DataDir = t.TempDir()
	for _, id := range []string{"one", "two"} {
		s.workers[id] = &Worker{ID: id, Status: WorkerStatusOnline, MaxTasks: 2, Resources: memoryFixtureSnapshot(), SendCh: make(chan *Message, 4)}
	}
	p := filepath.Join(t.TempDir(), "ips.txt")
	os.WriteFile(p, []byte("8.8.8.8\n1.1.1.1\n"), 0600)
	body, _ := json.Marshal(map[string]any{"tool_id": "reverseip-dnsrift", "input_file": p, "output_file": "dnsrift.txt"})
	recorder := httptest.NewRecorder()
	s.handleToolsLaunchAPI(recorder, httptest.NewRequest("POST", "/api/tools/launch", bytes.NewReader(body)))
	if recorder.Code != 201 {
		t.Fatal(recorder.Body.String())
	}
	var response struct {
		JobID   string `json:"job_id"`
		Mode    string `json:"mode"`
		Command string `json:"command"`
	}
	json.Unmarshal(recorder.Body.Bytes(), &response)
	if response.Mode != "spread" || !strings.Contains(response.Command, "reverseip-dnsrift") || !strings.Contains(response.Command, "{input}") {
		t.Fatalf("incorrect launch: %+v", response)
	}
	seen := map[string]bool{}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.RLock()
		ready := s.jobs[response.JobID].TotalTasks == 2 && len(s.tasks) == 2
		s.mu.RUnlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("spread tasks were not created")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.mu.RLock()
	for _, task := range s.tasks {
		if task.JobID == response.JobID {
			chunk, _ := task.Payload["chunk_path"].(string)
			data, err := os.ReadFile(chunk)
			if err != nil {
				t.Fatal(err)
			}
			seen[strings.TrimSpace(string(data))] = true
		}
	}
	s.mu.RUnlock()
	if !seen["8.8.8.8"] || !seen["1.1.1.1"] {
		t.Fatal("IPs were not split into distinct worker chunks")
	}
}
