package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func manifestFixture(t *testing.T, manifest string) (string, Config) {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{"tool.json": manifest, "scanner.py": "pass\n", "nested/checker.py": "pass\n", "shared.txt": "static\n"} {
		path := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(data), 0600)
	}
	c := Config{Routes: map[string][]string{"incoming.txt": {"tool"}}, Programs: map[string]Program{"tool": {Manifest: "tool.json"}}}
	return root, c
}

func TestManifestExpansionAndValidation(t *testing.T) {
	root, c := manifestFixture(t, `{"scripts":{"scanner":{"path":"scanner.py","outputs":["results.txt"]},"checker":{"path":"nested/checker.py","outputs":["checked.txt"]}},"files":["shared.txt"]}`)
	expanded, err := c.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(expanded.Routes["incoming.txt"], ",") != "tool--checker,tool--scanner" || len(expanded.Programs) != 2 {
		t.Fatalf("expansion: %+v", expanded)
	}
	for id, p := range expanded.Programs {
		if p.Script.Input != "input.txt" || len(p.Files) != 1 {
			t.Fatalf("defaults: %+v", p)
		}
		data, _, err := BuildBundle(root, id, p)
		if err != nil {
			t.Fatal(err)
		}
		var b Bundle
		json.Unmarshal(data, &b)
		if b.Script == nil || len(b.Files) != 4 {
			t.Fatalf("explicit files: %+v", b)
		}
	}
	for _, bad := range []string{
		`{"scripts":{}}`,
		`{"input":"../outside","scripts":{"scanner":{"path":"scanner.py","outputs":["results.txt"]}}}`,
		`{"scripts":{"scanner":{"path":"../outside.py","outputs":["results.txt"]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":[]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":["input.txt"]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":["a/results.txt","b/results.txt"]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":["../outside.txt"]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":["results.json"]}}}`,
		`{"unknown":true,"scripts":{"scanner":{"path":"scanner.py","outputs":["results.txt"]}}}`,
		`{"files":["input.txt"],"scripts":{"scanner":{"path":"scanner.py","outputs":["results.txt"]}}}`,
	} {
		os.WriteFile(filepath.Join(root, "tool.json"), []byte(bad), 0600)
		if _, err := c.ResolveManifests(root); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	os.WriteFile(filepath.Join(root, "tool.json"), []byte(`{"scripts":{"scanner":{"path":"alias.py","outputs":["results.txt"]}}}`), 0600)
	os.Symlink("scanner.py", filepath.Join(root, "alias.py"))
	if _, err := c.ResolveManifests(root); err == nil {
		t.Fatal("accepted symlink script")
	}
	os.WriteFile(filepath.Join(root, "tool.json"), []byte(`{"scripts":{"scanner":{"path":"alias/scanner.py","outputs":["results.txt"]}}}`), 0600)
	os.Symlink(".", filepath.Join(root, "alias"))
	if _, err := c.ResolveManifests(root); err == nil {
		t.Fatal("accepted symlink directory")
	}
}

func TestManifestIndependentConcurrentJobsAndSelectedOutputs(t *testing.T) {
	root, c := manifestFixture(t, `{"input":"targets.txt","scripts":{"scanner":{"path":"scanner.py","outputs":["results.txt","missing.txt"]},"checker":{"path":"nested/checker.py","outputs":["results.txt"]}}}`)
	script := "from pathlib import Path\nimport sys,time\nassert len(sys.argv)==1\nfolder=Path(__file__).parent\nassert folder.resolve()==Path.cwd().resolve()\ntext=(folder/'targets.txt').read_text()\nwith (folder/'results.txt').open('w') as f:\n f.write(text);f.flush();time.sleep(2)\n(folder/'ignored.txt').write_text('excluded\\n')\n"
	os.WriteFile(filepath.Join(root, "scanner.py"), []byte(script), 0600)
	os.WriteFile(filepath.Join(root, "nested/checker.py"), []byte(script), 0600)
	expanded, err := c.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	results := map[string]string{}
	completed := map[string]bool{}
	live := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		action := parts[len(parts)-1]
		id := parts[len(parts)-2]
		switch action {
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		case "input":
			w.Write([]byte(id + "\n"))
		case "publish":
			var p Publication
			json.NewDecoder(r.Body).Decode(&p)
			mu.Lock()
			results[id+"/"+p.Name] += strings.Join(p.Lines, "")
			live = live || !completed[id]
			mu.Unlock()
			w.Write([]byte(`{"accepted":true}`))
		case "complete":
			var completion Completion
			json.NewDecoder(r.Body).Decode(&completion)
			mu.Lock()
			completed[id] = completion.ExitCode == 0
			mu.Unlock()
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	worker := t.TempDir()
	runner := &Runner{Root: worker}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for id, p := range expanded.Programs {
		data, version, err := BuildBundle(root, id, p)
		if err != nil {
			t.Fatal(err)
		}
		bundleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
		_, err = InstallBundle(ctx, worker, Install{Version: version, BundleURL: bundleServer.URL})
		bundleServer.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"-a", "-b"} {
			a := Assignment{AttemptID: id + suffix, RunID: id + suffix, ProgramID: id, Version: version, BaseURL: srv.URL, Token: "fixture", PublishOnly: true}
			wg.Add(1)
			go func() { defer wg.Done(); errs <- runner.Run(ctx, a) }()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !live || len(completed) != 4 || len(results) != 4 {
		t.Fatalf("live=%v completed=%v results=%v", live, completed, results)
	}
	for id, ok := range completed {
		if !ok {
			t.Fatalf("failed %s", id)
		}
		program := strings.TrimSuffix(strings.TrimSuffix(id, "-a"), "-b")
		if results[id+"/"+program+"__results.txt"] != id+"\n" {
			t.Fatalf("unexpected %s: %v", id, results)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "targets.txt")); !os.IsNotExist(err) {
		t.Fatal("original tool directory modified")
	}
}
