package version

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectorRejectsBlockedMissingAndInvalidPublicEvidence(t *testing.T) {
	valid := "=== Owned fixture ===\nStable tag: 1.2.3\n"
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
		catchAll, truncated     bool
	}{
		{"forbidden", valid, "text/plain", 403, false, false},
		{"blocked HTML", "<html><title>Access denied</title>" + valid + "</html>", "text/html", 200, false, false},
		{"soft404", valid, "text/plain", 200, true, false},
		{"malformed tag", "=== Owned fixture ===\nStable tag: trunk\n", "text/plain", 200, false, false},
		{"oversize", valid + strings.Repeat("x", EvidenceBodyLimit), "text/plain", 200, false, false},
		{"truncated", valid, "text/plain", 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tc.catchAll && r.URL.Path != "/wp-content/plugins/fixture/readme.txt" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", tc.contentType)
				if tc.truncated {
					w.Header().Set("Content-Length", "10000")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer fixture.Close()
			target, _ := CanonicalTarget(fixture.URL)
			observation, err := CollectEvidence(context.Background(), fixture.Client(), target, "wordpress:plugin:fixture")
			if err != nil {
				t.Fatal(err)
			}
			if observation.Version != nil || observation.SelectedEvidence != nil || len(observation.EvidenceCandidates) != 0 || observation.Confidence != "unknown" {
				t.Fatalf("untrusted evidence accepted: %+v", observation)
			}
			rules, _ := NormalizeRules([]VersionRule{{ID: "one", Name: "one", Component: observation.Component, Clauses: []VersionClause{{Operator: "=", Version: "1.2.3"}}}})
			if len(MatchRules(observation, rules)) != 0 {
				t.Fatal("untrusted evidence matched strict rule")
			}
		})
	}
}

func TestCollectorExactCoreFallbackDoesNotUseQueryVersions(t *testing.T) {
	for _, tc := range []struct {
		body  string
		known bool
	}{
		{"/*! WordPress 6.4.1 */\nwindow.wpemoji = {};", true},
		{"window.wpemoji = {}; // asset?ver=6.4.1", false},
	} {
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/wp-includes/js/wp-emoji-release.min.js" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/javascript")
			fmt.Fprint(w, tc.body)
		}))
		target, _ := CanonicalTarget(fixture.URL)
		observation, err := CollectEvidence(context.Background(), fixture.Client(), target, "wordpress:core")
		fixture.Close()
		if err != nil {
			t.Fatal(err)
		}
		if tc.known {
			if observation.Version == nil || *observation.Version != "6.4.1" || observation.Confidence != ConfidenceValidatedFallback || observation.SelectedEvidence.Rank != 40 {
				t.Fatalf("fallback: %+v", observation)
			}
		} else if observation.Version != nil {
			t.Fatal("query hint became a version")
		}
	}
}

func TestCollectorAllJoomlaComponentPlans(t *testing.T) {
	components := []string{"joomla:core", "joomla:component:fixture", "joomla:module:fixture", "joomla:plugin:system:fixture", "joomla:library:fixture", "joomla:template:fixture", "joomla:package:fixture"}
	for _, component := range components {
		t.Run(component, func(t *testing.T) {
			plan, _ := EvidencePlan(component)
			typeName := strings.Split(component, ":")[1]
			name := "fixture"
			if typeName == "core" {
				typeName = "file"
				name = "files_joomla"
			}
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, probe := range plan {
					if r.URL.Path == probe.Path {
						w.Header().Set("Content-Type", "application/xml")
						fmt.Fprintf(w, `<extension type="%s"><name>%s</name><version>v4.2</version></extension>`, typeName, name)
						return
					}
				}
				http.NotFound(w, r)
			}))
			defer fixture.Close()
			target, _ := CanonicalTarget(fixture.URL)
			observation, err := CollectEvidence(context.Background(), fixture.Client(), target, component)
			if err != nil || observation.Version == nil || *observation.Version != "4.2.0" || len(observation.EvidenceCandidates) != len(plan) || observation.SelectedEvidence.Rank != 10 {
				t.Fatalf("%+v %v", observation, err)
			}
		})
	}
}
