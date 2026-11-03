package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cnc-enum/version"
)

func pipelineFixture(t *testing.T, serverURL string, rules []version.VersionRule) pipelineOptions {
	t.Helper()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(serverURL, "http://"))
	n, _ := strconv.Atoi(port)
	directory := t.TempDir()
	artifact := version.ArtifactV1{SchemaVersion: 1, JobID: "fixture-job", TargetScope: version.TargetScope{CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{n}}, Rules: rules}
	data, digest, err := version.ArtifactBytes(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path, err := version.WriteImmutable(directory, "artifact.json", data)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(directory, "input.txt")
	if err := os.WriteFile(input, []byte(serverURL+"\n"+serverURL+"/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return pipelineOptions{Input: input, Artifact: path, Digest: digest, JobID: artifact.JobID, ShardID: "fixture-shard", OutputDir: filepath.Join(directory, "output"), Threads: 2, Timeout: time.Second}
}
func TestStrictPipelineEmitsOneRecordWithAllMatchesAndExcludesConflict(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/wp-json/":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"generator":{"name":"WordPress","version":"6.4"}}`)
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><meta name="generator" content="WordPress 6.4.0"><script src="wp-content"></script>wp-includes</html>`)
		case "/readme.html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html>WordPress<h1>Version 6.4.0</h1></html>`)
		case "/wp-content/plugins/example/readme.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "=== Example ===\nStable tag: 1.2\n")
		case "/wp-content/plugins/example/package.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"name":"example","version":"1.3"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	rules := []version.VersionRule{{ID: "range", Name: "Supported core", Component: "wordpress:core", Clauses: []version.VersionClause{{Operator: ">=", Version: "6.4"}, {Operator: "<", Version: "6.5"}}}, {ID: "exact", Name: "Exact core", Component: "wordpress:core", Clauses: []version.VersionClause{{Operator: "=", Version: "6.4.0"}}}, {ID: "plugin", Name: "Plugin version", Component: "wordpress:plugin:example", Clauses: []version.VersionClause{{Operator: ">=", Version: "1"}}}}
	options := pipelineFixture(t, server.URL, rules)
	var stdout, stderr bytes.Buffer
	if code := runVersionPipeline(context.Background(), options, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, &stderr)
	}
	basename, _ := version.ResultBasename(options.JobID, options.ShardID)
	data, err := os.ReadFile(filepath.Join(options.OutputDir, basename))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 1 {
		t.Fatalf("expected one known component: %s", data)
	}
	var record version.ResultRecordV1
	if json.Unmarshal(lines[0], &record) != nil {
		t.Fatal(string(data))
	}
	if record.Observation.Component != "wordpress:core" || len(record.MatchedRules) != 2 || record.MatchedRules[0].ID != "exact" || record.Observation.SelectedEvidence.Rank != 10 || len(record.Observation.EvidenceCandidates) != 3 {
		t.Fatalf("%+v", record)
	}
	if !strings.Contains(stdout.String(), "[FILE:wordpress.txt]") || strings.Contains(stdout.String(), ".jsonl") {
		t.Fatalf("expected clean text export without internal JSONL in stdout: %s", &stdout)
	}
	if requests.Load() != 9 {
		t.Fatalf("duplicate canonical target fetched %d requests", requests.Load())
	}
}
func TestBadArtifactMakesNoRequestsOrResultFiles(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	options := pipelineFixture(t, server.URL, nil)
	options.Digest = strings.Repeat("0", 64)
	var stdout, stderr bytes.Buffer
	if runVersionPipeline(context.Background(), options, &stdout, &stderr) != 1 || requests.Load() != 0 || stdout.Len() != 0 {
		t.Fatalf("invalid artifact ran: %s", &stdout)
	}
	if _, err := os.Stat(options.OutputDir); !os.IsNotExist(err) {
		t.Fatal("created match directory for invalid artifact")
	}
}
func TestNoRulePipelinePreservesDrupalAndRejectsBlockedVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, "drupal sites/default core/modules drupal.js")
			return
		}
		http.Error(w, "Forbidden", 403)
	}))
	defer server.Close()
	options := pipelineFixture(t, server.URL, nil)
	// Ordinary discovery must also run without an explicit target scope.
	artifact, err := version.LoadArtifact(options.Artifact, options.Digest, options.JobID)
	if err != nil {
		t.Fatal(err)
	}
	artifact.TargetScope = version.TargetScope{}
	data, digest, err := version.ArtifactBytes(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path, err := version.WriteImmutable(filepath.Dir(options.Artifact), "unrestricted.json", data)
	if err != nil {
		t.Fatal(err)
	}
	options.Artifact, options.Digest = path, digest

	var stdout, stderr bytes.Buffer
	if code := runVersionPipeline(context.Background(), options, &stdout, &stderr); code != 0 {
		t.Fatalf("%d %s", code, &stderr)
	}
	if !strings.Contains(stdout.String(), "[FILE:drupal.txt] "+server.URL+"/") {
		t.Fatal(&stdout)
	}
	basename, _ := version.ResultBasename(options.JobID, options.ShardID)
	data, err = os.ReadFile(filepath.Join(options.OutputDir, basename))
	if err != nil || len(data) != 0 {
		t.Fatalf("blocked evidence published: %s %v", data, err)
	}
}

func TestPipelineRetainsKnownRecordOnCancellationAndPartialHTTPFailure(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelRequest), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<meta name="generator" content="WordPress 6.4">`)
				case "/wp-content/plugins/fixture/readme.txt":
					if cancelRequest {
						cancel()
						return
					}
					http.Error(w, "owned transport failure", 503)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			rules := []version.VersionRule{{ID: "core", Name: "core", Component: "wordpress:core", Clauses: []version.VersionClause{{Operator: "=", Version: "6.4"}}}, {ID: "plugin", Name: "plugin", Component: "wordpress:plugin:fixture", Clauses: []version.VersionClause{{Operator: "=", Version: "1"}}}}
			options := pipelineFixture(t, server.URL, rules)
			var stdout, stderr bytes.Buffer
			expectedExit := 0
			if cancelRequest {
				expectedExit = 1
			}
			if exit := runVersionPipeline(ctx, options, &stdout, &stderr); exit != expectedExit {
				t.Fatalf("exit=%d expected=%d stderr=%s", exit, expectedExit, &stderr)
			}
			basename, _ := version.ResultBasename(options.JobID, options.ShardID)
			data, err := os.ReadFile(filepath.Join(options.OutputDir, basename))
			if err != nil {
				t.Fatal(err)
			}
			record, err := version.DecodeResultLine(bytes.TrimSuffix(data, []byte{'\n'}))
			if err != nil || record.Observation.Component != "wordpress:core" || len(record.MatchedRules) != 1 {
				t.Fatalf("partial output: %s %v", data, err)
			}
			if strings.Contains(stdout.String(), ".jsonl") {
				t.Fatal("internal JSONL leaked into exported output")
			}
		})
	}
}

func TestNoRuleAdminPipelineRejectsCatchAllAndReportsServerFailure(t *testing.T) {
	for _, kind := range []string{"real", "catch-all", "403", "server-failure"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "server-failure" {
					http.Error(w, "owned failure", 503)
					return
				}
				if kind == "403" {
					http.Error(w, "forbidden", 403)
					return
				}
				if kind == "catch-all" || r.URL.Path == hardcodedAdminPaths[0] {
					fmt.Fprint(w, loginPage)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			options := pipelineFixture(t, server.URL, nil)
			var stdout, stderr bytes.Buffer
			exit := runVersionPipeline(context.Background(), options, &stdout, &stderr)
			expectedExit := 0
			if kind == "server-failure" || kind == "403" {
				expectedExit = 1
			}
			if exit != expectedExit {
				t.Fatalf("exit=%d expected=%d stderr=%s", exit, expectedExit, &stderr)
			}
			foundAdmin := strings.Count(stdout.String(), "[FILE:admin.txt]")
			foundEnumAdmin := strings.Count(stdout.String(), "[FILE:enum-admin.txt]")
			if kind == "real" {
				if foundAdmin != 1 || foundEnumAdmin != 1 {
					t.Fatalf("admin findings: %s", &stdout)
				}
			} else {
				if foundAdmin != 0 || foundEnumAdmin != 0 {
					t.Fatalf("unexpected admin findings: %s", &stdout)
				}
			}
		})
	}
}

func TestScopedAdminCancellationStopsActiveRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer fixture.Close()
	defer cancel()
	target, _ := version.CanonicalTarget(fixture.URL)
	finished := make(chan error, 1)
	go func() { _, err := adminScoped(ctx, fixture.Client(), target); finished <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("admin request did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancellation was not reported")
		}
	case <-time.After(time.Second):
		t.Fatal("admin request ignored cancellation")
	}
}

func TestPipelineEmitsWordPressAdminAndPluginOutputs(t *testing.T) {
	wpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><meta name="generator" content="WordPress 6.4"></head><body>hello</body></html>`)
		case "/wp-content/plugins/elementor/readme.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "=== Elementor ===\nStable tag: 3.16.0\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer wpServer.Close()

	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == hardcodedAdminPaths[0] {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Admin Control Panel Login</title></head><body><form><input type="password" name="pass"></form></body></html>`)
			return
		}
		http.NotFound(w, r)
	}))
	defer adminServer.Close()

	rules := []version.VersionRule{
		{
			ID:        "wp-elementor",
			Name:      "Elementor check",
			Component: "wordpress:plugin:elementor",
			Clauses:   []version.VersionClause{{Operator: ">=", Version: "3.0"}},
		},
	}

	directory := t.TempDir()
	artifact := version.ArtifactV1{
		SchemaVersion: 1,
		JobID:         "job-wp-admin-test",
		TargetScope:   version.TargetScope{},
		Rules:         rules,
	}
	data, digest, err := version.ArtifactBytes(artifact)
	if err != nil {
		t.Fatal(err)
	}
	artPath, err := version.WriteImmutable(directory, "artifact.json", data)
	if err != nil {
		t.Fatal(err)
	}

	input := filepath.Join(directory, "targets.txt")
	if err := os.WriteFile(input, []byte(wpServer.URL+"\n"+adminServer.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	opts := pipelineOptions{
		Input:     input,
		Artifact:  artPath,
		Digest:    digest,
		JobID:     artifact.JobID,
		ShardID:   "shard-1",
		OutputDir: filepath.Join(directory, "output"),
		Threads:   4,
		Timeout:   2 * time.Second,
		Automatic: true,
	}

	var stdout, stderr bytes.Buffer
	code := runVersionPipeline(context.Background(), opts, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "[FILE:wordpress.txt] "+wpServer.URL) {
		t.Errorf("missing [FILE:wordpress.txt] in stdout:\n%s", out)
	}
	if !strings.Contains(out, "[FILE:elementor.txt]") {
		t.Errorf("missing [FILE:elementor.txt] in stdout:\n%s", out)
	}
	if !strings.Contains(out, "[FILE:plugins_name.txt]") {
		t.Errorf("missing [FILE:plugins_name.txt] in stdout:\n%s", out)
	}
	if !strings.Contains(out, "[FILE:elementor.txt] "+wpServer.URL+"/\n") {
		t.Errorf("plugin list does not contain a clean URL:\n%s", out)
	}
	if !strings.Contains(out, "[FILE:plugins_name.txt] "+wpServer.URL+"/wp-content/plugins/elementor\n") {
		t.Errorf("plugin path list does not contain a clean URL:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "[FILE:") || !strings.Contains(line, ".txt]") {
			continue
		}
		payload := strings.TrimSpace(line[strings.Index(line, "]")+1:])
		if len(strings.Fields(payload)) != 1 || strings.Contains(payload, "component=") || strings.Contains(payload, "version=") || strings.Contains(payload, "confidence=") {
			t.Errorf("text result contains metadata: %q", line)
		}
	}
	if !strings.Contains(out, "[FILE:admin.txt] "+adminServer.URL+hardcodedAdminPaths[0]) {
		t.Errorf("missing [FILE:admin.txt] in stdout:\n%s", out)
	}
	if !strings.Contains(out, "[FILE:enum-admin.txt] "+adminServer.URL+hardcodedAdminPaths[0]) {
		t.Errorf("missing [FILE:enum-admin.txt] in stdout:\n%s", out)
	}
}

func TestCleanOutputURLAndBaseURL(t *testing.T) {
	for _, tc := range []struct {
		input       string
		wantURL     string
		wantBaseURL string
	}{
		{"https://finehomekeeping.com:443/", "https://finehomekeeping.com/", "https://finehomekeeping.com"},
		{"http://example.com:80/", "http://example.com/", "http://example.com"},
		{"https://1winlogin.ng:443/wp-content/plugins/wp-file-manager", "https://1winlogin.ng/wp-content/plugins/wp-file-manager", "https://1winlogin.ng/wp-content/plugins/wp-file-manager"},
		{"https://box5730.bluehost.com:2083/", "https://box5730.bluehost.com:2083/", "https://box5730.bluehost.com:2083"},
		{"http://example.com:443/a", "http://example.com:443/a", "http://example.com:443/a"},
		{"https://example.com:80/a", "https://example.com:80/a", "https://example.com:80/a"},
		{"https://example.com:443/a:443/b", "https://example.com/a:443/b", "https://example.com/a:443/b"},
		{"https://[::1]:443/", "https://[::1]/", "https://[::1]"},
	} {
		gotURL := cleanOutputURL(tc.input)
		if gotURL != tc.wantURL {
			t.Errorf("cleanOutputURL(%q) = %q, want %q", tc.input, gotURL, tc.wantURL)
		}
		target, err := version.CanonicalTarget(tc.input)
		if err == nil {
			gotBase := cleanBaseURL(target)
			if gotBase != tc.wantBaseURL {
				t.Errorf("cleanBaseURL(%q) = %q, want %q", tc.input, gotBase, tc.wantBaseURL)
			}
		}
	}
}

func TestWordPressCoreFeedInventoryAndFilter(t *testing.T) {
	for _, tc := range []struct {
		name, generator, filter string
		want                    bool
	}{
		{"feed inventory", "6.8.1", ">=", true},
		{"filter excludes", "6.8.1", "<", false},
		{"hidden version", "", ">=", false},
		{"conflicting evidence", "6.8.2", ">=", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<html><script src="/wp-includes/test.js"></script><a href="/wp-content/">WordPress</a>`)
					if tc.name == "conflicting evidence" {
						fmt.Fprint(w, `<meta name="generator" content="WordPress 6.8.1">`)
					}
					fmt.Fprint(w, `</html>`)
				case "/feed/":
					w.Header().Set("Content-Type", "application/rss+xml")
					fmt.Fprintf(w, `<rss version="2.0"><channel><generator>https://wordpress.org/?v=%s</generator></channel></rss>`, tc.generator)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			boundary := "0.0.0-0"
			if tc.filter == "<" {
				boundary = "6.8.0"
			}
			options := pipelineFixture(t, server.URL, []version.VersionRule{{ID: "core", Name: "WordPress core", Component: "wordpress:core", Clauses: []version.VersionClause{{Operator: tc.filter, Version: boundary}}}})
			var stdout, stderr bytes.Buffer
			if code := runVersionPipeline(context.Background(), options, &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d: %s", code, &stderr)
			}
			marker := "[FILE:wordpress_core_versions.txt] " + server.URL + "/\t6.8.1"
			if strings.Contains(stdout.String(), marker) != tc.want {
				t.Fatalf("wrong version export: %s", &stdout)
			}
			if !tc.want && strings.Contains(stdout.String(), "[FILE:wordpress_core_versions.txt]") {
				t.Fatalf("unconfirmed output: %s", &stdout)
			}
			basename, _ := version.ResultBasename(options.JobID, options.ShardID)
			data, err := os.ReadFile(filepath.Join(options.OutputDir, basename))
			if err != nil {
				t.Fatal(err)
			}
			if tc.want {
				var record version.ResultRecordV1
				if json.Unmarshal(bytes.TrimSpace(data), &record) != nil || record.Observation.SelectedEvidence == nil || record.Observation.SelectedEvidence.Source != "wp_feed_generator" || len(record.MatchedRules) != 1 {
					t.Fatalf("invalid receipt: %s", data)
				}
			} else if len(bytes.TrimSpace(data)) != 0 {
				t.Fatalf("unexpected match: %s", data)
			}
		})
	}
}

func TestPHPScannerIndependentOfCMSAndExactRangeBoundaries(t *testing.T) {
	for _, branch := range []struct{ lower, upper, last string }{{"8.5.0", "8.5.12", "8.5.11"}, {"8.4.0", "8.4.27", "8.4.26"}, {"8.3.0", "8.3.36", "8.3.35"}, {"8.2.0", "8.2.35", "8.2.34"}} {
		for _, testVersion := range []string{branch.lower, branch.last, branch.upper} {
			t.Run(testVersion, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/" {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("X-Powered-By", "PHP/"+testVersion)
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, "<html><title>Plain application</title></html>")
				}))
				defer server.Close()
				rules := []version.VersionRule{{ID: "php", Name: "PHP range", Component: "php:php-src", Clauses: []version.VersionClause{{Operator: ">=", Version: branch.lower}, {Operator: "<", Version: branch.upper}}}}
				options := pipelineFixture(t, server.URL, rules)
				var stdout, stderr bytes.Buffer
				if code := runVersionPipeline(context.Background(), options, &stdout, &stderr); code != 0 {
					t.Fatalf("exit %d: %s", code, &stderr)
				}
				want := testVersion != branch.upper
				if strings.Contains(stdout.String(), "[FILE:php_versions.txt] "+server.URL+"/\t"+testVersion) != want {
					t.Fatalf("wrong version export %s", &stdout)
				}
				if strings.Contains(stdout.String(), "[FILE:php-ver.txt] "+server.URL+"/") != want {
					t.Fatalf("wrong URL export %s", &stdout)
				}
				basename, _ := version.ResultBasename(options.JobID, options.ShardID)
				data, err := os.ReadFile(filepath.Join(options.OutputDir, basename))
				if err != nil {
					t.Fatal(err)
				}
				if want {
					var record version.ResultRecordV1
					if json.Unmarshal(bytes.TrimSpace(data), &record) != nil || record.Observation.Confidence != version.ConfidenceReported || len(record.MatchedRules) != 1 {
						t.Fatalf("invalid PHP receipt: %s", data)
					}
					artifactData, err := os.ReadFile(options.Artifact)
					if err != nil {
						t.Fatal(err)
					}
					var artifact version.ArtifactV1
					if json.Unmarshal(artifactData, &artifact) != nil {
						t.Fatal("artifact")
					}
					if err := version.ValidateResult(record, artifact, options.ShardID); err != nil {
						t.Fatal(err)
					}
				} else if len(bytes.TrimSpace(data)) != 0 {
					t.Fatalf("excluded version published: %s", data)
				}
			})
		}
	}
}
