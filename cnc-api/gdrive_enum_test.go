package cnc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func TestDriveSkipsInternalEnumArtifacts(t *testing.T) {
	directory := t.TempDir()
	name := "enum-versions-job-shard.jsonl"
	if err := os.WriteFile(filepath.Join(directory, name), []byte("internal evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("internal JSONL must not access Drive")
	})}
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	u := &GDriveUploader{config: &GDriveConfig{WatchDir: directory}, service: service, ctx: context.Background(), uploaded: map[string]bool{}, knownFiles: map[string]bool{name: true}}
	u.scanAndUpload()
	if requests != 0 {
		t.Fatalf("internal JSONL triggered %d Drive requests", requests)
	}
	if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
		t.Fatal("internal artifact was removed", err)
	}
}

func TestDriveEnumUploadsExactLargeRecordBytesAndRetriesOutage(t *testing.T) {
	name := "enum-versions-job-shard.jsonl"
	data := []byte(`{"schema_version":1,"matched_rules":[{"id":"large","name":"` + strings.Repeat("x", 128*1024) + `"}]}` + " \r\n")
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var uploaded []byte
	outage := true
	created := false
	requests := 0
	client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"id":"owned-result"}`
		if r.Method == http.MethodGet && r.URL.Query().Get("q") != "" {
			body = `{"files":[]}`
			if created {
				body = `{"files":[{"id":"owned-result"}]}`
			}
		} else if r.Method == http.MethodGet {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-ndjson"}}, Body: io.NopCloser(bytes.NewReader(uploaded)), Request: r}, nil
		} else if r.Method == http.MethodPost {
			requests++
			if outage {
				return nil, fmt.Errorf("owned Drive outage")
			}
			mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
				t.Fatalf("unexpected media upload %s %v", mediaType, err)
			}
			reader := multipart.NewReader(r.Body, params["boundary"])
			metadata, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, metadata)
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			uploaded, err = io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			created = true
		} else {
			t.Fatalf("immutable result was rewritten: %s", r.Method)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	u := &GDriveUploader{config: &GDriveConfig{}, service: service, ctx: context.Background(), uploaded: map[string]bool{}}
	if err := u.syncFile(path, name, "obsolete"); err == nil {
		t.Fatal("outage was acknowledged")
	}
	if len(u.uploaded) != 0 {
		t.Fatal("outage marked uploaded")
	}
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(retained, data) {
		t.Fatal("outage lost original bytes")
	}
	outage = false
	if err := u.syncFile(path, name, "obsolete"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uploaded, data) {
		t.Fatal("large JSONL record was truncated or normalized")
	}
	if !u.uploaded[name+":"+sha256Bytes(data)] {
		t.Fatal("exact source digest was not recorded")
	}
	if err := u.syncFile(path, name, "obsolete"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("same bytes were uploaded again", requests)
	}
}

func TestDriveEnumRejectsConflictingRemoteBytesWithoutMutation(t *testing.T) {
	name := "enum-versions-job-shard.jsonl"
	path := filepath.Join(t.TempDir(), name)
	os.WriteFile(path, []byte("original\n"), 0600)
	mutations := 0
	client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
		body := "different\n"
		if r.URL.Query().Get("q") != "" {
			body = `{"files":[{"id":"conflict"},{"id":"another"}]}`
		}
		if r.Method != http.MethodGet {
			mutations++
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	u := &GDriveUploader{config: &GDriveConfig{}, service: service, ctx: context.Background(), uploaded: map[string]bool{}}
	if err := u.syncFile(path, name, "obsolete"); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatal("remote collision accepted", err)
	}
	if mutations != 0 || len(u.uploaded) != 0 {
		t.Fatal("collision mutated Drive or acknowledged publication")
	}
	if !isImmutableEnumResult(name) || isImmutableEnumResult("results.jsonl") || isImmutableEnumResult("../"+name) {
		t.Fatal("immutable publication filename matching")
	}
}
