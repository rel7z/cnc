package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const loginPage = `<title>Admin Login</title><form><input type="password"></form>`

func TestLauncherJoomlaWordlistPaths(t *testing.T) {
	for entry, want := range map[string]string{
		"com_gridbox":           "https://example.test/administrator/components/com_gridbox/gridbox.xml",
		"lib_astroid":           "https://example.test/libraries/astroid/astroid.xml",
		"plugin:system:astroid": "https://example.test/plugins/system/astroid/astroid.xml",
	} {
		got := manifestURLs("example.test", entry)
		if len(got) == 0 || got[0] != want {
			t.Fatalf("%s: %v", entry, got)
		}
	}
}

func TestAdminMissingPageFiltering(t *testing.T) {
	for _, kind := range []string{"real", "catch-all", "reflected-path", "redirect-login", "403", "404", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			client := newHTTPClient(0)
			client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
				body, code := loginPage, 200
				header := make(http.Header)
				switch kind {
				case "real":
					if r.URL.Path != "/admin" {
						body, code = "missing", 404
					}
				case "reflected-path":
					body += " Path: " + r.URL.Path
				case "redirect-login":
					if r.URL.Path != "/login" {
						code = 302
						header.Set("Location", "/login")
					}
				case "403":
					code = 403
				case "404":
					code = 404
				case "truncated":
					body += strings.Repeat("x", 128*1024)
				}
				return &http.Response{StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			baseline, err := fetchAdminResponse(client, "https://example.test/missing")
			if kind == "truncated" {
				if err == nil {
					t.Fatal("accepted oversized response")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, hit := probeAdminPath(client, "https://example.test", "/admin", baseline)
			if hit != (kind == "real") {
				t.Fatalf("hit=%v", hit)
			}
		})
	}
}

func TestCMSForbiddenIsNotDetected(t *testing.T) {
	client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("Forbidden")), Request: r}, nil
	})}
	if _, hit := probeWPPlugin(client, "example.test", "akismet"); hit {
		t.Fatal("403 confirmed as plugin")
	}
	if _, hit := probeJoomlaExt(client, "example.test", "com_content"); hit {
		t.Fatal("403 confirmed as extension")
	}
}

func TestResultWriterFlushesEachLine(t *testing.T) {
	var output bytes.Buffer
	w := &resultWriter{buf: bufio.NewWriter(&output)}
	w.writeLine("[FILE:enum-admin.txt] https://example.test/admin")
	if got := output.String(); got != fmt.Sprintln("[FILE:enum-admin.txt] https://example.test/admin") {
		t.Fatalf("not flushed: %q", got)
	}
}

func TestClassifyHTTPOnlyCMS(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = testTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme == "https" {
			return nil, fmt.Errorf("HTTP-only fixture")
		}
		body := "wp-content wp-includes wp-json"
		if r.URL.Path == "/readme.html" {
			body = "missing"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	wp, joomla, unknown := make(chan string, 1), make(chan string, 1), make(chan string, 1)
	classifyTargets([]string{"example.test"}, 1, 0, wp, joomla, unknown)
	if len(wp) != 1 || len(joomla) != 0 || len(unknown) != 0 {
		t.Fatal("HTTP-only WordPress site was misclassified")
	}
}
