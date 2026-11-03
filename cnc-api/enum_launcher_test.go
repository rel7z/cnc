package cnc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signedEnumRequest(t *testing.T, payload []byte) *http.Request {
	t.Helper()
	secret := strings.Repeat("audit-fixture-", 4)
	t.Setenv("CNC_DASHBOARD_AUDIT_SECRET", secret)
	req := httptest.NewRequest(http.MethodPost, "/api/tools/launch", bytes.NewReader(payload))
	stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	req.Header.Set("X-CNC-Actor", "fixture-owner")
	req.Header.Set("X-CNC-Audit-Time", stamp)
	req.Header.Set("X-CNC-Audit-Signature", dashboardAuditSignature(secret, "fixture-owner", stamp, payload))
	return req
}
func fixtureAuthorization() map[string]interface{} {
	return map[string]interface{}{"acknowledged": true, "scope": map[string]interface{}{"cidrs": []string{"127.0.0.0/8"}, "hosts": []string{}, "schemes": []string{"http"}, "ports": []int{80}}}
}

func TestEnumLauncherWordlistAndStreaming(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	input := filepath.Join(dir, "targets.txt")
	if err := os.WriteFile(input, []byte("http://127.0.0.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(&ServerConfig{DataDir: dir, GDrive: &GDriveWrapper{WatchDir: dir}})
	payload, _ := json.Marshal(map[string]interface{}{"tool_id": "enum", "input_file": input, "output_file": "x.txt", "joomla_exts": "com_gridbox\nlib_astroid\nplugin:system:astroid\n", "timeout_seconds": -1, "target_authorization": fixtureAuthorization()})
	w := httptest.NewRecorder()
	s.handleToolsLaunchAPI(w, signedEnumRequest(t, payload))
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if _, ok := response["command"]; ok {
		t.Fatal("enum exposes a shell command")
	}
	id := response["job_id"].(string)
	var taskID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		ids := s.jobTasks[id]
		if len(ids) > 0 {
			taskID = ids[0]
		}
		s.mu.RUnlock()
		if taskID != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if taskID == "" {
		t.Fatal("no task prepared")
	}
	s.mu.RLock()
	timeout := s.jobs[id].TimeoutSeconds
	s.mu.RUnlock()
	if timeout != NoTimeout {
		t.Fatal("overall timeout was not preserved")
	}
	s.mu.RLock()
	task := s.tasks[taskID]
	job := s.jobs[id]
	if task.Type != "enum" || job.Enum.Actor != "fixture-owner" || task.Payload["enum_joomla_exts_url"] != "/api/enum/jobs/"+id+"/joomla_exts.txt" {
		t.Fatalf("unexpected task: %+v", task)
	}
	s.mu.RUnlock()
	for _, p := range []TaskStreamPayload{
		{TaskID: taskID, Chunk: "startup message\n[FILE:com_gridbox.txt] confirmed\n"},
		{TaskID: taskID, Chunk: "progress\n[FILE:lib_astroid.txt] ver", IsStderr: true},
		{TaskID: taskID, Chunk: "[FILE:com_gridbox.txt] second\n"},
		{TaskID: taskID, Chunk: "sion\n", IsStderr: true},
	} {
		m, _ := NewMessage(MsgTypeTaskStream, p)
		s.handleTaskStream(m)
	}
	for name, want := range map[string]string{"com_gridbox.txt": "confirmed\nsecond\n", "lib_astroid.txt": "version\n"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("diagnostics polluted results")
	}
	for i := 0; i < 100; i++ {
		m, _ := NewMessage(MsgTypeTaskStream, TaskStreamPayload{TaskID: taskID, Chunk: "\r[*] CMS Scan: progress", IsStderr: true})
		s.handleTaskStream(m)
	}
	if len(s.taskBuffers[taskID+"\x00stderr"]) > 100 {
		t.Fatal("carriage-return progress accumulated without bound")
	}
}

func TestEnumLauncherMissingInput(t *testing.T) {
	s := NewServer(&ServerConfig{DataDir: t.TempDir()})
	payload, _ := json.Marshal(map[string]interface{}{"tool_id": "enum", "input_file": filepath.Join(t.TempDir(), "missing.txt"), "target_authorization": fixtureAuthorization()})
	w := httptest.NewRecorder()
	s.handleToolsLaunchAPI(w, signedEnumRequest(t, payload))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "cannot read source file") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestEnumAuditRejectsTamperingAndExpiredSignatures(t *testing.T) {
	body := []byte(`{"tool_id":"enum"}`)
	if actor, err := dashboardActor(signedEnumRequest(t, body), body); err != nil || actor != "fixture-owner" {
		t.Fatalf("valid signature: %q %v", actor, err)
	}
	for _, kind := range []string{"actor", "signature", "expired", "body", "missing"} {
		t.Run(kind, func(t *testing.T) {
			req := signedEnumRequest(t, body)
			verified := body
			switch kind {
			case "actor":
				req.Header.Set("X-CNC-Actor", "forged-owner")
			case "signature":
				req.Header.Set("X-CNC-Audit-Signature", strings.Repeat("0", 64))
			case "expired":
				stamp := strconv.FormatInt(time.Now().Add(-3*time.Minute).UnixMilli(), 10)
				req.Header.Set("X-CNC-Audit-Time", stamp)
				req.Header.Set("X-CNC-Audit-Signature", dashboardAuditSignature(os.Getenv("CNC_DASHBOARD_AUDIT_SECRET"), "fixture-owner", stamp, body))
			case "body":
				verified = []byte(`{"tool_id":"enum","options":{"threads":256}}`)
			case "missing":
				req.Header.Del("X-CNC-Audit-Signature")
			}
			if _, err := dashboardActor(req, verified); err == nil {
				t.Fatal("untrusted audit identity accepted")
			}
		})
	}
}

func TestEnumLauncherNoTargetScopeAndSkipsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "targets.txt")
	contents := "# comment\nhttp://example.invalid/\nhttp://example.invalid/icon.svg\nsvg\nnot a URL\nhttps://127.0.0.1:18443/owned\n"
	if err := os.WriteFile(input, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(&ServerConfig{DataDir: dir})
	payload := map[string]interface{}{
		"tool_id": "enum", "input_file": input,
		"target_authorization": map[string]interface{}{"scope": map[string]interface{}{"hosts": []string{"other.example"}, "schemes": []string{"http"}, "ports": []int{80}}},
	}
	data, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	s.handleToolsLaunchAPI(w, signedEnumRequest(t, data))
	if w.Code != http.StatusCreated {
		t.Fatalf("one invalid line or legacy scope rejected the scan: %d %s", w.Code, w.Body.String())
	}
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["skipped_inputs"] != float64(1) {
		t.Fatalf("expected one skipped invalid URL: %v", response)
	}
	jobID := response["job_id"].(string)
	waitEnumTask(t, s, jobID)
	s.mu.RLock()
	job := s.jobs[jobID]
	s.mu.RUnlock()
	if !job.Enum.TargetScope.Unrestricted() || !job.Enum.AcknowledgedAt.IsZero() {
		t.Fatal("legacy target scope still affects new scans")
	}
	snapshot, err := os.ReadFile(job.InputFile)
	if err != nil || !strings.Contains(string(snapshot), "http://example.invalid:80/icon.svg") || !strings.Contains(string(snapshot), "\nsvg/\n") || !strings.Contains(string(snapshot), "https://127.0.0.1:18443/owned") || strings.Contains(string(snapshot), "not a URL") {
		t.Fatalf("unexpected snapshot: %q %v", snapshot, err)
	}
}
