package version

import "testing"

func TestWordPressFeedEvidenceIdentity(t *testing.T) {
	probe := EvidenceProbe{Path: "/feed/", Source: "wp_feed_generator", Kind: "wp_feed", Rank: 25}
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"standard", `<rss version="2.0"><channel><generator>https://wordpress.org/?v=6.8.1</generator></channel></rss>`, true},
		{"legacy", `<rss><channel><generator>http://wordpress.org/?v=6.8.1</generator></channel></rss>`, true},
		{"wrong product", `<rss><channel><generator>https://example.org/?v=6.8.1</generator></channel></rss>`, false},
		{"duplicate", `<rss><channel><generator>https://wordpress.org/?v=6.8.1</generator><generator>https://wordpress.org/?v=6.8.2</generator></channel></rss>`, false},
		{"nested item", `<rss><channel><item><generator>https://wordpress.org/?v=6.8.1</generator></item></channel></rss>`, false},
		{"duplicate query", `<rss><channel><generator>https://wordpress.org/?v=6.8.1&amp;v=6.8.2</generator></channel></rss>`, false},
		{"malformed", `<rss><channel><generator>https://wordpress.org/?v=6.8.1</generator>`, false},
		{"hidden", `<rss><channel><generator>https://wordpress.org/</generator></channel></rss>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvidenceVersion("wordpress:core", probe, []byte(tc.body), "application/rss+xml")
			if tc.valid {
				if err != nil || got != "6.8.1" {
					t.Fatalf("got %q, %v", got, err)
				}
			} else if err == nil {
				t.Fatalf("accepted %q", got)
			}
		})
	}
}
