package cnc

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func uploadRequest(t *testing.T, dir, name string, data []byte, extra bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if extra {
		part, err = writer.CreateFormFile("file", "extra.txt")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte("extra"))
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/fs/upload?dir="+url.QueryEscape(dir), bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}
func TestUploadBinaryAndRejectOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := &Server{}
	data := []byte{0, 255, 128, 1, '\n', 0}
	first := httptest.NewRecorder()
	s.handleFSUploadAPI(first, uploadRequest(t, dir, "résumé.bin", data, false))
	if first.Code != 201 {
		t.Fatal(first.Code, first.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, "résumé.bin"))
	if err != nil || !bytes.Equal(content, data) {
		t.Fatalf("content=%v err=%v", content, err)
	}
	again := httptest.NewRecorder()
	s.handleFSUploadAPI(again, uploadRequest(t, dir, "résumé.bin", []byte("replace"), false))
	if again.Code != 409 {
		t.Fatal(again.Code)
	}
	content, _ = os.ReadFile(filepath.Join(dir, "résumé.bin"))
	if !bytes.Equal(content, data) {
		t.Fatal("overwrote existing file")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("temporary files leaked")
	}
}
func TestUploadValidationAndSizeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		extra  bool
		status int
	}{
		{"empty.txt", nil, false, 201}, {"four.bin", []byte("1234"), false, 201},
		{"big.bin", []byte("12345"), false, 413}, {"../escape.txt", nil, false, 400},
		{`..\escape.txt`, nil, false, 400}, {"file.txt", []byte("x"), true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			w := httptest.NewRecorder()
			(&Server{}).uploadFile(w, uploadRequest(t, dir, tc.name, tc.data, tc.extra), 4)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			entries, _ := os.ReadDir(dir)
			want := 0
			if tc.status == 201 {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("unexpected files=%v", entries)
			}
		})
	}
}
func TestUploadConcurrentCollision(t *testing.T) {
	dir := t.TempDir()
	s := &Server{}
	requests := []*http.Request{uploadRequest(t, dir, "same.txt", []byte("one"), false), uploadRequest(t, dir, "same.txt", []byte("two"), false)}
	recorders := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) { defer wg.Done(); s.handleFSUploadAPI(recorders[i], requests[i]) }(i)
	}
	wg.Wait()
	if !((recorders[0].Code == 201 && recorders[1].Code == 409) || (recorders[1].Code == 201 && recorders[0].Code == 409)) {
		t.Fatal(recorders[0].Code, recorders[1].Code)
	}
}
func TestUploadIncompleteAndSymlink(t *testing.T) {
	dir := t.TempDir()
	s := &Server{}
	r := httptest.NewRequest("POST", "/api/fs/upload?dir="+url.QueryEscape(dir), strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"x.txt\"\r\n\r\nunfinished"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	w := httptest.NewRecorder()
	s.handleFSUploadAPI(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("incomplete file exposed")
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	os.WriteFile(target, []byte("keep"), 0600)
	if err := os.Symlink(target, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handleFSUploadAPI(w, uploadRequest(t, dir, "link.txt", []byte("replace"), false))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	content, _ := os.ReadFile(target)
	if string(content) != "keep" {
		t.Fatal("followed symlink")
	}
}
