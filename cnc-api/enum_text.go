package cnc

import (
	"net/url"
	"strings"
)

// Normalize old enum exports when merging Drive history with current URL-only
// output. Only recognize the two exact metadata layouts emitted by enum.
func cleanEnumTextLine(line string) string {
	for _, marker := range []string{"  component=", "  plugin="} {
		prefix, suffix, found := strings.Cut(line, marker)
		if !found {
			continue
		}
		u, err := url.Parse(prefix)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return line
		}
		fields := strings.Fields(strings.TrimPrefix(marker, "  ") + suffix)
		if marker == "  component=" {
			if len(fields) != 3 || !strings.HasPrefix(fields[1], "version=") || !strings.HasPrefix(fields[2], "confidence=") ||
				!(strings.HasPrefix(fields[0], "component=wordpress:plugin:") || strings.HasPrefix(fields[0], "component=joomla:")) {
				return line
			}
		} else if len(fields) != 2 || !strings.HasPrefix(fields[1], "version=") || !strings.Contains(u.Path, "/wp-content/plugins/") {
			return line
		}
		return prefix
	}
	return line
}
