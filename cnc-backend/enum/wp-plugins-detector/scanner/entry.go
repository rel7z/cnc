package scanner

import "strings"

// PluginEntry represents one line from plugins.txt.
// If TargetVersion is non-empty the user wants a version-filtered output file.
type PluginEntry struct {
	Slug          string
	TargetVersion string // empty → no version filter
}

// ParsePluginEntry parses a single line from plugins.txt.
//
//	"elementor"        → {Slug:"elementor", TargetVersion:""}
//	"elementor:3.24.0" → {Slug:"elementor", TargetVersion:"3.24.0"}
func ParsePluginEntry(line string) PluginEntry {
	line = strings.TrimSpace(line)
	if idx := strings.IndexByte(line, ':'); idx != -1 {
		slug := strings.TrimSpace(line[:idx])
		ver := strings.TrimSpace(line[idx+1:])
		return PluginEntry{Slug: slug, TargetVersion: ver}
	}
	return PluginEntry{Slug: line}
}

// Slugs returns deduplicated slug strings from a slice of entries.
// If two entries share the same slug (e.g. "elementor" and "elementor:3.24.0"),
// the slug is only scanned once — the version filter is applied at write time.
func Slugs(entries []PluginEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	var out []string
	for _, e := range entries {
		if _, ok := seen[e.Slug]; !ok {
			seen[e.Slug] = struct{}{}
			out = append(out, e.Slug)
		}
	}
	return out
}
