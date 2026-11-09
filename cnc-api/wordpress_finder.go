package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"cnc-enum/version"
)

const (
	finderCandidateLimit = 200
	finderApifyURL       = "https://api.apify.com/v2/actors/nFJndFXA5zjCTuudP/run-sync-get-dataset-items?timeout=100"
)

type wordpressFinderInput struct {
	Keyword string `json:"keyword"`
	URLs    string `json:"urls"`
	Recent  bool   `json:"recent"`
	Pages   int    `json:"pages"`
}

type wordpressFinderResult struct {
	URLs        []string `json:"urls"`
	Checked     int      `json:"checked"`
	Recent      int      `json:"recent"`
	SearchError string   `json:"search_error,omitempty"`
}

type finderCandidate struct {
	url    string
	recent bool
}

func finderSearch(ctx context.Context, client *http.Client, endpoint, token string, input wordpressFinderInput, now time.Time) ([]finderCandidate, error) {
	base := strings.TrimSpace(input.Keyword + ` "powered by WordPress" -site:wordpress.org`)
	if !input.Recent {
		return finderSearchQuery(ctx, client, endpoint, token, base, input.Pages, false)
	}
	type recentSearch struct {
		hits []finderCandidate
		err  error
	}
	recent := make(chan recentSearch, 1)
	go func() {
		query := base + " after:" + now.AddDate(0, 0, -30).Format("2006-01-02")
		hits, err := finderSearchQuery(ctx, client, endpoint, token, query, input.Pages, true)
		recent <- recentSearch{hits, err}
	}()
	hits, err := finderSearchQuery(ctx, client, endpoint, token, base, input.Pages, false)
	fresh := <-recent
	return append(fresh.hits, hits...), errors.Join(fresh.err, err)
}

func finderSearchQuery(ctx context.Context, client *http.Client, endpoint, token, query string, pages int, recent bool) ([]finderCandidate, error) {
	payload, err := json.Marshal(map[string]any{
		"queries": query, "maxPagesPerQuery": pages,
		"countryCode": "us", "searchLanguage": "en", "languageCode": "en",
		"focusOnPaidAds": false, "includeUnfilteredResults": true,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Apify search could not be reached or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("Apify search returned HTTP %d", resp.StatusCode)
	}
	const responseLimit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil || len(raw) > responseLimit {
		return nil, errors.New("Apify returned an incomplete or oversized result")
	}
	var rows []struct {
		OrganicResults []struct{ URL string } `json:"organicResults"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, errors.New("Apify returned an unreadable result")
	}
	var hits []finderCandidate
	for _, row := range rows {
		for _, hit := range row.OrganicResults {
			hits = append(hits, finderCandidate{url: hit.URL, recent: recent})
		}
	}
	return hits, nil
}

func finderUnique(seeds string, hits []finderCandidate) []finderCandidate {
	result := make([]finderCandidate, 0, finderCandidateLimit)
	indices := make(map[string]int)
	add := func(candidate finderCandidate) {
		home, host := version.WordPressHomepage(candidate.url)
		if host == "" {
			return
		}
		if i, ok := indices[host]; ok {
			result[i].recent = result[i].recent || candidate.recent
			return
		}
		if len(result) == finderCandidateLimit {
			return
		}
		indices[host] = len(result)
		result = append(result, finderCandidate{url: home, recent: candidate.recent})
	}
	for raw := range strings.SplitSeq(seeds, "\n") {
		add(finderCandidate{url: raw})
	}
	for _, candidate := range hits {
		if candidate.recent {
			add(candidate)
		}
	}
	for _, candidate := range hits {
		add(candidate)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].recent && !result[j].recent })
	return result
}

func verifyFinderCandidates(ctx context.Context, client *http.Client, candidates []finderCandidate, result *wordpressFinderResult) {
	urls := make([]string, len(candidates))
	work := make(chan int, len(candidates))
	for i := range candidates {
		work <- i
	}
	close(work)
	var workers sync.WaitGroup
	for range min(16, len(candidates)) {
		workers.Go(func() {
			for i := range work {
				if ctx.Err() != nil {
					return
				}
				urls[i], _ = version.VerifyWordPressHomepage(ctx, client, candidates[i].url)
			}
		})
	}
	workers.Wait()
	for i, u := range urls {
		if u != "" {
			result.URLs = append(result.URLs, u)
			if candidates[i].recent {
				result.Recent++
			}
		}
	}
	result.Checked = len(candidates)
}

func (s *Server) launchWordPressFinder(w http.ResponseWriter, r *http.Request, body []byte) {
	fail := func(code int, message string) {
		writeJSON(w, code, map[string]string{"error": message})
	}
	if _, err := dashboardActor(r, body); err != nil {
		fail(http.StatusForbidden, "WordPress Finder requires an authenticated dashboard")
		return
	}
	var input wordpressFinderInput
	if len(body) > 64<<10 || json.Unmarshal(body, &input) != nil || len(input.Keyword) > 240 ||
		strings.ContainsAny(input.Keyword, "\r\n") || strings.Count(strings.TrimSpace(input.URLs), "\n") >= finderCandidateLimit ||
		input.Pages < 0 || input.Pages > 5 {
		fail(http.StatusBadRequest, "Enter a keyword up to 240 characters and up to 200 URLs or domains")
		return
	}
	input.Keyword = strings.TrimSpace(input.Keyword)
	if input.Pages == 0 {
		input.Pages = 3
	}
	seeds := finderUnique(input.URLs, nil)
	search := input.Keyword != "" || strings.TrimSpace(input.URLs) == ""
	token := strings.TrimSpace(os.Getenv("APIFY_TOKEN"))
	if search && token == "" && len(seeds) == 0 {
		fail(http.StatusServiceUnavailable, "Set APIFY_TOKEN on the CNC controller for keyword search, or paste a list of URLs")
		return
	}
	if !search && len(seeds) == 0 {
		fail(http.StatusBadRequest, "No public HTTP or HTTPS domain was provided")
		return
	}
	if !s.wordpressFinderMu.TryLock() {
		fail(http.StatusTooManyRequests, "WordPress Finder is already running")
		return
	}
	defer s.wordpressFinderMu.Unlock()
	result := wordpressFinderResult{URLs: []string{}}
	var hits []finderCandidate
	if search && token != "" {
		client := &http.Client{Timeout: 110 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		var err error
		hits, err = finderSearch(r.Context(), client, finderApifyURL, token, input, time.Now())
		if err != nil {
			if len(seeds) == 0 && len(hits) == 0 {
				fail(http.StatusBadGateway, err.Error())
				return
			}
			result.SearchError = err.Error()
		}
	} else if search {
		result.SearchError = "APIFY_TOKEN is unavailable. Only the pasted URLs were checked."
	}
	client, closeClient, err := version.NewWordPressHomepageClient()
	if err != nil {
		fail(http.StatusInternalServerError, "Cannot initialize public homepage verification")
		return
	}
	defer closeClient()
	verifyFinderCandidates(r.Context(), client, finderUnique(input.URLs, hits), &result)
	writeJSON(w, http.StatusOK, result)
}

func handleWordPressGeneratorStatus(w http.ResponseWriter, r *http.Request) {
	path := os.Getenv("CNC_WORDPRESS_GENERATOR_STATUS_FILE")
	if path == "" {
		path = "/opt/cnc/wordpress-generator/status.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Generator status unavailable"})
		return
	}
	var status struct {
		UpdatedAt          int64    `json:"updated_at"`
		FeedAt             *int64   `json:"feed_at"`
		OldestFreshAt      *int64   `json:"oldest_fresh_at"`
		MergedVerified     int      `json:"merged_verified"`
		HostnamesPerSecond *float64 `json:"hostnames_per_second"`
		ActiveJob          *string  `json:"active_job"`
	}
	if err := json.Unmarshal(data, &status); err != nil || status.UpdatedAt == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Generator status unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}
