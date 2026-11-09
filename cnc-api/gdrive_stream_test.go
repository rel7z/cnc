package cnc

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type driveTestTransport func(*http.Request) (*http.Response, error)

func (f driveTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDriveSyncPreservesConcurrentResults(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "update"}[existing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "results.txt")
			if err := os.WriteFile(path, []byte("first\nfirst\n"), 0600); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"id":"one"}`
				if r.Method == "GET" && r.URL.Query().Get("q") != "" {
					// A worker appends after syncFile took its snapshot.
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						return nil, err
					}
					_, err = f.WriteString("new\n")
					f.Close()
					if err != nil {
						return nil, err
					}
					body = `{"files":[]}`
					if existing {
						body = `{"files":[{"id":"one"}]}`
					}
				} else if r.Method == "GET" {
					body = "old\n"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			service, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
			if err != nil {
				t.Fatal(err)
			}
			u := &GDriveUploader{config: &GDriveConfig{}, service: service, ctx: context.Background(), uploaded: make(map[string]bool)}
			if err := u.syncFile(path, "results.txt", "obsolete"); err != nil {
				t.Fatal(err)
			}
			u.removeUploadedSnapshot(path, "results.txt")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "first\nfirst\nnew\n" {
				t.Fatalf("lost live results: %q", data)
			}
		})
	}
}

func TestDriveNotificationCoalesces(t *testing.T) {
	u := &GDriveUploader{dirty: make(chan struct{}, 1)}
	for i := 0; i < 100; i++ {
		u.NotifyResults()
	}
	if len(u.dirty) != 1 {
		t.Fatal("expected one pending sync")
	}
}

func TestTaskStreamRoutesBatchWithoutStderr(t *testing.T) {
	dir := t.TempDir()
	u := &GDriveUploader{dirty: make(chan struct{}, 1)}
	s := &Server{
		config:         &ServerConfig{GDrive: &GDriveWrapper{WatchDir: dir}},
		tasks:          map[string]*Task{"task": {JobID: "job"}},
		jobs:           map[string]*Job{"job": {OutputFile: "results.txt"}},
		gdriveUploader: u,
	}
	for _, payload := range []TaskStreamPayload{
		{TaskID: "task", Chunk: "[FILE:enum-admin.txt] first\n[FILE:enum-admin.txt] sec"},
		{TaskID: "task", Chunk: "diagnostic\n", IsStderr: true},
		{TaskID: "task", Chunk: "ond\n[FILE:enum-admin.txt] third\n"},
	} {
		s.applyTaskStream(payload)
	}
	data, err := os.ReadFile(filepath.Join(dir, "enum-admin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first\nsecond\nthird\n" {
		t.Fatalf("unexpected output %q", data)
	}
	if len(u.dirty) != 1 {
		t.Fatal("live output did not notify Drive uploader")
	}
	if _, err := os.Stat(filepath.Join(dir, "results.txt")); !os.IsNotExist(err) {
		t.Fatal("stderr entered result file")
	}
}
