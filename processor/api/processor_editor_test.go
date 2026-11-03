package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessorEditorTemplatesUploadsAndDownloads(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "db.sqlite")
	db, err := store.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	db.InitSchema()
	token := strings.Repeat("s", 32)
	server := &Server{Store: db, AppDir: root}
	handler := server.Handler(token)
	request := func(method, path string, body any, auth bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	definition := map[string]any{"tool_id": "owned", "incoming": "incoming.txt", "folder": "examples", "manifest": map[string]any{"input": "targets.txt", "scripts": map[string]any{"scanner": map[string]any{"path": "scanner.py", "outputs": []any{map[string]any{"path": "results.txt", "send_to": "checker", "drive": false}}}, "checker": map[string]any{"path": "check.sh", "input": "results.txt", "outputs": []string{"success.txt"}}}}}
	body := map[string]any{"name": "Pipeline", "definition": definition}
	if w := request("POST", "/v1/processor-templates", body, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/v1/processor-templates", body, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved store.ProcessorTemplate
	json.Unmarshal(w.Body.Bytes(), &saved)
	if w := request("POST", "/v1/processor-templates", body, true); w.Code != 409 {
		t.Fatal("duplicate accepted", w.Code)
	}
	body["revision"] = saved.Revision
	body["name"] = "Renamed"
	url := "/v1/processor-templates/" + saved.ID
	if w := request("PUT", url, body, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("PUT", url, body, true); w.Code != 409 {
		t.Fatal("stale overwrite", w.Code)
	}
	db.Close()
	db, err = store.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	db.InitSchema()
	server.Store = db
	if w := request("GET", "/v1/processor-templates", nil, true); w.Code != 200 || !strings.Contains(w.Body.String(), "Renamed") || !strings.Contains(w.Body.String(), `"drive":false`) {
		t.Fatal("persistence", w.Body.String())
	}
	if w := request("DELETE", url, map[string]any{"revision": 2}, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	upload := func(name string) *httptest.ResponseRecorder {
		var buffer bytes.Buffer
		form := multipart.NewWriter(&buffer)
		part, _ := form.CreateFormFile("file:"+name, "scanner.py")
		part.Write([]byte("print('owned fixture')\n"))
		form.Close()
		r := httptest.NewRequest("POST", "/v1/processor-assets", &buffer)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := upload("../escape.py"); w.Code != 400 {
		t.Fatal("upload escape", w.Code)
	}
	w = upload("nested/scanner.py")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var assets struct {
		Folder string `json:"folder"`
	}
	json.Unmarshal(w.Body.Bytes(), &assets)
	if data, err := os.ReadFile(filepath.Join(root, assets.Folder, "nested/scanner.py")); err != nil || !strings.Contains(string(data), "owned fixture") {
		t.Fatal(err)
	}
	ctx := context.Background()
	db.SetEnabled(ctx, true)
	db.RegisterProgram(ctx, "owned", store.ProgramSpec{Version: "v", Limit: 6})
	_, runs, err := db.AcceptBatch(ctx, store.Batch{SourceID: "fixture", BatchID: "one", Filename: "incoming.txt", RecordsJSON: `["owned\n"]`}, []string{"owned"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.AllocateAttempt(ctx, runs[0], "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	db.AuthorizeAttempt(ctx, a.ID)
	if err := db.AcceptPublication(ctx, a, cluster.Publication{Name: "owned__success.txt", Generation: "g", Sequence: 1, Lines: []string{"one\n", "two\n"}}, nil); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/v1/processor-results?name=owned__success.txt", nil, true)
	if w.Code != 200 || w.Body.String() != "one\ntwo\n" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("download", w.Code, w.Body.String())
	}
	if w := request("GET", "/v1/processor-results?name=..%2Fsecret.txt", nil, true); w.Code != 400 {
		t.Fatal("path traversal", w.Code)
	}
	if w := request("GET", "/v1/processor-results", nil, false); w.Code != 401 {
		t.Fatal("unauthenticated results", w.Code)
	}
}
