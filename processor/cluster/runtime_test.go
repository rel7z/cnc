package cluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeManifestGraphAndPolicies(t *testing.T) {
	root, c := manifestFixture(t, `{"input":"wordpress.txt","scripts":{"scanner":{"path":"scanner.py","outputs":[{"path":"results.txt","send_to":"checker","drive":false}]},"checker":{"path":"nested/checker.py","input":"results.txt","outputs":["success.txt"]}}}`)
	expanded, err := c.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(expanded.Routes["incoming.txt"], ",") != "tool--scanner" {
		t.Fatal(expanded.Routes)
	}
	p := expanded.Programs["tool--scanner"]
	if p.Script.Policies[0].Drive || p.Script.Policies[0].SendTo != "tool--checker" {
		t.Fatal(p)
	}
	if expanded.Programs["tool--checker"].Script.Input != "results.txt" {
		t.Fatal("input override")
	}
	p.Script.Policies[0].Target = &PinnedTarget{ProgramID: "tool--checker", Version: "frozen", Limit: 6}
	data, _, err := BuildBundle(root, "tool--scanner", p)
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	if b.Script.Policies[0].Target.Version != "frozen" {
		t.Fatal("lost pinned target")
	}
	for _, bad := range []string{
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":[{"path":"results.txt","send_to":"absent"}]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":[{"path":"results.txt","send_to":"scanner"}]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","runtime":"unknown","outputs":["results.txt"]}}}`,
		`{"scripts":{"scanner":{"path":"scanner.py","outputs":[{"path":"results.txt","target":{"version":"fake"}}]}}}`,
	} {
		os.WriteFile(filepath.Join(root, "tool.json"), []byte(bad), 0600)
		if _, err := c.ResolveManifests(root); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	caps := DetectRuntimeCapabilities()
	caps.Runtimes["node"] = false
	node := Program{Entrypoint: "scanner.js", Script: &ScriptExecution{Runtime: "node", Input: "input.txt", Outputs: []string{"r.txt"}}}
	if RuntimeAdmission(node, caps) == "" || RuntimeAdmission(node, nil) == "" {
		t.Fatal("missing runtime admitted")
	}
	if RuntimeAdmission(Program{}, nil) != "" {
		t.Fatal("legacy blocked")
	}
	binary := node
	binary.Script = &ScriptExecution{Runtime: "binary", BinaryArch: "not-this-architecture"}
	if RuntimeAdmission(binary, caps) == "" {
		t.Fatal("wrong binary admitted")
	}
}
func TestRuntimeProgramsNoArgumentsLiveSelectedOutput(t *testing.T) {
	for _, runtime := range []string{"bash", "node"} {
		t.Run(runtime, func(t *testing.T) {
			command := runtime
			if _, err := exec.LookPath(command); err != nil {
				t.Skip(err)
			}
			root := t.TempDir()
			path := "scanner.sh"
			script := "test \"$#\" -eq 0 || exit 7\ncat input.txt > results.txt\nsleep 2\nprintf 'SUMMARY: completed fixture\\n'\nprintf 'SUMMARY: stderr retained\\n' >&2\n"
			if runtime == "node" {
				path = "scanner.js"
				script = "const fs=require('fs');if(process.argv.length!==2)process.exit(7);fs.writeFileSync('results.txt',fs.readFileSync('input.txt'));setTimeout(()=>{console.log('SUMMARY: completed fixture');console.error('SUMMARY: stderr retained');},2000);"
			}
			os.WriteFile(filepath.Join(root, path), []byte(script), 0600)
			c := Config{Routes: map[string][]string{"in.txt": {"tool"}}, Programs: map[string]Program{"tool": {Manifest: "tool.json"}}}
			manifest := `{"scripts":{"scanner":{"path":"` + path + `","outputs":["results.txt"]}}}`
			os.WriteFile(filepath.Join(root, "tool.json"), []byte(manifest), 0600)
			expanded, err := c.ResolveManifests(root)
			if err != nil {
				t.Fatal(err)
			}
			p := expanded.Programs["tool--scanner"]
			data, version, err := BuildBundle(root, "tool--scanner", p)
			if err != nil {
				t.Fatal(err)
			}
			live := false
			complete := false
			result := ""
			finalLog := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch filepath.Base(r.URL.Path) {
				case "bundle":
					w.Write(data)
				case "input":
					w.Write([]byte("owned fixture\n"))
				case "publish":
					var pub Publication
					json.NewDecoder(r.Body).Decode(&pub)
					result += strings.Join(pub.Lines, "")
					live = live || !complete
					w.Write([]byte(`{"accepted":true}`))
				case "complete":
					var c Completion
					json.NewDecoder(r.Body).Decode(&c)
					complete = c.ExitCode == 0
					if !strings.Contains(finalLog, "SUMMARY: completed fixture") || !strings.Contains(finalLog, "SUMMARY: stderr retained") {
						t.Error("completion arrived without the final stdout/stderr summary log")
					}
					w.Write([]byte(`{}`))
				case "log":
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					finalLog = string(body)
					w.Write([]byte(`{}`))
				case "renew":
					json.NewEncoder(w).Encode(Lease{Seconds: 30})
				default:
					w.Write([]byte(`{}`))
				}
			}))
			defer srv.Close()
			worker := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := InstallBundle(ctx, worker, Install{Version: version, BundleURL: srv.URL + "/bundle"}); err != nil {
				t.Fatal(err)
			}
			if err := (&Runner{Root: worker}).Run(ctx, Assignment{AttemptID: "fixture", RunID: "fixture", ProgramID: "tool--scanner", Version: version, BaseURL: srv.URL, PublishOnly: true}); err != nil {
				t.Fatal(err)
			}
			if !live || !complete || result != "owned fixture\n" {
				t.Fatalf("live=%v complete=%v results=%q", live, complete, result)
			}
		})
	}
}

func TestNodeLockedCacheDisablesScriptsAndRejectsMutation(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"scanner.js":        "require('fs').writeFileSync('results.txt','owned\\n')",
		"package.json":      `{"name":"owned-fixture","version":"1.0.0","scripts":{"postinstall":"node -e \"require('fs').writeFileSync('lifecycle-ran','bad')\""}}`,
		"package-lock.json": `{"name":"owned-fixture","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"owned-fixture","version":"1.0.0"}}}`,
		"tool.json":         `{"scripts":{"scanner":{"path":"scanner.js","package_lock":"package-lock.json","outputs":["results.txt"]}}}`,
	}
	for path, data := range files {
		os.WriteFile(filepath.Join(root, path), []byte(data), 0600)
	}
	c := Config{Programs: map[string]Program{"tool": {Manifest: "tool.json"}}}
	expanded, err := c.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	data, version, err := BuildBundle(root, "tool--scanner", expanded.Programs["tool--scanner"])
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write(data) }))
	defer srv.Close()
	worker := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spec := Install{Version: version, BundleURL: srv.URL}
	folder, err := InstallBundle(ctx, worker, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(folder, "lifecycle-ran")); !os.IsNotExist(err) {
		t.Fatal("npm lifecycle ran")
	}
	if _, err := InstallBundle(ctx, worker, spec); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("cache downloaded again", requests)
	}
	os.MkdirAll(filepath.Join(folder, "node_modules"), 0700)
	os.WriteFile(filepath.Join(folder, "node_modules/tampered.js"), []byte("bad"), 0600)
	if _, err := LoadBundle(worker, version); err == nil {
		t.Fatal("mutated dependency cache accepted")
	}
}
