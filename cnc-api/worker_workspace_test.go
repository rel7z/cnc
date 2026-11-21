package cnc

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func workspaceAgent(t *testing.T) (*WorkerAgent, *bytes.Buffer) {
	t.Helper()
	agent := NewWorkerAgent(&WorkerConfig{WorkerID: "fixture-worker", DataDir: t.TempDir()})
	t.Cleanup(agent.cancel)
	var wire bytes.Buffer
	agent.encoder = json.NewEncoder(&wire)
	return agent, &wire
}

func workspaceStreams(t *testing.T, wire *bytes.Buffer) map[string]string {
	t.Helper()
	streams := map[string]string{}
	dec := json.NewDecoder(bytes.NewReader(wire.Bytes()))
	for {
		var message Message
		if err := dec.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		var p TaskStreamPayload
		if err := message.UnmarshalPayload(&p); err != nil {
			t.Fatal(err)
		}
		if !p.IsStderr {
			streams[p.TaskID] += p.Chunk
		}
	}
	return streams
}

func assertShellWorkspacesClean(t *testing.T, agent *WorkerAgent) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(agent.config.DataDir, "shell-runs"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("workspace remains after task: %v %v", entries, err)
	}
}

func TestShellWorkspaceConcurrentInputsAndOutputs(t *testing.T) {
	source := t.TempDir()
	t.Chdir(source)
	t.Setenv("HOME", source)
	for _, name := range []string{"x.txt", "xxz.txt"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("STALE\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.TrimPrefix(r.URL.Path, "/")+"\n")
	}))
	defer server.Close()
	agent, wire := workspaceAgent(t)
	agent.config.SpreadWorkspaceRoot = t.TempDir()
	t.Setenv("SPREAD_TEST_ROOT", agent.config.SpreadWorkspaceRoot)
	agent.serverHTTPAddr = server.URL
	var wg sync.WaitGroup
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			r, err := agent.executeShellTask(&Task{ID: id, Payload: map[string]interface{}{
				"command":      "case \"$HOME\" in \"$SPREAD_TEST_ROOT\"/shell-runs/task-*) ;; *) exit 1 ;; esac; test ! -f xxz.txt && test \"$(cd \"$HOME\" && pwd -P)\" = \"$(pwd -P)\" && cat {input} > xxz.txt && sleep 0.3 && cat xxz.txt",
				"download_url": "/" + id, "dest_name": "x.txt", "output_file": "xxz.txt",
			}})
			if err != nil || r.ExitCode != 0 || r.Stdout != id+"\n" {
				t.Errorf("%s got %+v %v", id, r, err)
			}
		}(id)
	}
	wg.Wait()
	streams := workspaceStreams(t, wire)
	if streams["first"] != "first\n" || streams["second"] != "second\n" {
		t.Fatalf("cross-job or duplicate output: %v", streams)
	}
	for _, name := range []string{"x.txt", "xxz.txt"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil || string(data) != "STALE\n" {
			t.Fatalf("old worker file changed: %s %q %v", name, data, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(agent.config.SpreadWorkspaceRoot, "shell-runs"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("spread workspace remains after task: %v %v", entries, err)
	}
	retained, err := os.ReadFile(filepath.Join(agent.config.SpreadWorkspaceRoot, "x.txt"))
	if err != nil || (string(retained) != "first\n" && string(retained) != "second\n") {
		t.Fatalf("spread input missing after task cleanup: %q %v", retained, err)
	}
}

func TestShellWorkspaceEmptyRunDoesNotReplayOldOutput(t *testing.T) {
	source := t.TempDir()
	t.Chdir(source)
	os.WriteFile("xxz.txt", []byte("STALE\n"), 0600)
	agent, wire := workspaceAgent(t)
	r, err := agent.executeShellTask(&Task{ID: "empty", Payload: map[string]interface{}{"command": "true", "output_file": "xxz.txt"}})
	if err != nil || r.ExitCode != 0 || workspaceStreams(t, wire)["empty"] != "" {
		t.Fatalf("replayed old output: %+v %v %s", r, err, wire.String())
	}
	assertShellWorkspacesClean(t, agent)
}

func TestShellWorkspaceRelativeToolsAndFinalFlush(t *testing.T) {
	source := t.TempDir()
	t.Chdir(source)
	os.Mkdir("tools", 0700)
	os.WriteFile("tools/fixture", []byte("#!/bin/sh\nprintf 'fresh\\n' > \"$HOME/final.txt\"\nprintf 'fresh\\n'\n"), 0700)
	agent, wire := workspaceAgent(t)
	r, err := agent.executeShellTask(&Task{ID: "fast", Payload: map[string]interface{}{"command": "./tools/fixture", "output_file": "~/final.txt"}})
	if err != nil || r.ExitCode != 0 || workspaceStreams(t, wire)["fast"] != "fresh\n" {
		t.Fatalf("missing final flush or duplicate stdout: %+v %v %s", r, err, wire.String())
	}
	if _, err := os.Stat(filepath.Join(source, "final.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote output outside task workspace")
	}
	assertShellWorkspacesClean(t, agent)
}

func TestShellWorkspaceFailureRetainsPartialStreamAndCleans(t *testing.T) {
	agent, wire := workspaceAgent(t)
	r, err := agent.executeShellTask(&Task{ID: "failed", Payload: map[string]interface{}{"command": "printf 'partial\\n' > results.txt; exit 7", "output_file": "results.txt"}})
	if err != nil || r.ExitCode != 7 || workspaceStreams(t, wire)["failed"] != "partial\n" {
		t.Fatalf("lost partial output: %+v %v", r, err)
	}
	assertShellWorkspacesClean(t, agent)
}

func TestShellWorkspaceCancelDownload(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	agent, _ := workspaceAgent(t)
	agent.serverHTTPAddr = server.URL
	done := make(chan error, 1)
	go func() {
		_, err := agent.executeShellTask(&Task{ID: "download", Payload: map[string]interface{}{"command": "true", "download_url": "/input", "dest_name": "x.txt"}})
		done <- err
	}()
	<-started
	agent.taskCancelsMu.Lock()
	agent.taskCancels["download"]()
	agent.taskCancelsMu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled download succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download ignored cancellation")
	}
	assertShellWorkspacesClean(t, agent)
}

func TestShellWorkspaceRejectsEscapingPaths(t *testing.T) {
	for _, name := range []string{"../old.txt", "/root/old.txt", "tools/results.txt"} {
		agent, _ := workspaceAgent(t)
		_, err := agent.executeShellTask(&Task{ID: "escape", Payload: map[string]interface{}{"command": "true", "output_file": name}})
		if err == nil {
			t.Fatalf("accepted escaping output %q", name)
		}
		assertShellWorkspacesClean(t, agent)
	}
}

func TestShellWorkspaceLiveOutputAndCancelProcess(t *testing.T) {
	agent, wire := workspaceAgent(t)
	done := make(chan *TaskResult, 1)
	go func() {
		r, err := agent.executeShellTask(&Task{ID: "live", Payload: map[string]interface{}{
			"command": "printf 'live\\n' > results.txt; sleep 30", "output_file": "results.txt", "timeout_seconds": float64(NoTimeout),
		}})
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	deadline := time.After(3 * time.Second)
	for {
		agent.connMu.Lock()
		seen := strings.Contains(wire.String(), "live\\n")
		agent.connMu.Unlock()
		if seen {
			break
		}
		select {
		case <-deadline:
			t.Fatal("output did not arrive during execution")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-done:
		t.Fatal("fixture finished before cancellation")
	default:
	}
	agent.taskCancelsMu.Lock()
	agent.taskCancels["live"]()
	agent.taskCancelsMu.Unlock()
	select {
	case r := <-done:
		if r == nil || r.ExitCode == 0 || workspaceStreams(t, wire)["live"] != "live\n" {
			t.Fatalf("cancellation lost or duplicated output: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shell process group ignored cancellation")
	}
	assertShellWorkspacesClean(t, agent)
}

func TestShellFileRecordsWithConfiguredOutputDoNotReplay(t *testing.T) {
	agent, wire := workspaceAgent(t)
	r, err := agent.executeShellTask(&Task{ID: "cms-record", Payload: map[string]interface{}{"command": "printf '[FILE:drupal.txt] owned-fixture\\n'", "output_file": "ignored.txt", "file_records": true}})
	if err != nil || r.ExitCode != 0 || workspaceStreams(t, wire)["cms-record"] != "[FILE:drupal.txt] owned-fixture\n" {
		t.Fatalf("records missing or duplicated: %+v %v", r, err)
	}
}

func TestSpreadConfigRetainsDefaultRoot(t *testing.T) {
	if got := DefaultWorkerConfig().SpreadWorkspaceRoot; got != "/root" {
		t.Fatalf("default root = %q", got)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"worker_id":"legacy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadWorkerConfig(path)
	if err != nil || config.SpreadWorkspaceRoot != "/root" {
		t.Fatalf("legacy config = %+v, %v", config, err)
	}
}
