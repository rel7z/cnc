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

func TestAutomaticPublicationLiveFinalFailureAndRecovery(t *testing.T) {
	for _, location := range []string{"output", "work"} {
		t.Run(location, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(t.TempDir(), "executions")
			script := "import sys,pathlib,time\nfrom processor_publish import Publisher\nwith open(" + pythonString(marker) + ",'a') as f:f.write('once\\n')\np=pathlib.Path(" + map[string]string{"output": "sys.argv[2]", "work": "'.'"}[location] + ")\n(p/'nested').mkdir()\n(p/'nested'/'hidden.txt').write_text('excluded\\n')\n(p/'other.bin').write_text('excluded\\n')\nr=p/'results.txt'\n(p/'initial.tmp').write_text('first\\npartial')\n(p/'initial.tmp').replace(r)\nPublisher().publish(r)\ntime.sleep(3)\nwith r.open('a') as f:f.write(' completed\\nlast\\n')\nprint('finished')\n"
			version := fixtureBundle(t, root, script, Program{AutoPublish: AutoPublish{Enabled: true, Directory: location}})
			var mu sync.Mutex
			var publications []Publication
			var completion Completion
			live := false
			fail := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				parts := strings.Split(r.URL.Path, "/")
				action := parts[len(parts)-1]
				switch action {
				case "renew":
					json.NewEncoder(w).Encode(Lease{Seconds: 30})
				case "input":
					w.Write([]byte("input must not be published\n"))
				case "start":
					w.Write([]byte("{}"))
				case "publish":
					mu.Lock()
					defer mu.Unlock()
					if fail {
						w.WriteHeader(503)
						return
					}
					var p Publication
					json.NewDecoder(r.Body).Decode(&p)
					publications = append(publications, p)
					if len(p.Lines) == 1 && p.Lines[0] == "first\n" {
						live = true
					}
					w.Write([]byte(`{"accepted":true}`))
				case "complete":
					mu.Lock()
					json.NewDecoder(r.Body).Decode(&completion)
					mu.Unlock()
					w.Write([]byte("{}"))
				default:
					w.Write([]byte("{}"))
				}
			}))
			defer srv.Close()
			a := Assignment{AttemptID: "auto", RunID: "auto-run", ProgramID: "fixture", Version: version, BaseURL: srv.URL, Token: "token", PublishOnly: true}
			runner := &Runner{Root: root}
			if err := runner.Run(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			if !live || completion.ExitCode != 0 {
				t.Fatalf("live=%v completion=%+v", live, completion)
			}
			lines := []string{}
			for _, p := range publications {
				if p.Name != "fixture__results.txt" {
					t.Fatalf("unexpected output %q", p.Name)
				}
				lines = append(lines, p.Lines...)
			}
			mu.Unlock()
			if strings.Join(lines, "") != "first\npartial completed\nlast\n" {
				t.Fatalf("duplicated or missing output: %q publications=%+v", lines, publications)
			}
			// All output is captured during a transfer outage, then recovered without rerunning.
			a.AttemptID = "auto-outage"
			a.RunID = "auto-outage-run"
			mu.Lock()
			fail = true
			mu.Unlock()
			outageCtx, cancelOutage := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelOutage()
			if err := runner.Run(outageCtx, a); err == nil {
				t.Fatal("outage did not retain pending transfer")
			}
			mu.Lock()
			fail = false
			mu.Unlock()
			if err := runner.DeliverPending(context.Background()); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(marker)
			if string(data) != "once\nonce\n" {
				t.Fatalf("tool reran: %q", data)
			}
			mu.Lock()
			defer mu.Unlock()
			lines = nil
			for _, p := range publications {
				lines = append(lines, p.Lines...)
			}
			if strings.Join(lines, "") != "first\npartial completed\nlast\nfirst\npartial completed\nlast\n" {
				t.Fatalf("outage lost or duplicated output: %q", lines)
			}

		})
	}
}

func TestAutomaticPublicationSettingsVersionAndValidation(t *testing.T) {
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "run.py"), []byte("pass\n"), 0600)
	_, old, err := BuildBundle(source, "fixture", Program{Entrypoint: "run.py"})
	if err != nil {
		t.Fatal(err)
	}
	data, next, err := BuildBundle(source, "fixture", Program{Entrypoint: "run.py", AutoPublish: AutoPublish{Enabled: true}})
	if err != nil || old == next {
		t.Fatalf("settings not pinned: %v", err)
	}
	var b Bundle
	json.Unmarshal(data, &b)
	if !b.AutoPublish.Enabled || len(b.Files["processor_supervise.py"]) == 0 {
		t.Fatal("missing supervisor/settings")
	}
	for _, p := range []Program{{Entrypoint: "run.py", AutoPublish: AutoPublish{Enabled: true, Directory: "../outside"}}, {Entrypoint: "processor_supervise.py"}} {
		if _, _, err = BuildBundle(source, "fixture", p); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	if _, _, err = BuildBundle(source, "../fixture", Program{Entrypoint: "run.py", AutoPublish: AutoPublish{Enabled: true}}); err == nil {
		t.Fatal("unsafe ID accepted")
	}
}

func TestAutomaticPublicationToolFailureValidationAndCancellation(t *testing.T) {
	for _, scenario := range []string{"tool-failure", "invalid-output", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(t.TempDir(), "unexpected-surviving-child")
			script := "import sys,pathlib,time\np=pathlib.Path(sys.argv[2],'results.txt')\np.write_text('retained partial output\\n')\n"
			if scenario == "invalid-output" {
				script = "import sys,pathlib\npathlib.Path(sys.argv[2],'results.txt').write_bytes(b'\\xff\\n')\n"
			} else if scenario == "cancel" {
				script += "time.sleep(4)\npathlib.Path(" + pythonString(marker) + ").write_text('survived')\n"
			} else {
				script += "sys.exit(7)\n"
			}
			version := fixtureBundle(t, root, script, Program{AutoPublish: AutoPublish{Enabled: true}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var lines []string
			var completion Completion
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				parts := strings.Split(r.URL.Path, "/")
				switch parts[len(parts)-1] {
				case "renew":
					json.NewEncoder(w).Encode(Lease{Seconds: 30})
				case "input", "start", "log":
					w.Write([]byte("{}"))
				case "publish":
					var p Publication
					json.NewDecoder(r.Body).Decode(&p)
					mu.Lock()
					lines = append(lines, p.Lines...)
					mu.Unlock()
					w.Write([]byte(`{"accepted":true}`))
					if scenario == "cancel" {
						cancel()
					}
				case "complete":
					mu.Lock()
					json.NewDecoder(r.Body).Decode(&completion)
					mu.Unlock()
					w.Write([]byte("{}"))
				}
			}))
			defer srv.Close()
			runner := &Runner{Root: root}
			err := runner.Run(ctx, Assignment{AttemptID: "test", RunID: "test-run", ProgramID: "fixture", Version: version, BaseURL: srv.URL, Token: "token", PublishOnly: true})
			if scenario == "cancel" {
				if err == nil {
					t.Fatal("cancelled job succeeded")
				}
				time.Sleep(4 * time.Second)
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("child survived cancellation")
				}
				data, err := os.ReadFile(filepath.Join(root, "attempts", "test", "completion.json"))
				if err != nil {
					t.Fatal(err)
				}
				json.Unmarshal(data, &completion)
				if !completion.Interrupted {
					t.Fatalf("cancellation not recorded: %+v", completion)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if scenario == "invalid-output" {
				if completion.ExitCode != 65 || len(lines) != 0 {
					t.Fatalf("invalid output not rejected: %+v %q", completion, lines)
				}
				log, _ := os.ReadFile(filepath.Join(root, "attempts", "test", "log"))
				if !strings.Contains(string(log), "automatic publication error") {
					t.Fatal("missing visible validation error")
				}
			} else {
				if strings.Join(lines, "") != "retained partial output\n" {
					t.Fatalf("partial output lost: %q", lines)
				}
				if scenario == "tool-failure" && completion.ExitCode != 7 {
					t.Fatalf("tool failure changed: %+v", completion)
				}
			}
		})
	}
}
