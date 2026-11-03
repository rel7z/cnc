package version

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSemVerPrecedenceAndLargeNumbers(t *testing.T) {
	ordered := []string{"0.0.0-0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "999999999999999999999999999999.0.0", "1000000000000000000000000000000.0.0"}
	for i, x := range ordered {
		a, _ := ParseVersion(x)
		for j, y := range ordered {
			b, _ := ParseVersion(y)
			n := Compare(a, b)
			if (i < j && n >= 0) || (i == j && n != 0) || (i > j && n <= 0) {
				t.Fatalf("%s vs %s: %d", x, y, n)
			}
		}
	}
}

func TestFourPartAndLetterSuffixBoundaries(t *testing.T) {
	for _, ordered := range [][]string{
		{"3.1.8", "3.1.8.1", "3.1.8.9", "3.1.8.10", "3.1.9"},
		{"2.6.8-alpha", "2.6.8", "2.6.8a", "2.6.8b", "2.6.8c", "2.6.9"},
	} {
		for i := 1; i < len(ordered); i++ {
			a, err := ParseVersion(ordered[i-1]); if err != nil { t.Fatal(err) }
			b, err := ParseVersion(ordered[i]); if err != nil { t.Fatal(err) }
			if Compare(a,b) >= 0 { t.Fatalf("%s should sort below %s", ordered[i-1],ordered[i]) }
		}
	}
	for _, test := range []struct{ observed,bound string; match bool }{
		{"3.1.8.9","3.1.8.10",true}, {"3.1.8.10","3.1.8.10",false},
		{"2.6.8b","2.6.8c",true}, {"2.6.8c","2.6.8c",false},
	} {
		observed,err := ParseVersion(test.observed); if err != nil { t.Fatal(err) }
		if got := clauseMatches(observed,VersionClause{Operator:"<",Version:test.bound}); got != test.match { t.Fatalf("%s < %s = %v",test.observed,test.bound,got) }
	}
}

func TestRuleNormalizationRejectsImpossibleAndDuplicateSets(t *testing.T) {
	rule := func(id string, clauses ...VersionClause) VersionRule {
		return VersionRule{ID: id, Name: id, Component: "wordpress:core", Clauses: clauses}
	}
	for _, clauses := range [][]VersionClause{
		{{Operator: ">=", Version: "6.5"}, {Operator: "<", Version: "6.5.0"}},
		{{Operator: "=", Version: "6.5"}, {Operator: ">", Version: "6.6"}},
		{{Operator: "<", Version: "0.0.0-0"}},
		{{Operator: ">", Version: "1.0.0-alpha"}, {Operator: "<", Version: "1.0.0-alpha.0"}},
		{{Operator: ">", Version: "1.0.0.9"}, {Operator: "<", Version: "1.0.0.10-0"}},
		{{Operator: "~", Version: "1"}},
	} {
		if _, err := NormalizeRules([]VersionRule{rule("one", clauses...)}); err == nil {
			t.Fatalf("accepted %v", clauses)
		}
	}
	if _, err := NormalizeRules([]VersionRule{rule("a", VersionClause{"=", "v6.5"}), rule("b", VersionClause{"=", "6.5.0"})}); err == nil {
		t.Fatal("accepted normalized duplicate")
	}
	rules, err := NormalizeRules([]VersionRule{rule("z", VersionClause{">=", "6"}, VersionClause{"<", "7"}), rule("a", VersionClause{"=", "v6.5"})})
	if err != nil {
		t.Fatal(err)
	}
	c := candidate("wp_generator", 20, "6.5.0", "wp_generator")
	o, err := ResolveEvidence("wordpress:core", []EvidenceCandidate{c})
	if err != nil {
		t.Fatal(err)
	}
	if got := MatchRules(o, rules); len(got) != 2 || got[0].ID != "a" || got[1].ID != "z" {
		t.Fatalf("matches %v", got)
	}
	o.Confidence = ConfidenceConflicted
	if len(MatchRules(o, rules)) != 0 {
		t.Fatal("conflict matched")
	}
	for _, c := range []string{"wordpress:plugin:MyPlugin", "joomla:component:com_test", "joomla:plugin:system", "joomla:module:mod_test", "drupal:core"} {
		if ValidateComponent(c) == nil {
			t.Fatalf("accepted %s", c)
		}
	}
}

func TestCanonicalTargetPrivacyAndEscapes(t *testing.T) {
	got, err := CanonicalTarget("https://user:secret@BÜCHER.example:8443/a/%2e%2e/b/%7e/%2f?q=secret#private")
	want := TargetLocator{"https", "xn--bcher-kva.example", 8443, "/b/~/%2F"}
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
	for _, raw := range []string{"https://*.example/", "https://example.test./", "https://example.test/a;b", "https://example.test/a%3Bb", "https://example.test/%QQ", "ftp://example.test/"} {
		if _, err := CanonicalTarget(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if got, _ := CanonicalTarget("http://example.test/a//b/./c/.."); got.Path != "/a//b/" {
		t.Fatal(got)
	}
}

func TestScopeMixedAnswersAndRebinding(t *testing.T) {
	g, err := NewScopeGuard(TargetScope{Hosts: []string{"owned.test"}, CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{80}})
	if err != nil {
		t.Fatal(err)
	}
	g.Resolve = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")}, nil
	}
	if _, _, err := g.Authorize(context.Background(), "http://owned.test"); err == nil {
		t.Fatal("mixed answer escaped both authorization branches")
	}
	g.Resolve = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("8.8.8.8")}, nil }
	if _, _, err := g.Authorize(context.Background(), "http://owned.test"); err != nil {
		t.Fatal(err)
	}
	g.Scope.CIDRs = nil
	g.Resolve = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }
	if _, _, err := g.Authorize(context.Background(), "http://owned.test"); err == nil {
		t.Fatal("rebinding to loopback accepted")
	}
	if _, err := NormalizeScope(TargetScope{CIDRs: []string{"10.0.0.1/24"}, Schemes: []string{"http"}, Ports: []int{80}}); err == nil {
		t.Fatal("noncanonical CIDR accepted")
	}
}

func TestGuardPinsDNSAndStopsOutOfScopeRedirect(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Host, "owned.test:") {
			t.Error(r.Host)
		}
		http.Redirect(w, r, "http://outside.test"+r.URL.Path, http.StatusFound)
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	var n int
	fmt.Sscan(port, &n)
	g, _ := NewScopeGuard(TargetScope{Hosts: []string{"owned.test"}, Schemes: []string{"http"}, Ports: []int{n, 80}})
	var resolves atomic.Int32
	g.Resolve = func(context.Context, string) ([]net.IP, error) {
		resolves.Add(1)
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	g.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:"+port {
			t.Errorf("unpinned dial %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	if _, err := g.Client(time.Second).Get("http://owned.test:" + port + "/redirect"); err == nil {
		t.Fatal("outside redirect followed")
	}
	if calls.Load() != 1 || resolves.Load() != 2 {
		t.Fatalf("requests %d resolutions %d", calls.Load(), resolves.Load())
	}
}

func TestArtifactsRejectTamperingAndNeverOverwrite(t *testing.T) {
	a := ArtifactV1{SchemaVersion: 1, JobID: "job-1", TargetScope: TargetScope{CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{80}}, Rules: []VersionRule{}}
	data, digest, err := ArtifactBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := WriteImmutable(dir, "job-1-"+digest+".json", data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArtifact(p, digest, "job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArtifact(p, digest, "job-2"); err == nil {
		t.Fatal("wrong job accepted")
	}
	if _, err := WriteImmutable(dir, filepath.Base(p), []byte("bad")); err == nil {
		t.Fatal("overwritten artifact")
	}
	if err := os.WriteFile(p, append(data, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArtifact(p, digest, "job-1"); err == nil {
		t.Fatal("tampered artifact accepted")
	}
	if _, err := ResultBasename("../escape", "task"); err == nil {
		t.Fatal("unsafe filename")
	}
}

func candidate(source string, rank int, value, kind string) EvidenceCandidate {
	return EvidenceCandidate{Source: source, Rank: rank, Version: value, RetrievedAt: time.Now().UTC(), Validation: EvidenceValidation{Kind: kind, ContentType: "text/plain", Bytes: 20}}
}
func TestEvidenceAgreementAndConflict(t *testing.T) {
	a, b := candidate("wp_generator", 20, "v6.4", "wp_generator"), candidate("wp_rest_metadata", 10, "6.4.0", "wp_rest")
	o, err := ResolveEvidence("wordpress:core", []EvidenceCandidate{a, b})
	if err != nil || o.Version == nil || *o.Version != "6.4.0" || o.SelectedEvidence.Rank != 10 || len(o.EvidenceCandidates) != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	b.Version = "6.5"
	o, err = ResolveEvidence("wordpress:core", []EvidenceCandidate{a, b})
	if err != nil || o.Confidence != ConfidenceConflicted || o.Version != nil || o.SelectedEvidence != nil || len(o.EvidenceCandidates) != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	a.Source = "https://user:password@private.test"
	if _, err = ResolveEvidence("wordpress:core", []EvidenceCandidate{a}); err == nil {
		t.Fatal("raw URL evidence source accepted")
	}
}

func TestManifestProbePathsAndValidation(t *testing.T) {
	want := map[string][]string{
		"joomla:core":              {"/administrator/manifests/files/joomla.xml"},
		"joomla:component:foo":     {"/administrator/components/com_foo/foo.xml", "/administrator/components/com_foo/com_foo.xml", "/components/com_foo/foo.xml"},
		"joomla:module:foo":        {"/administrator/modules/mod_foo/foo.xml", "/modules/mod_foo/foo.xml"},
		"joomla:plugin:system:foo": {"/plugins/system/foo/foo.xml"},
		"joomla:library:foo":       {"/libraries/foo/foo.xml"},
		"joomla:template:foo":      {"/templates/foo/templateDetails.xml", "/administrator/templates/foo/templateDetails.xml"},
		"joomla:package:foo":       {"/administrator/manifests/packages/pkg_foo.xml", "/administrator/manifests/packages/foo.xml"},
	}
	for component, paths := range want {
		plan, err := EvidencePlan(component)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, p := range plan {
			got = append(got, p.Path)
		}
		if !reflect.DeepEqual(got, paths) {
			t.Fatalf("%s %v", component, got)
		}
	}
	plan, _ := EvidencePlan("joomla:component:foo")
	for _, body := range []string{`<html><version>1.2</version></html>`, `<extension type="module"><version>1.2</version></extension>`, `<extension type="component"><version>1.2</version><version>2.0</version></extension>`, `<extension type="component"><version>trunk</version></extension>`} {
		if _, err := EvidenceVersion("joomla:component:foo", plan[0], []byte(body), "application/xml"); err == nil {
			t.Fatal(body)
		}
	}
	if _, err := EvidenceVersion("joomla:component:foo", plan[0], []byte(`<extension type="component"><version>1.2</version></extension>`), "application/xml"); err == nil {
		t.Fatal("manifest without component identity accepted")
	}
	if v, err := EvidenceVersion("joomla:component:foo", plan[0], []byte(`<extension type="component"><name>com_foo</name><version>1.2</version></extension>`), "application/xml"); err != nil || v != "1.2.0" {
		t.Fatalf("%s %v", v, err)
	}
}

func waitQueued(t *testing.T, b *RequestBudget, job string, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for b.Snapshot()[job].Queued != count {
		if time.Now().After(deadline) {
			t.Fatal(b.Snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}
func TestBudgetFairnessAndCancellation(t *testing.T) {
	b, _ := NewRequestBudget(1)
	release, _ := b.Acquire(context.Background(), "hold")
	order := make(chan string, 3)
	done := make(chan struct{})
	counts := map[string]int{}
	for _, job := range []string{"a", "a", "b"} {
		counts[job]++
		go func(job string) {
			r, e := b.Acquire(context.Background(), job)
			if e != nil {
				return
			}
			order <- job
			<-done
			r()
		}(job)
		waitQueued(t, b, job, counts[job])
	}
	release()
	if got := <-order; got != "a" {
		t.Fatal(got)
	}
	close(done)
	if got := <-order; got != "b" {
		t.Fatalf("job starved: %s", got)
	}
	if got := <-order; got != "a" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := b.Acquire(context.Background(), "hold")
	finished := make(chan error, 1)
	go func() { _, e := b.Acquire(ctx, "cancel"); finished <- e }()
	waitQueued(t, b, "cancel", 1)
	cancel()
	if !errors.Is(<-finished, context.Canceled) {
		t.Fatal("not canceled")
	}
	r()
	if m := b.Snapshot()["cancel"]; m.Queued != 0 || m.Active != 0 || m.Cancelled != 1 {
		t.Fatal(m)
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBudgetHoldsPermitUntilBodyCloseAndCancelsActive(t *testing.T) {
	b, _ := NewRequestBudget(1)
	transport := &BudgetTransport{Budget: b, JobID: "a", Base: fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://fixture.test", nil)
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(response.Body)
	if b.Snapshot()["a"].Active != 1 {
		t.Fatal("permit released before body closed")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for b.Snapshot()["a"].Active != 0 {
		if time.Now().After(deadline) {
			t.Fatal("active request cancellation leaked")
		}
		time.Sleep(time.Millisecond)
	}
	response.Body.Close()
	if b.Snapshot()["a"].Cancelled != 1 {
		t.Fatal(b.Snapshot())
	}
}
