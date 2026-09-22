package cnc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSListAndReadWriteDelete(t *testing.T) {
	tempDir := t.TempDir()
	server := NewServer(nil)

	// 1. Test Write API (including automatic parent directory creation)
	subDirFile := filepath.Join(tempDir, "subdir", "testfile.txt")
	writePayload, _ := json.Marshal(map[string]string{
		"file":    subDirFile,
		"content": "Hello Production World\nLine 2",
	})

	writeReq := httptest.NewRequest(http.MethodPost, "/api/fs/write", bytes.NewReader(writePayload))
	writeRec := httptest.NewRecorder()
	server.handleFSWriteAPI(writeRec, writeReq)

	if writeRec.Code != http.StatusOK {
		t.Fatalf("handleFSWriteAPI failed with code %d: %s", writeRec.Code, writeRec.Body.String())
	}

	// Verify file actually written to disk
	content, err := os.ReadFile(subDirFile)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if !strings.Contains(string(content), "Hello Production World") {
		t.Fatalf("unexpected file content: %s", string(content))
	}

	// 2. Test Read API
	readReq := httptest.NewRequest(http.MethodGet, "/api/fs/read?file="+subDirFile, nil)
	readRec := httptest.NewRecorder()
	server.handleFSReadAPI(readRec, readReq)

	if readRec.Code != http.StatusOK {
		t.Fatalf("handleFSReadAPI failed with code %d: %s", readRec.Code, readRec.Body.String())
	}
	if !strings.Contains(readRec.Body.String(), "Hello Production World") {
		t.Fatalf("unexpected read content: %s", readRec.Body.String())
	}

	// 3. Test List API
	listReq := httptest.NewRequest(http.MethodGet, "/api/fs/list?dir="+filepath.Join(tempDir, "subdir"), nil)
	listRec := httptest.NewRecorder()
	server.handleFSListAPI(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("handleFSListAPI failed with code %d: %s", listRec.Code, listRec.Body.String())
	}

	var listResult struct {
		CurrentDir string     `json:"current_dir"`
		Files      []FileInfo `json:"files"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResult); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(listResult.Files) != 1 || listResult.Files[0].Name != "testfile.txt" {
		t.Fatalf("expected 1 file named testfile.txt, got %+v", listResult.Files)
	}

	// 4. Test Delete API
	delReq := httptest.NewRequest(http.MethodDelete, "/api/fs/delete?file="+subDirFile, nil)
	delRec := httptest.NewRecorder()
	server.handleFSDeleteAPI(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("handleFSDeleteAPI failed with code %d: %s", delRec.Code, delRec.Body.String())
	}

	// Verify file is deleted
	if _, err := os.Stat(subDirFile); !os.IsNotExist(err) {
		t.Fatalf("file should have been deleted, but stat returned: %v", err)
	}
}

func TestServerExecAPI(t *testing.T) {
	tempDir := t.TempDir()
	server := NewServer(nil)

	// Test basic command execution
	execPayload, _ := json.Marshal(map[string]string{
		"command": "echo 'cnc-prod-test-output'",
		"cwd":     tempDir,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/server/exec", bytes.NewReader(execPayload))
	rec := httptest.NewRecorder()
	server.handleServerExecAPI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleServerExecAPI failed with code %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Cwd      string `json:"cwd"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode exec response: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "cnc-prod-test-output") {
		t.Errorf("expected stdout to contain 'cnc-prod-test-output', got: %s", res.Stdout)
	}
}
