package main

import (
	"bufio"
	"cnc-enum/detection"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cnc-enum/version"
)

type pipelineOptions struct {
	Input, Artifact, Digest, JobID, ShardID, OutputDir, WPPlugins, JoomlaExts string
	Threads                                                                   int
	Timeout                                                                   time.Duration
	Automatic                                                                 bool
}

func cleanOutputURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	return u.String()
}

func cleanBaseURL(target version.TargetLocator) string {
	return strings.TrimRight(cleanOutputURL(target.URL()), "/")
}

func runVersionPipeline(ctx context.Context, options pipelineOptions, stdout, stderr io.Writer) int {
	artifact, err := version.LoadArtifact(options.Artifact, options.Digest, options.JobID)
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Invalid version artifact:", err)
		return 1
	}
	if !version.ValidTransportID(options.ShardID) || options.Threads < 1 || options.Threads > 256 || options.Timeout <= 0 {
		fmt.Fprintln(stderr, "[ERROR] Invalid scanner execution settings")
		return 1
	}
	guard, err := version.NewScopeGuard(artifact.TargetScope)
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Invalid target scope")
		return 1
	}
	client := guard.Client(options.Timeout)
	defer guard.CloseIdleConnections()
	if gateway := os.Getenv("ENUM_EVIDENCE_GATEWAY"); gateway != "" {
		u, e := url.Parse(gateway)
		if e != nil || u.Scheme != "http" || u.Path != "/fetch" || u.User != nil {
			fmt.Fprintln(stderr, "[ERROR] Invalid worker evidence gateway")
			return 1
		}
		address, e := netip.ParseAddr(u.Hostname())
		if e != nil || !address.IsLoopback() || os.Getenv("ENUM_EVIDENCE_TOKEN") == "" {
			fmt.Fprintln(stderr, "[ERROR] Invalid worker evidence gateway")
			return 1
		}
		client.Transport = &version.GatewayTransport{URL: gateway, Token: os.Getenv("ENUM_EVIDENCE_TOKEN")}
	} else {
		limit, e := version.BudgetLimit(os.Getenv("ENUM_MAX_INFLIGHT_REQUESTS"))
		if e != nil {
			fmt.Fprintln(stderr, "[ERROR]", e)
			return 1
		}
		budget, _ := version.NewRequestBudget(limit)
		client.Transport = &version.BudgetTransport{Budget: budget, JobID: artifact.JobID, Base: guard}
	}
	scheduler := detection.NewScheduler(client.Transport, options.Threads, options.Automatic)
	start := time.Now()
	wp, err := pipelineWordlist(options.WPPlugins, defaultWPPlugins)
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Cannot read WordPress wordlist")
		return 1
	}
	joomla, err := pipelineWordlist(options.JoomlaExts, defaultJoomlaExtensions)
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Cannot read Joomla wordlist")
		return 1
	}
	input, err := os.Open(options.Input)
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Cannot read target input")
		return 1
	}
	defer input.Close()
	result, err := version.NewResultFile(options.OutputDir, artifact.JobID, options.ShardID)
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Cannot create result file")
		return 1
	}
	defer result.Abort()
	var outputMu sync.Mutex
	var writeFailed atomic.Bool
	emitted := make(map[string]bool)
	emit := func(name, line string) {
		outputMu.Lock()
		defer outputMu.Unlock()
		key := name + "\x00" + line
		if emitted[key] {
			return
		}
		emitted[key] = true
		if _, err := fmt.Fprintf(stdout, "[FILE:%s] %s\n", name, line); err != nil {
			writeFailed.Store(true)
		}
	}
	type targetJob struct {
		target version.TargetLocator
		raw    string
	}
	jobs := make(chan targetJob, options.Threads)
	var wg sync.WaitGroup
	var processed, failed atomic.Int64
	var successful atomic.Int64
	targetWorkers := options.Threads
	if targetWorkers > 64 {
		targetWorkers = 64
	}
	for i := 0; i < targetWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				target := job.target
				targetCtx := detection.TargetContext(ctx, target.URL())
				targetClient := detection.NewSession(client, scheduler)
				targetFailed := false
				hasValidOutput := false
				if ctx.Err() != nil {
					return
				}
				components := []string{}
				for _, rule := range artifact.Rules {
					if rule.Component == "php:php-src" {
						components = append(components, rule.Component)
					}
				}
				cms := ""
				var classifyErr error
				classified := detection.Classify(targetCtx, targetClient, job.raw)
				cms = classified.CMS
				classifyErr = classified.Err
				if canonical, e := version.CanonicalTarget(classified.URL); e == nil {
					target = canonical
				}
				if classified.Outcome == "blocked" {
					classifyErr = fmt.Errorf("classification blocked")
					emit("cms-blocked.txt", cleanOutputURL(target.URL()))
				}
				if classified.Outcome == "error" {
					emit("cms-errors.txt", cleanOutputURL(target.URL()))
				}
				if classifyErr != nil {
					targetFailed = true
					failed.Add(1)
				}
				switch cms {
				case "wordpress":
					hasValidOutput = true
					emit("wordpress.txt", cleanBaseURL(target))
					components = append(components, "wordpress:core")
					for _, rule := range artifact.Rules {
						if strings.HasPrefix(rule.Component, "wordpress:") {
							components = append(components, rule.Component)
						}
					}
					if options.WPPlugins != "" || len(artifact.Rules) == 0 {
						for _, slug := range wp {
							component := "wordpress:plugin:" + slug
							if version.ValidateComponent(component) == nil {
								components = append(components, component)
							}
						}
					}
				case "joomla":
					hasValidOutput = true
					emit("joomla.txt", cleanOutputURL(target.URL()))
					components = append(components, "joomla:core")
					for _, rule := range artifact.Rules {
						if strings.HasPrefix(rule.Component, "joomla:") {
							components = append(components, rule.Component)
						}
					}
					if options.JoomlaExts != "" || len(artifact.Rules) == 0 {
						for _, entry := range joomla {
							if component := joomlaComponent(entry); component != "" {
								components = append(components, component)
							}
						}
					}
				case "drupal", "shopify", "ghost", "magento", "wix", "squarespace":
					hasValidOutput = true
					emit(cms+".txt", cleanOutputURL(target.URL()))
					for _, rule := range artifact.Rules {
						if strings.HasPrefix(rule.Component, cms+":") {
							components = append(components, rule.Component)
						}
					}
				default:
					if classifyErr == nil {
						emit("cms-unknown.txt", cleanOutputURL(target.URL()))
					}
					for _, rule := range artifact.Rules {
						prefix := strings.Split(rule.Component, ":")[0]
						if prefix != "wordpress" && prefix != "joomla" && prefix != "drupal" && prefix != "shopify" && prefix != "ghost" && prefix != "magento" && prefix != "wix" && prefix != "squarespace" {
							components = append(components, rule.Component)
						}
					}
					found, adminErr := adminScopedFinding(targetCtx, targetClient, target)
					if adminErr != nil {
						targetFailed = true
						failed.Add(1)
					}
					if found.URL != "" {
						if found.Kind == "cpanel" {
							emit("cpanel.txt", cleanOutputURL(found.URL))
						} else {
							emit("admin.txt", cleanOutputURL(found.URL))
							emit("enum-admin.txt", cleanOutputURL(found.URL))
						}
						hasValidOutput = true
					}
				}
				sort.Strings(components)
				collector := version.NewCollector(targetClient, target)
				var targetMu sync.Mutex
				last := ""
				unique := []string{}
				for _, component := range components {
					if component != last {
						unique = append(unique, component)
					}
					last = component
				}
				processComponent := func(component string) {
					observation, e := collector.Collect(targetCtx, component)
					targetMu.Lock()
					if e != nil {
						targetFailed = true
						failed.Add(1)
						if observation.Component == "" {
							targetMu.Unlock()
							return
						}
					}
					hasValidOutput = hasValidOutput || len(observation.EvidenceCandidates) > 0
					matches := version.MatchRules(observation, artifact.Rules)
					if len(matches) == 0 && len(observation.EvidenceCandidates) == 0 && !strings.HasSuffix(component, ":core") {
						targetMu.Unlock()
						return
					}
					if len(matches) > 0 || (len(artifact.Rules) == 0 && len(observation.EvidenceCandidates) > 0) {
						record := version.ResultRecordV1{
							SchemaVersion: 1,
							JobID:         artifact.JobID,
							ShardID:       options.ShardID,
							// Receipts bind evidence to the submitted input even when
							// classification follows a redirect to its canonical URL.
							Target:       job.target,
							MatchedRules: matches,
							Observation:  observation,
						}
						if result.Append(record) != nil {
							writeFailed.Store(true)
						}
					}
					if component == "php:php-src" && observation.Version != nil && len(matches) > 0 {
						emit("php-ver.txt", cleanOutputURL(target.URL()))
						emit("php_versions.txt", cleanOutputURL(target.URL())+"\t"+*observation.Version)
					}
					if component == "wordpress:core" && observation.Version != nil && observation.Confidence != "conflicted" && (len(artifact.Rules) == 0 || len(matches) > 0) {
						emit("wordpress_core_versions.txt", cleanOutputURL(target.URL())+"\t"+*observation.Version)
					}
					if observation.Version != nil && (len(artifact.Rules) == 0 || len(matches) > 0) && observation.Confidence != "conflicted" && component != "php:php-src" && !strings.HasSuffix(component, ":core") {
						slug := discoverySlug(component)
						emit(discoveryFilename(component), cleanOutputURL(target.URL()))
						if strings.HasPrefix(component, "wordpress:plugin:") {
							baseURL := cleanBaseURL(target)
							emit("plugins_name.txt", fmt.Sprintf("%s/wp-content/plugins/%s", baseURL, slug))
						}
					}
					targetMu.Unlock()
				}
				// Core first preserves accepted evidence when subsequent probes cancel.
				for _, component := range unique {
					if strings.HasSuffix(component, ":core") {
						processComponent(component)
					}
				}
				componentJobs := make(chan string)
				var componentWG sync.WaitGroup
				for n := 0; n < 4; n++ {
					componentWG.Add(1)
					go func() {
						defer componentWG.Done()
						for component := range componentJobs {
							processComponent(component)
						}
					}()
				}
				for _, component := range unique {
					if strings.HasSuffix(component, ":core") {
						continue
					}
					select {
					case componentJobs <- component:
					case <-ctx.Done():
					}
				}
				close(componentJobs)
				componentWG.Wait()
				processed.Add(1)
				if !targetFailed || hasValidOutput {
					successful.Add(1)
				}
			}
		}()
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	seen := map[version.TargetLocator]bool{}
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		target, e := version.CanonicalTarget(raw)
		if e != nil {
			failed.Add(1)
			fmt.Fprintln(stderr, "[WARN] Skipping invalid HTTP target")
			continue
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		select {
		case jobs <- targetJob{target: target, raw: raw}:
		case <-ctx.Done():
			failed.Add(1)
		}
	}
	close(jobs)
	wg.Wait()
	if scanner.Err() != nil || writeFailed.Load() {
		fmt.Fprintln(stderr, "[ERROR] Incomplete input or output")
		return 1
	}
	_, err = result.Finish()
	if err != nil {
		fmt.Fprintln(stderr, "[ERROR] Could not finalize result file")
		return 1
	}
	m := scheduler.Snapshot()
	fmt.Fprintf(stderr, "[*] targets=%d errors=%d concurrency=%d requests=%d cache_hits=%d elapsed=%s\n", processed.Load(), failed.Load(), m.Concurrency, m.Requests, m.CacheHits, time.Since(start).Round(time.Millisecond))
	if ctx.Err() != nil {
		return 1
	}
	if successful.Load() == 0 && failed.Load() > 0 {
		return 1
	}
	return 0
}

func discoverySlug(component string) string {
	parts := strings.Split(component, ":")
	if len(parts) >= 3 && parts[0] == "wordpress" && parts[1] == "plugin" {
		return parts[2]
	}
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return component
}

func discoveryFilename(component string) string {
	parts := strings.Split(component, ":")
	if parts[0] == "wordpress" {
		return parts[2] + ".txt"
	}
	prefix := map[string]string{"component": "com_", "module": "mod_", "library": "lib_", "template": "tpl_", "package": "pkg_"}
	if parts[1] == "plugin" {
		return "plugin:" + parts[2] + ":" + parts[3] + ".txt"
	}
	return prefix[parts[1]] + parts[2] + ".txt"
}

func pipelineWordlist(path string, fallback []string) ([]string, error) {
	if path == "" {
		return fallback, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	list := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			list = append(list, line)
		}
	}
	return list, scanner.Err()
}
func joomlaComponent(entry string) string {
	component := ""
	switch {
	case strings.HasPrefix(entry, "joomla:"):
		component = entry
	case strings.HasPrefix(entry, "plugin:"):
		component = "joomla:" + entry
	case strings.HasPrefix(entry, "com_"):
		component = "joomla:component:" + strings.TrimPrefix(entry, "com_")
	case strings.HasPrefix(entry, "mod_"):
		component = "joomla:module:" + strings.TrimPrefix(entry, "mod_")
	case strings.HasPrefix(entry, "lib_"):
		component = "joomla:library:" + strings.TrimPrefix(entry, "lib_")
	case strings.HasPrefix(entry, "pkg_"):
		component = "joomla:package:" + strings.TrimPrefix(entry, "pkg_")
	default:
		component = "joomla:component:" + entry
	}
	if version.ValidateComponent(component) != nil {
		return ""
	}
	return component
}
func classifyScoped(ctx context.Context, client *http.Client, target version.TargetLocator) (string, error) {
	r := detection.Classify(ctx, client, target.URL())
	if r.Outcome == "blocked" {
		return r.CMS, fmt.Errorf("classification blocked")
	}
	return r.CMS, r.Err
}

type adminFinding struct{ URL, Kind string }

func adminScoped(ctx context.Context, client *http.Client, target version.TargetLocator) (string, error) {
	finding, err := adminScopedFinding(ctx, client, target)
	return finding.URL, err
}

func adminScopedFinding(ctx context.Context, client *http.Client, target version.TargetLocator) (adminFinding, error) {
	origin := target
	origin.Path = "/"
	base := strings.TrimRight(origin.URL(), "/")
	home, homeErr := fetchAdminResponseContext(ctx, client, base+"/")
	if homeErr == nil && home.status == http.StatusOK && !detection.Blocked(home.status, home.body) && loginFormKind(home.body) == "cpanel" {
		if locator, err := version.CanonicalTarget(home.finalURL); err == nil {
			return adminFinding{URL: locator.URL(), Kind: "cpanel"}, nil
		}
	}
	baseline, err := fetchAdminResponseContext(ctx, client, base+missingAdminPath())
	if err != nil || baseline.status >= 500 {
		return adminFinding{}, fmt.Errorf("admin baseline request failed")
	}
	if isThirdPartyAdmin(baseline.body) {
		return adminFinding{}, nil
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	found := adminFinding{}
	errors := 0
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				response, e := fetchAdminResponseContext(probeCtx, client, base+p)
				kind := loginFormKind(response.body)
				mu.Lock()
				if e != nil || response.status >= 500 {
					if probeCtx.Err() == nil {
						errors++
					}
					mu.Unlock()
					continue
				}
				if response.status == http.StatusOK && !detection.Blocked(response.status, response.body) && kind != "" && response.finalURL != baseline.finalURL && response.signature != baseline.signature && !(baseline.status == 200 && response.structure != "" && response.structure == baseline.structure) {
					if locator, e := version.CanonicalTarget(response.finalURL); e == nil && found.URL == "" {
						found = adminFinding{URL: locator.URL(), Kind: kind}
						cancel()
					}
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for _, p := range hardcodedAdminPaths {
		select {
		case jobs <- p:
		case <-probeCtx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return found, ctx.Err()
	}
	if errors > 0 {
		return found, fmt.Errorf("%d admin requests failed", errors)
	}
	return found, nil
}
