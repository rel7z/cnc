package scanner

import (
	"fmt"
	"net/url"
	"strings"
)

type URLEntry struct {
	Raw        string
	IsAbsolute bool
	FixedURL   string
	Path       string
}

func ParseURLEntry(line string) URLEntry {
	line = strings.TrimSpace(line)
	e := URLEntry{Raw: line}

	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		if u, err := url.Parse(line); err == nil && u.Host != "" {
			e.IsAbsolute = true
			e.FixedURL = line
			e.Path = u.Path
			return e
		}
	}

	if !strings.HasPrefix(line, "/") {
		line = "/" + line
	}
	e.Path = line
	return e
}

func BuildURL(domain string, entry URLEntry) string {
	if entry.IsAbsolute {
		return entry.FixedURL
	}
	domain = strings.TrimRight(domain, "/")
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	return fmt.Sprintf("%s%s", domain, entry.Path)
}
