package main

import (
	"bytes"
	"cnc-enum/version"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginTextRequiresMatchingRule(t *testing.T) {
	for _, cms := range []string{"wordpress", "joomla"} {
		for _, v := range []string{"1.0.0", "2.0.0", "9.0.0"} {
			t.Run(cms+"/"+v, func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/":
						w.Header().Set("Content-Type", "text/html")
						fmt.Fprintf(w, `<html><meta name="generator" content="%s 6.4"></html>`, map[string]string{"wordpress": "WordPress", "joomla": "Joomla!"}[cms])
					case "/wp-content/plugins/fixture/readme.txt":
						if cms != "wordpress" {
							http.NotFound(w, r)
							return
						}
						w.Header().Set("Content-Type", "text/plain")
						fmt.Fprintf(w, "=== Fixture ===\nStable tag: %s\n", v)
					case "/plugins/system/fixture/fixture.xml":
						if cms != "joomla" {
							http.NotFound(w, r)
							return
						}
						w.Header().Set("Content-Type", "application/xml")
						fmt.Fprintf(w, `<extension type="plugin"><name>fixture</name><version>%s</version></extension>`, v)
					default:
						http.NotFound(w, r)
					}
				}))
				defer s.Close()
				component := "wordpress:plugin:fixture"
				if cms == "joomla" {
					component = "joomla:plugin:system:fixture"
				}
				opts := pipelineFixture(t, s.URL, []version.VersionRule{{ID: "below-two", Name: "Below two", Component: component, Clauses: []version.VersionClause{{Operator: "<", Version: "2.0.0"}}}})
				var out, logs bytes.Buffer
				if code := runVersionPipeline(context.Background(), opts, &out, &logs); code != 0 {
					t.Fatalf("exit %d: %s", code, &logs)
				}
				want := v == "1.0.0"
				marker := "[FILE:fixture.txt]"
				if cms == "joomla" {
					marker = "[FILE:plugin:system:fixture.txt]"
				}
				if strings.Contains(out.String(), marker) != want {
					t.Fatalf("incorrect rule text: %s", &out)
				}
				if cms == "wordpress" && strings.Contains(out.String(), "[FILE:plugins_name.txt]") != want {
					t.Fatalf("incorrect plugin names: %s", &out)
				}
				for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
					fields := strings.Fields(line)
					if len(fields) != 2 || !strings.HasPrefix(fields[1], "http://") {
						t.Fatalf("unclean output: %q", line)
					}
				}
			})
		}
	}
}

func TestPluginReceiptRemainsBoundToInputAfterRedirect(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/entry":
			http.Redirect(w, r, "/", http.StatusFound)
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><meta name="generator" content="WordPress 6.4"></html>`)
		case "/wp-content/plugins/fixture/readme.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "=== Fixture ===\nStable tag: 1.0.0\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	opts := pipelineFixture(t, s.URL, []version.VersionRule{{ID: "plugin", Name: "plugin", Component: "wordpress:plugin:fixture", Clauses: []version.VersionClause{{Operator: "=", Version: "1"}}}})
	if err := os.WriteFile(opts.Input, []byte(s.URL+"/entry\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, logs bytes.Buffer
	if code := runVersionPipeline(context.Background(), opts, &out, &logs); code != 0 {
		t.Fatalf("exit %d: %s", code, &logs)
	}
	name, _ := version.ResultBasename(opts.JobID, opts.ShardID)
	data, err := os.ReadFile(filepath.Join(opts.OutputDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var record version.ResultRecordV1
	if json.Unmarshal(bytes.TrimSpace(data), &record) != nil {
		t.Fatal("missing bound record")
	}
	input, _ := version.CanonicalTarget(s.URL + "/entry")
	if record.Target != input {
		t.Fatalf("receipt lost original input locator")
	}
	if !strings.Contains(out.String(), "[FILE:plugins_name.txt] "+s.URL+"/wp-content/plugins/fixture\n") {
		t.Fatal("export lost canonical working URL")
	}
}
