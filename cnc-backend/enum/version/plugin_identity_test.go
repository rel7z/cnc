package version

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPluginReadmeIdentity(t *testing.T) {
	for _, tc := range []struct {
		slug, title string
		ok          bool
	}{
		{"fixture", "Owned fixture", true},
		{"fixture", "Completely Different Plugin", false},
		{"fixture", "notfixture", false},
		{"contact-form-7", "Contact Form 7", true},
		{"wordpress-seo", "Yoast SEO", true},
		{"elementor-pro", "Elementor", false},
		{"elementor-pro", "Elementor Pro – Website Builder", true},
	} {
		probe, _ := EvidencePlan("wordpress:plugin:" + tc.slug)
		_, err := EvidenceVersion("wordpress:plugin:"+tc.slug, probe[0], []byte("=== "+tc.title+" ===\nStable tag: 1.2.3\n"), "text/plain")
		if (err == nil) != tc.ok {
			t.Errorf("%s / %s: %v", tc.slug, tc.title, err)
		}
	}
}

func TestPluginCollectorRejectsRedirectToOtherPlugin(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-content/plugins/fixture/readme.txt":
			http.Redirect(w, r, "/wp-content/plugins/other/readme.txt", http.StatusFound)
		case "/wp-content/plugins/other/readme.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "=== Fixture ===\nStable tag: 1.0.0\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	target, _ := CanonicalTarget(s.URL)
	observation, err := CollectEvidence(context.Background(), s.Client(), target, "wordpress:plugin:fixture")
	if err != nil || observation.Version != nil || len(observation.EvidenceCandidates) != 0 {
		t.Fatalf("redirect accepted: %+v %v", observation, err)
	}
}

func TestJoomlaManifestIdentity(t *testing.T) {
	for _, tc := range []struct {
		component, name string
		ok              bool
	}{
		{"joomla:plugin:system:fixture", "PLG_SYSTEM_FIXTURE", true},
		{"joomla:plugin:system:fixture", "fixture", true},
		{"joomla:component:fixture", "COM_FIXTURE", true},
		{"joomla:plugin:system:fixture", "other", false},
	} {
		plan, _ := EvidencePlan(tc.component)
		typeName := "plugin"
		if tc.component == "joomla:component:fixture" {
			typeName = "component"
		}
		body := fmt.Sprintf(`<extension type="%s"><name>%s</name><version>1.0</version></extension>`, typeName, tc.name)
		_, err := EvidenceVersion(tc.component, plan[0], []byte(body), "application/xml")
		if (err == nil) != tc.ok {
			t.Errorf("%s / %s: %v", tc.component, tc.name, err)
		}
	}
}

func TestTranslatePressPublishedDisplayName(t *testing.T) {
	probe := EvidenceProbe{Kind: "wp_plugin_readme"}
	for _, tc := range []struct {
		title string
		valid bool
	}{{"TranslatePress - Translate Multilingual sites with AI Translation", true}, {"TranslatePress – Multilingual", true}, {"Multilingual Translator", false}, {"Other Plugin", false}} {
		body := []byte("=== " + tc.title + " ===\nStable tag: 3.3.1\n")
		got, err := EvidenceVersion("wordpress:plugin:translatepress-multilingual", probe, body, "text/plain")
		if tc.valid && (err != nil || got != "3.3.1") {
			t.Fatalf("published title rejected: %q %v", tc.title, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("unrelated readme accepted: %q", tc.title)
		}
	}
}
