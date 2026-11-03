package version

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOptionalScopeRequestsAndRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "owned fixture") }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	guard, err := NewScopeGuard(TargetScope{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := guard.Client(time.Second).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "owned fixture" {
		t.Fatal(string(body))
	}
	if _, _, err := guard.Authorize(context.Background(), "file:///tmp/fixture"); err == nil {
		t.Fatal("non-HTTP target accepted")
	}
	if _, err := NormalizeScope(TargetScope{Ports: []int{80}}); err == nil {
		t.Fatal("partial explicit scope accepted")
	}
	artifact := ArtifactV1{SchemaVersion: 1, JobID: "optional-scope"}
	data, digest, err := ArtifactBytes(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path, err := WriteImmutable(t.TempDir(), "artifact.json", data)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadArtifact(path, digest, artifact.JobID)
	if err != nil || !loaded.TargetScope.Unrestricted() {
		t.Fatalf("optional scope artifact: %+v %v", loaded, err)
	}
}

func TestUnrestrictedScopeFollowsRedirectToDifferentHost(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "second host")
	}))
	defer destination.Close()
	destinationURL, _ := url.Parse(destination.URL)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://second.test:"+destinationURL.Port()+"/", http.StatusFound)
	}))
	defer origin.Close()
	originURL, _ := url.Parse(origin.URL)
	guard, err := NewScopeGuard(TargetScope{})
	if err != nil {
		t.Fatal(err)
	}
	guard.Resolve = func(_ context.Context, host string) ([]net.IP, error) {
		if host != "first.test" && host != "second.test" {
			t.Fatalf("unexpected redirect host %q", host)
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	response, err := guard.Client(time.Second).Get("http://first.test:" + originURL.Port() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(response.Request.URL.Host, "second.test") || string(body) != "second host" {
		t.Fatalf("redirect result: %s %q", response.Request.URL, body)
	}
}
