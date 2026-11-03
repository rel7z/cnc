package cnc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessorProxyAccess(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		feature, remote, path, method string
		status                        int
	}{
		{"", "127.0.0.1:1234", "health", "GET", 404},
		{"1", "198.51.100.2:1234", "health", "GET", 403},
		{"1", "127.0.0.1:1234", "unknown", "GET", 404},
		{"1", "127.0.0.1:1234", "processing-state", "GET", 405},
		{"1", "127.0.0.1:1234", "health", "GET", 503},
		{"1", "127.0.0.1:1234", "checkpoints/reset", "POST", 503},
		{"1", "127.0.0.1:1234", "checkpoints/reset", "GET", 405},
		{"1", "198.51.100.2:1234", "checkpoints/reset", "POST", 403},
		{"1", "127.0.0.1:1234", "program-priorities", "GET", 503},
		{"1", "127.0.0.1:1234", "program-priorities/fixture", "PUT", 503},
		{"1", "198.51.100.2:1234", "program-priorities/fixture", "PUT", 403},
		{"1", "127.0.0.1:1234", "program-priorities/fixture", "GET", 405},
		{"1", "127.0.0.1:1234", "program-priorities/fixture/extra", "PUT", 404},
	} {
		t.Setenv("CNC_PROCESSOR_ENABLED", tc.feature)
		t.Setenv("CNC_PROCESSOR_TOKEN", "")
		req := httptest.NewRequest(tc.method, "/api/processor/"+tc.path, nil)
		req.RemoteAddr = tc.remote
		w := httptest.NewRecorder()
		s.handleProcessorAPI(w, req)
		if w.Code != tc.status {
			t.Fatalf("%+v: got %d", tc, w.Code)
		}
	}
}

func TestProcessorDeletionRequiresDurableAcknowledgment(t *testing.T) {
	t.Setenv("CNC_PROCESSOR_ENABLED", "1")
	t.Setenv("CNC_PROCESSOR_TOKEN", strings.Repeat("a", 32))
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	for _, tc := range []struct {
		code    int
		body    string
		deleted bool
	}{
		{409, "paused", false}, {503, "unavailable", false}, {200, `{"captured":false}`, false}, {200, `{"captured":true}`, true},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "input.txt")
		if err := os.WriteFile(path, []byte("fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		u := &GDriveUploader{uploaded: map[string]bool{}}
		hash, err := u.hashFile(path)
		if err != nil {
			t.Fatal(err)
		}
		u.uploaded["input.txt:"+hash] = true
		http.DefaultTransport = driveTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/capture" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 32) {
				t.Fatal("invalid capture request")
			}
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})
		u.removeUploadedSnapshot(path, "input.txt")
		_, err = os.Stat(path)
		if os.IsNotExist(err) != tc.deleted {
			t.Fatalf("code=%d body=%s deleted=%v", tc.code, tc.body, os.IsNotExist(err))
		}
	}
}

func TestPriorityProxyForwardsRevisionAndCredentials(t *testing.T) {
	t.Setenv("CNC_PROCESSOR_ENABLED", "1")
	t.Setenv("CNC_PROCESSOR_TOKEN", strings.Repeat("s", 32))
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	http.DefaultTransport = driveTestTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "PUT" || r.URL.String() != "http://127.0.0.1:9191/v1/program-priorities/fixture" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || string(body) != `{"rank":5,"revision":1}` {
			t.Fatal("incorrect forwarding")
		}
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"error":"conflict"}`)), Header: make(http.Header)}, nil
	})
	r := httptest.NewRequest("PUT", "/api/processor/program-priorities/fixture", strings.NewReader(`{"rank":5,"revision":1}`))
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	(&Server{}).handleProcessorAPI(w, r)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
