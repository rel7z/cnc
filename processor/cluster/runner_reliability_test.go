package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDependencyCopyRepeat(t *testing.T) {
	source := filepath.Join(t.TempDir(), "node_modules")
	target := filepath.Join(t.TempDir(), "node_modules")
	os.MkdirAll(filepath.Join(source, ".bin"), 0700)
	os.MkdirAll(filepath.Join(source, "fixture"), 0700)
	os.WriteFile(filepath.Join(source, "fixture", "cli.js"), []byte("#!/usr/bin/env node\n"), 0700)
	source, _ = filepath.EvalSymlinks(source)
	os.Symlink("../fixture/cli.js", filepath.Join(source, ".bin", "fixture"))
	if err := copyDependencies(source, target); err != nil {
		t.Fatal(err)
	}
	if err := copyDependencies(source, target); err != nil {
		t.Fatalf("second workspace execution cannot prepare dependencies: %v", err)
	}
	if err := copyDependencies(filepath.Join(t.TempDir(), "absent"), target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("removed dependencies remained available to a new version")
	}
}

func TestQuarantinedCompletionReleasesWorkerSlotWithoutReupload(t *testing.T) {
	runner := &Runner{Root: t.TempDir()}
	dir := filepath.Join(runner.Root, "attempts", "quarantined")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"exit_code":0}`)
	if err := os.WriteFile(filepath.Join(dir, "completion.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "renew":
			w.Write([]byte(`{"seconds":30,"phase":"failed"}`))
		case "complete":
			w.Write([]byte(`{"acknowledged":true}`))
		default:
			t.Errorf("terminal result tried to transfer %s", r.URL.Path)
			w.WriteHeader(409)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runner.Run(ctx, Assignment{AttemptID: "quarantined", RunID: "quarantined", BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "acknowledged")); err != nil {
		t.Fatal(err)
	}
}
func TestDependencyExecutableMode(t *testing.T) {
	source := filepath.Join(t.TempDir(), "node_modules")
	target := filepath.Join(t.TempDir(), "node_modules")
	os.MkdirAll(source, 0700)
	os.WriteFile(filepath.Join(source, "fixture-cli"), []byte("#!/bin/sh\nexit 0\n"), 0700)
	if err := copyDependencies(source, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(target, "fixture-cli"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Fatalf("executable dependency lost its execute permissions: %v", info.Mode())
	}
}

func TestFinalSummarySurvivesLogLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := &cappedWriter{file: file, remaining: MaxLogBytes}
	writer.Write([]byte(strings.Repeat("x", MaxLogBytes)))
	writer.Write([]byte("\nprocessed = 2/2\n"))
	file.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "processed = 2/2") {
		t.Fatal("successful tool's final summary is silently discarded after 4 MiB of logs")
	}
}
