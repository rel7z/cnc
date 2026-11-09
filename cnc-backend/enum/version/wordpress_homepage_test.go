package version

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestWordPressHomepageOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		status, calls int
	}{
		{"blocked", "http_403", http.StatusForbidden, 1},
		{"rate limited", "http_429", http.StatusTooManyRequests, 1},
		{"redirect refused", "http_302", http.StatusFound, 1},
		{"dns alias recovery", WordPressVerified, 0, 2},
		{"marker absent", WordPressNoMarker, http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				status, body := tc.status, "<html>Example</html>"
				if tc.status == 0 {
					if req.URL.Host == "example.test" {
						return nil, &net.DNSError{Err: "fixture DNS failure"}
					}
					status, body = http.StatusOK, `<link href="/wp-content/themes/example/style.css">`
				}
				return &http.Response{Request: req, StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
					Header: http.Header{"Content-Type": {"text/html"}}}, nil
			})}
			_, outcome := VerifyWordPressHomepage(context.Background(), client, "example.test")
			if calls != tc.calls || outcome != tc.outcome {
				t.Fatalf("calls=%d outcome=%s", calls, outcome)
			}
		})
	}
}

func TestWordPressHomepageEvidenceStopsAfterSplitMarker(t *testing.T) {
	prefix := strings.Repeat(" ", 8192+32-8)
	marker := "/WP-CONTENT/THEMES/example"
	body := strings.NewReader(prefix + marker + strings.Repeat("x", WordPressHomepageLimit))
	response := &http.Response{Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(body)}
	found, err := wordpressHomepageEvidence(response)
	if err != nil || !found {
		t.Fatalf("split marker was not recognized: %v %v", found, err)
	}
	if bytesRead := int(body.Size()) - body.Len(); bytesRead > 17<<10 {
		t.Fatalf("read %d bytes despite an early marker", bytesRead)
	}

	body.Reset(strings.Repeat("x", WordPressHomepageLimit) + marker)
	found, err = wordpressHomepageEvidence(response)
	if err != nil || found || int(body.Size())-body.Len() != WordPressHomepageLimit {
		t.Fatalf("unexpected outcome beyond the homepage limit: %v %v", found, err)
	}
}
