package scanner

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Status int

const (
	StatusConfirmed Status = iota
	StatusProtected
	StatusNotFound
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusConfirmed:
		return "CONFIRMED"
	case StatusProtected:
		return "PROTECTED"
	case StatusNotFound:
		return "not-found"
	default:
		return "error"
	}
}

type Result struct {
	Domain          string
	Path            string
	FullURL         string
	StatusCode      int
	Status          Status
	MatchedKeywords []string
	MatchedTitles   []string
	BodySize        int
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		},
	}
}

const readLimit = 128 * 1024

func ProbeURL(client *http.Client, fullURL string, keywords []string, titles []string, allTrue bool) Result {
	result := Result{FullURL: fullURL}

	if u, err := parseURLParts(fullURL); err == nil {
		result.Domain = u.host
		result.Path = u.path
	} else {
		result.Domain = fullURL
		result.Path = "/"
	}

	candidates := []string{fullURL}
	if strings.HasPrefix(fullURL, "https://") {
		candidates = append(candidates, "http://"+strings.TrimPrefix(fullURL, "https://"))
	}

	for _, u := range candidates {
		r, err := doProbe(client, u, result, keywords, titles, allTrue)
		if err != nil {
			continue
		}
		return r
	}

	result.Status = StatusError
	return result
}

func doProbe(client *http.Client, url string, base Result, keywords []string, titles []string, allTrue bool) (Result, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; admin-enum/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	r := base
	r.FullURL = url
	r.StatusCode = resp.StatusCode

	switch resp.StatusCode {
	case http.StatusOK:
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, readLimit))
		if readErr != nil {
			r.Status = StatusError
			return r, nil
		}
		r.BodySize = len(body)

		matchedKW := matchKeywords(body, keywords)
		matchedTitle := matchTitles(body, titles)

		wantKW := len(keywords) > 0
		wantTitle := len(titles) > 0

		confirmed := false
		if allTrue {
			allKW := !wantKW || len(matchedKW) == len(keywords)
			allTitle := !wantTitle || len(matchedTitle) == len(titles)
			confirmed = (wantKW || wantTitle) && allKW && allTitle
		} else {
			hasKW := len(matchedKW) > 0
			hasTitle := len(matchedTitle) > 0
			if wantKW && wantTitle {
				confirmed = hasKW && hasTitle
			} else if wantKW {
				confirmed = hasKW
			} else if wantTitle {
				confirmed = hasTitle
			}
		}

		if confirmed {
			r.Status = StatusConfirmed
			r.MatchedKeywords = matchedKW
			r.MatchedTitles = matchedTitle
		} else {
			r.Status = StatusNotFound
		}

	case http.StatusForbidden:
		r.Status = StatusProtected

	default:
		r.Status = StatusNotFound
	}

	return r, nil
}

func matchKeywords(body []byte, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	lower := strings.ToLower(string(body))
	var found []string
	for _, kw := range keywords {
		k := strings.ToLower(strings.TrimSpace(kw))
		if k == "" {
			continue
		}
		if strings.Contains(lower, k) {
			found = append(found, k)
		}
	}
	return found
}

var titleRegex = regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)

func matchTitles(body []byte, titles []string) []string {
	if len(titles) == 0 {
		return nil
	}
	m := titleRegex.FindSubmatch(body)
	if m == nil {
		return nil
	}
	titleText := strings.ToLower(strings.TrimSpace(string(m[1])))
	if titleText == "" {
		return nil
	}
	var found []string
	for _, t := range titles {
		needle := strings.ToLower(strings.TrimSpace(t))
		if needle == "" {
			continue
		}
		if strings.Contains(titleText, needle) {
			found = append(found, needle)
		}
	}
	return found
}

type urlParts struct {
	host string
	path string
}

func parseURLParts(raw string) (urlParts, error) {
	stripped := raw
	if idx := strings.Index(raw, "://"); idx != -1 {
		stripped = raw[idx+3:]
	}
	if idx := strings.Index(stripped, "/"); idx != -1 {
		return urlParts{host: stripped[:idx], path: stripped[idx:]}, nil
	}
	return urlParts{host: stripped, path: "/"}, nil
}
