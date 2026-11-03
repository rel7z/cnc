package version

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPHPReportedHeaderEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		headers   []string
		body      string
		status    int
		want      string
		truncated bool
	}{
		{name: "normal", headers: []string{"PHP/8.5.11"}, want: "8.5.11"},
		{name: "multiple products", headers: []string{"Express, PHP/8.4.26"}, want: "8.4.26"},
		{name: "same duplicate", headers: []string{"PHP/8.3.35", "PHP/8.3.35"}, want: "8.3.35"},
		{name: "empty body", headers: []string{"PHP/8.2.34"}, want: "8.2.34"},
		{name: "hidden", body: "PHP/8.5.1"},
		{name: "cookie only", body: "PHPSESSID"},
		{name: "ambiguous", headers: []string{"PHP/8.5.1", "PHP/8.4.1"}},
		{name: "ambiguous single header", headers: []string{"PHP/8.5.1, PHP/8.4.1"}},
		{name: "invalid", headers: []string{"PHP/8.5"}},
		{name: "distribution suffix", headers: []string{"PHP/8.3.6-0ubuntu0.24.04.1"}},
		{name: "bad version", headers: []string{"PHP/nope"}},
		{name: "not PHP", headers: []string{"NotPHP/8.5.1"}},
		{name: "forbidden", headers: []string{"PHP/8.5.1"}, status: 403},
		{name: "server error", headers: []string{"PHP/8.5.1"}, status: 503},
		{name: "blocked", headers: []string{"PHP/8.5.1"}, body: "<html><title>Access denied</title></html>"},
		{name: "truncated", headers: []string{"PHP/8.5.1"}, body: "short", truncated: true},
		{name: "oversized", headers: []string{"PHP/8.5.1"}, body: strings.Repeat("x", EvidenceBodyLimit+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/app/index.php" {
					t.Errorf("unexpected PHP probe %s", r.URL.Path)
				}
				for _, h := range tc.headers {
					w.Header().Add("X-Powered-By", h)
				}
				w.Header().Set("Content-Type", "text/plain")
				if tc.truncated {
					w.Header().Set("Content-Length", "1000")
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			target, _ := CanonicalTarget(server.URL + "/app/index.php")
			got, _ := CollectEvidence(context.Background(), server.Client(), target, "php:php-src")
			if tc.want == "" {
				if got.Version != nil || len(got.EvidenceCandidates) != 0 {
					t.Fatalf("untrusted PHP accepted: %+v", got)
				}
				return
			}
			if got.Version == nil || *got.Version != tc.want || got.Confidence != ConfidenceReported || got.SelectedEvidence.Source != "php_powered_by" {
				t.Fatalf("unexpected PHP observation: %+v", got)
			}
		})
	}
}
