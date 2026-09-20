package scanner

import "strings"

// ExtType classifies what kind of Joomla extension we're probing.
type ExtType int

const (
	// ExtComponent — com_<slug>  (the most common kind)
	ExtComponent ExtType = iota
	// ExtPlugin   — a system/content/etc. plugin under /plugins/<group>/<slug>/
	ExtPlugin
	// ExtModule   — a module under /modules/mod_<slug>/
	ExtModule
	// ExtTemplate — a template under /templates/<slug>/
	ExtTemplate
	// ExtLibrary  — a library under /libraries/<slug>/  (e.g. astroid)
	ExtLibrary
)

// PluginEntry represents one line from plugins.txt.
//
// Supported line formats:
//
//	com_gridbox                     → component, any version
//	com_gridbox:1.0.2               → component, version filter
//	plugin:system:astroid           → system plugin, any version
//	plugin:system:astroid:2.0.0     → system plugin, version filter
//	mod_menu                        → module, any version
//	tpl_cassiopeia                  → template, any version
//	lib_astroid                     → library, any version  (e.g. Astroid Framework)
type PluginEntry struct {
	Raw           string  // original trimmed line
	Slug          string  // bare identifier (e.g. "gridbox", "astroid", "menu")
	Prefix        string  // "com_", "mod_", "tpl_", or ""
	Group         string  // plugin group (e.g. "system") — only for ExtPlugin
	TargetVersion string  // empty → no version filter
	Type          ExtType // derived from the line format
}

// ParsePluginEntry parses a single plugins.txt line into a PluginEntry.
func ParsePluginEntry(line string) PluginEntry {
	line = strings.TrimSpace(line)
	e := PluginEntry{Raw: line}

	// plugin:<group>:<slug>[:<version>]
	if strings.HasPrefix(line, "plugin:") {
		parts := strings.SplitN(line[len("plugin:"):], ":", 3)
		e.Type = ExtPlugin
		if len(parts) >= 1 {
			e.Group = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			e.Slug = strings.TrimSpace(parts[1])
		}
		if len(parts) >= 3 {
			e.TargetVersion = strings.TrimSpace(parts[2])
		}
		e.Prefix = ""
		return e
	}

	// lib_<slug>[:<version>]
	if strings.HasPrefix(line, "lib_") {
		e.Type = ExtLibrary
		e.Prefix = "lib_"
		slug, ver := splitSlugVer(strings.TrimPrefix(line, "lib_"))
		e.Slug = slug
		e.TargetVersion = ver
		return e
	}

	// tpl_<slug>[:<version>]
	if strings.HasPrefix(line, "tpl_") {
		e.Type = ExtTemplate
		e.Prefix = "tpl_"
		slug, ver := splitSlugVer(strings.TrimPrefix(line, "tpl_"))
		e.Slug = slug
		e.TargetVersion = ver
		return e
	}

	// mod_<slug>[:<version>]
	if strings.HasPrefix(line, "mod_") {
		e.Type = ExtModule
		e.Prefix = "mod_"
		slug, ver := splitSlugVer(strings.TrimPrefix(line, "mod_"))
		e.Slug = slug
		e.TargetVersion = ver
		return e
	}

	// com_<slug>[:<version>]  (default)
	slug := line
	if strings.HasPrefix(line, "com_") {
		slug = strings.TrimPrefix(line, "com_")
	}
	e.Type = ExtComponent
	e.Prefix = "com_"
	s, ver := splitSlugVer(slug)
	e.Slug = s
	e.TargetVersion = ver
	return e
}

// Key returns a stable string key for de-duplication (ignores version).
func (e PluginEntry) Key() string {
	switch e.Type {
	case ExtPlugin:
		return "plugin:" + e.Group + ":" + e.Slug
	case ExtModule:
		return "mod_" + e.Slug
	case ExtTemplate:
		return "tpl_" + e.Slug
	case ExtLibrary:
		return "lib_" + e.Slug
	default:
		return "com_" + e.Slug
	}
}

// DisplayName returns the human-readable name used in output files and summary.
func (e PluginEntry) DisplayName() string {
	return e.Key()
}

// Entries deduplicates a slice, preferring entries with a TargetVersion.
func Dedup(entries []PluginEntry) []PluginEntry {
	seen := make(map[string]PluginEntry, len(entries))
	for _, e := range entries {
		k := e.Key()
		existing, ok := seen[k]
		if !ok || (e.TargetVersion != "" && existing.TargetVersion == "") {
			seen[k] = e
		}
	}
	out := make([]PluginEntry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	return out
}

// Keys returns unique Key() strings in insertion order for the worker pool.
func Keys(entries []PluginEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	var out []string
	for _, e := range entries {
		k := e.Key()
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

func splitSlugVer(s string) (slug, ver string) {
	if idx := strings.IndexByte(s, ':'); idx != -1 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
	}
	return strings.TrimSpace(s), ""
}
