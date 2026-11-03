package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSimpleConfigCompatibility(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"routes":{"input.txt":["simple"]},"programs":{"simple":"run.py","legacy":{"entrypoint":"old.py"}}}`), &config); err != nil {
		t.Fatal(err)
	}
	p := config.Programs["simple"]
	if p.Entrypoint != "run.py" || !p.AutoPublish.Enabled || p.AutoPublish.Directory != "both" || p.Resources() != (Resources{CPUCores: 1, MemoryMB: 512, Threads: 1}) || p.Limit() != 6 || p.PriorityRank() != 1 {
		t.Fatalf("unexpected simple defaults: %+v", p)
	}
	if config.Programs["legacy"].AutoPublish.Enabled {
		t.Fatal("legacy object publication changed")
	}
	for _, invalid := range []string{`""`, `"  "`, `null`, `[]`, `7`} {
		if json.Unmarshal([]byte(invalid), &p) == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}

func TestSimpleConfigPublishesWorkAndOutput(t *testing.T) {
	var p Program
	if err := json.Unmarshal([]byte(`"run.py"`), &p); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	version := fixtureBundle(t, root, "import pathlib,sys\npathlib.Path('local.txt').write_text('work result\\n')\npathlib.Path(sys.argv[2],'output.txt').write_text('output result\\n')\n", p)
	var mu sync.Mutex
	lines := map[string]string{}
	completed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "renew":
			json.NewEncoder(w).Encode(Lease{Seconds: 30})
		case "input":
			w.Write([]byte("input is excluded\n"))
		case "publish":
			var publication Publication
			json.NewDecoder(r.Body).Decode(&publication)
			mu.Lock()
			lines[publication.Name] += strings.Join(publication.Lines, "")
			mu.Unlock()
			w.Write([]byte(`{"accepted":true}`))
		case "complete":
			var completion Completion
			json.NewDecoder(r.Body).Decode(&completion)
			mu.Lock()
			completed = completion.ExitCode == 0
			mu.Unlock()
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := (&Runner{Root: root}).Run(ctx, Assignment{AttemptID: "simple", RunID: "simple-run", Version: version, ProgramID: "fixture", BaseURL: srv.URL, Token: "fixture", PublishOnly: true}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !completed || len(lines) != 2 || lines["fixture__local.txt"] != "work result\n" || lines["fixture__output.txt"] != "output result\n" {
		t.Fatalf("completion=%v outputs=%v", completed, lines)
	}
}
