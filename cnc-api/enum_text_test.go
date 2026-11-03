package cnc

import "testing"

func TestEnumDriveMergeKeepsOnlyCleanURLs(t *testing.T) {
	old := []byte("https://example.invalid/  component=wordpress:plugin:fixture  version=1.2  confidence=authoritative\n")
	current := []byte("https://example.invalid/\n")
	if got := string(mergeAndDeduplicateLines(old, current)); got != string(current) {
		t.Fatalf("Drive history reintroduced metadata: %q", got)
	}
	path := "https://example.invalid/wp-content/plugins/fixture"
	if got := cleanEnumTextLine(path + "  plugin=fixture  version=1.2"); got != path {
		t.Fatal(got)
	}
	for _, line := range []string{
		"https://example.invalid/ (user:pass)",
		"ftp://example.invalid/  component=wordpress:plugin:fixture  version=1  confidence=authoritative",
		"https://user:pass@example.invalid/  component=wordpress:plugin:fixture  version=1  confidence=authoritative",
		"https://example.invalid/  component=unrelated  version=1  confidence=authoritative",
		"https://example.invalid/  component=wordpress:plugin:fixture  version=1",
		"plain text result",
	} {
		if got := cleanEnumTextLine(line); got != line {
			t.Fatalf("unrelated output changed: %q", got)
		}
	}
}
