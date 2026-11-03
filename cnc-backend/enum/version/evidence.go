package version

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const EvidenceBodyLimit = 512 * 1024

type EvidenceProbe struct {
	Path, Source, Kind string
	Rank               int
}

func EvidencePlan(component string) ([]EvidenceProbe, error) {
	if err := ValidateComponent(component); err != nil {
		return nil, err
	}
	p := strings.Split(component, ":")
	if component == "php:php-src" {
		return []EvidenceProbe{{"", "php_powered_by", "php_header", 10}}, nil
	}
	if component == "wordpress:core" {
		return []EvidenceProbe{{"/wp-json/", "wp_rest_metadata", "wp_rest", 10}, {"/", "wp_generator", "wp_generator", 20}, {"/feed/", "wp_feed_generator", "wp_feed", 25}, {"/readme.html", "wp_readme", "wp_core_readme", 30}, {"/wp-includes/js/wp-emoji-release.min.js", "wp_exact_asset", "wp_exact_asset", 40}}, nil
	}
	if p[0] == "wordpress" {
		return []EvidenceProbe{{"/wp-content/plugins/" + p[2] + "/readme.txt", "wp_plugin_readme", "wp_plugin_readme", 10}, {"/wp-content/plugins/" + p[2] + "/package.json", "wp_plugin_metadata", "wp_plugin_metadata", 20}}, nil
	}
	typeName, name := p[1], ""
	if len(p) > 2 {
		name = p[len(p)-1]
	}
	var paths []string
	switch typeName {
	case "core":
		paths = []string{"/administrator/manifests/files/joomla.xml"}
	case "component":
		paths = []string{"/administrator/components/com_" + name + "/" + name + ".xml", "/administrator/components/com_" + name + "/com_" + name + ".xml", "/components/com_" + name + "/" + name + ".xml"}
	case "module":
		paths = []string{"/administrator/modules/mod_" + name + "/" + name + ".xml", "/modules/mod_" + name + "/" + name + ".xml"}
	case "plugin":
		paths = []string{"/plugins/" + p[2] + "/" + name + "/" + name + ".xml"}
	case "library":
		paths = []string{"/libraries/" + name + "/" + name + ".xml"}
	case "template":
		paths = []string{"/templates/" + name + "/templateDetails.xml", "/administrator/templates/" + name + "/templateDetails.xml"}
	case "package":
		paths = []string{"/administrator/manifests/packages/pkg_" + name + ".xml", "/administrator/manifests/packages/" + name + ".xml"}
	}
	probes := []EvidenceProbe{}
	for i, path := range paths {
		rank := (i + 1) * 10
		probes = append(probes, EvidenceProbe{path, fmt.Sprintf("joomla_manifest:%s:%d", typeName, rank), "joomla_manifest", rank})
	}
	return probes, nil
}

var stableTag = regexp.MustCompile(`(?im)^\s*stable tag:\s*([^\r\n]+)\s*$`)
var pluginHeading = regexp.MustCompile(`(?m)^===\s*[^=\r\n]+\s*===\s*$`)
var identityWords = regexp.MustCompile(`[a-z0-9]+`)

// Require the requested slug in the readme identity, not just any readme
// returned by a rewrite or a redirect. Display-name aliases are explicit.
func pluginReadmeIdentity(component string, data []byte) bool {
	p := strings.Split(component, ":")
	if len(p) != 3 {
		return false
	}
	heading := pluginHeading.Find(data)
	if len(heading) == 0 || len(heading) > 512 {
		return false
	}
	words := identityWords.FindAllString(strings.ToLower(string(heading)), -1)
	want := strings.Join(identityWords.FindAllString(p[2], -1), "")
	aliases := map[string]string{"wordpress-seo": "yoastseo", "all-in-one-seo-pack": "allinoneseo", "translatepress-multilingual": "translatepress"}
	for i := range words {
		for j := i + 1; j <= len(words); j++ {
			name := strings.Join(words[i:j], "")
			if name == want || (aliases[p[2]] != "" && name == aliases[p[2]]) {
				return true
			}
		}
	}
	return false
}

var metaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
var htmlAttribute = regexp.MustCompile(`(?is)([a-z_:][-a-z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
var coreReadmeVersion = regexp.MustCompile(`(?is)<(?:h1|h2)[^>]*>\s*(?:<[^>]+>\s*)*Version\s+([^\s<]+)`)
var blockedPage = regexp.MustCompile(`(?is)(?:<title>[^<]*(?:404|not found|forbidden|blocked|access denied)|cf-chl-|cloudflare ray id|attention required!|request blocked|access to this resource is denied)`)
var exactCoreBanner = regexp.MustCompile(`\A/\*! WordPress v?([^\s*]+) \*/`)

func responseData(response *http.Response) ([]byte, string, error) {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("evidence response not successful")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, EvidenceBodyLimit+1))
	if err != nil || len(data) > EvidenceBodyLimit {
		return nil, "", fmt.Errorf("evidence response exceeds limit or is incomplete")
	}
	if blockedPage.Match(data) {
		return nil, "", fmt.Errorf("blocked or missing evidence")
	}
	typeName, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		typeName = ""
	}
	return data, strings.ToLower(typeName), nil
}

func EvidenceVersion(component string, probe EvidenceProbe, data []byte, contentType string) (string, error) {
	var raw string
	html := strings.Contains(strings.ToLower(string(data)), "<html") || contentType == "text/html"
	switch probe.Kind {
	case "wp_rest":
		if html {
			return "", fmt.Errorf("HTML is not REST evidence")
		}
		var payload struct {
			Generator json.RawMessage `json:"generator"`
		}
		if json.Unmarshal(data, &payload) != nil {
			return "", fmt.Errorf("invalid REST evidence")
		}
		var generator string
		if json.Unmarshal(payload.Generator, &generator) == nil {
			if strings.HasPrefix(generator, "WordPress ") {
				raw = strings.TrimPrefix(generator, "WordPress ")
			} else {
				u, err := url.Parse(generator)
				if err == nil && u.Scheme == "https" && u.Host == "wordpress.org" && u.Path == "/" && len(u.Query()) == 1 {
					raw = u.Query().Get("v")
				}
			}
		} else {
			var generator struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if json.Unmarshal(payload.Generator, &generator) == nil && generator.Name == "WordPress" {
				raw = generator.Version
			}
		}
	case "wp_generator":
		for _, tag := range metaTag.FindAllString(string(data), -1) {
			attrs := map[string]string{}
			for _, match := range htmlAttribute.FindAllStringSubmatch(tag, -1) {
				v := match[2]
				if match[3] != "" {
					v = match[3]
				}
				attrs[strings.ToLower(match[1])] = v
			}
			if strings.EqualFold(attrs["name"], "generator") && strings.HasPrefix(attrs["content"], "WordPress ") {
				v := strings.TrimPrefix(attrs["content"], "WordPress ")
				if raw != "" && raw != v {
					return "", fmt.Errorf("ambiguous generator evidence")
				}
				raw = v
			}
		}
	case "wp_feed":
		if html {
			return "", fmt.Errorf("HTML is not feed evidence")
		}
		var feed struct {
			XMLName  xml.Name
			Channels []struct {
				Generators []string `xml:"generator"`
			} `xml:"channel"`
		}
		if xml.Unmarshal(data, &feed) != nil || feed.XMLName.Local != "rss" || feed.XMLName.Space != "" || len(feed.Channels) != 1 || len(feed.Channels[0].Generators) != 1 {
			return "", fmt.Errorf("invalid or ambiguous RSS evidence")
		}
		u, err := url.Parse(strings.TrimSpace(feed.Channels[0].Generators[0]))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != "wordpress.org" || u.User != nil || u.Path != "/" || u.Fragment != "" {
			return "", fmt.Errorf("feed generator identity mismatch")
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(query) != 1 || len(query["v"]) != 1 {
			return "", fmt.Errorf("invalid feed generator version")
		}
		raw = query.Get("v")
	case "wp_core_readme":
		if !strings.Contains(strings.ToLower(string(data)), "wordpress") {
			return "", fmt.Errorf("not a WordPress readme")
		}
		matches := coreReadmeVersion.FindAllSubmatch(data, -1)
		if len(matches) != 1 {
			return "", fmt.Errorf("ambiguous readme version")
		}
		raw = string(matches[0][1])
	case "wp_plugin_readme":
		if html || !pluginHeading.Match(data) || !pluginReadmeIdentity(component, data) {
			return "", fmt.Errorf("not a plugin readme")
		}
		matches := stableTag.FindAllSubmatch(data, -1)
		if len(matches) != 1 {
			return "", fmt.Errorf("ambiguous stable tag")
		}
		raw = strings.TrimSpace(string(matches[0][1]))
	case "wp_plugin_metadata":
		if html {
			return "", fmt.Errorf("HTML is not plugin metadata")
		}
		var payload struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		p := strings.Split(component, ":")
		if json.Unmarshal(data, &payload) != nil || payload.Name != p[2] {
			return "", fmt.Errorf("metadata identity mismatch")
		}
		raw = payload.Version
	case "wp_exact_asset":
		if html || (contentType != "application/javascript" && contentType != "text/javascript") || !strings.Contains(string(data), "wpemoji") {
			return "", fmt.Errorf("not an approved exact core asset")
		}
		match := exactCoreBanner.FindSubmatch(data)
		if len(match) != 2 {
			return "", fmt.Errorf("asset has no explicit core version banner")
		}
		raw = string(match[1])
	case "joomla_manifest":
		if html {
			return "", fmt.Errorf("HTML is not a manifest")
		}
		var manifest struct {
			XMLName  xml.Name
			Type     string   `xml:"type,attr"`
			Name     string   `xml:"name"`
			Versions []string `xml:"version"`
		}
		if xml.Unmarshal(data, &manifest) != nil || len(manifest.Versions) != 1 {
			return "", fmt.Errorf("invalid manifest")
		}
		p := strings.Split(component, ":")
		expected := p[1]
		if expected == "core" {
			expected = "file"
		}
		if manifest.XMLName.Local != "extension" || manifest.Type != expected {
			return "", fmt.Errorf("manifest type mismatch")
		}
		if p[1] == "core" && manifest.Name != "files_joomla" {
			return "", fmt.Errorf("core manifest identity mismatch")
		}
		if p[1] != "core" {
			identity := strings.Join(identityWords.FindAllString(strings.ToLower(manifest.Name), -1), "")
			name := strings.Join(identityWords.FindAllString(p[len(p)-1], -1), "")
			prefix := map[string]string{"component": "com", "module": "mod", "plugin": "plg", "library": "lib", "template": "tpl", "package": "pkg"}[p[1]]
			valid := identity == name || identity == prefix+name
			if p[1] == "plugin" {
				valid = valid || identity == "plg"+strings.Join(identityWords.FindAllString(p[2], -1), "")+name
			}
			if !valid {
				return "", fmt.Errorf("manifest identity mismatch")
			}
		}
		raw = strings.TrimSpace(manifest.Versions[0])
	default:
		return "", fmt.Errorf("unsupported evidence kind")
	}
	return NormalizeVersion(raw)
}

func ResolveEvidence(component string, candidates []EvidenceCandidate) (VersionObservation, error) {
	observation := VersionObservation{Component: component, Confidence: "unknown", EvidenceCandidates: []EvidenceCandidate{}}
	if err := ValidateComponent(component); err != nil {
		return observation, err
	}
	seen := map[int]bool{}
	plan, _ := EvidencePlan(component)
	for _, candidate := range candidates {
		version, err := NormalizeVersion(candidate.Version)
		approved := false
		for _, probe := range plan {
			approved = approved || (candidate.Source == probe.Source && candidate.Rank == probe.Rank && candidate.Validation.Kind == probe.Kind)
		}
		approved = approved || (component == "wordpress:core" && candidate.Source == "wp_exact_asset" && candidate.Rank == 40 && candidate.Validation.Kind == "wp_exact_asset")
		mediaType, _, mediaErr := mime.ParseMediaType(candidate.Validation.ContentType)
		if candidate.Validation.ContentType == "" {
			mediaErr = nil
		}
		if err != nil || !approved || seen[candidate.Rank] || candidate.RetrievedAt.IsZero() || candidate.Validation.Bytes < 0 || candidate.Validation.Bytes > EvidenceBodyLimit || mediaErr != nil || (candidate.Validation.ContentType != "" && mediaType != candidate.Validation.ContentType) {
			return observation, fmt.Errorf("invalid evidence candidate")
		}
		candidate.Version = version
		seen[candidate.Rank] = true
		observation.EvidenceCandidates = append(observation.EvidenceCandidates, candidate)
	}
	sort.Slice(observation.EvidenceCandidates, func(i, j int) bool {
		return observation.EvidenceCandidates[i].Rank < observation.EvidenceCandidates[j].Rank
	})
	if len(candidates) == 0 {
		return observation, nil
	}
	selected := observation.EvidenceCandidates[0]
	for _, candidate := range observation.EvidenceCandidates {
		if candidate.Version != selected.Version {
			observation.Confidence = ConfidenceConflicted
			return observation, nil
		}
	}
	observation.Version = &selected.Version
	observation.SelectedEvidence = &selected
	observation.Confidence = ConfidenceAuthoritative
	if component == "php:php-src" {
		observation.Confidence = ConfidenceReported
	}
	if selected.Source == "wp_exact_asset" {
		observation.Confidence = ConfidenceValidatedFallback
	}
	return observation, nil
}

type Collector struct {
	client        *http.Client
	target        TargetLocator
	once          sync.Once
	baseline      [32]byte
	baselineType  string
	baselineValid bool
}

func NewCollector(client *http.Client, target TargetLocator) *Collector {
	return &Collector{client: client, target: target}
}
func evidenceSignature(data []byte, path string) [32]byte {
	text := string(data)
	if path != "" && path != "/" {
		text = strings.ReplaceAll(text, path, "<requested-path>")
	}
	return sha256.Sum256([]byte(strings.Join(strings.Fields(text), " ")))
}
func (c *Collector) missingBaseline(ctx context.Context) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return
	}
	location := c.target
	location.Path = fmt.Sprintf("/__cnc_evidence_missing_%x", nonce)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.URL(), nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "cnc-enum/public-evidence")
	response, err := c.client.Do(req)
	if err != nil {
		return
	}
	data, contentType, err := responseData(response)
	if err != nil {
		return
	}
	c.baseline = evidenceSignature(data, location.Path)
	c.baselineType = contentType
	c.baselineValid = true
}
func CollectEvidence(ctx context.Context, client *http.Client, target TargetLocator, component string) (VersionObservation, error) {
	return NewCollector(client, target).Collect(ctx, component)
}
func (c *Collector) Collect(ctx context.Context, component string) (VersionObservation, error) {
	plan, err := EvidencePlan(component)
	if err != nil {
		return VersionObservation{}, err
	}
	if component != "php:php-src" {
		c.once.Do(func() { c.missingBaseline(ctx) })
	}
	candidates := []EvidenceCandidate{}
	transportErrors := 0
	for _, probe := range plan {
		location := c.target
		if component != "php:php-src" {
			location.Path = probe.Path
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.URL(), nil)
		if err != nil {
			return VersionObservation{}, err
		}
		req.Header.Set("User-Agent", "cnc-enum/public-evidence")
		response, err := c.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return VersionObservation{}, ctx.Err()
			}
			transportErrors++
			continue
		}
		if response.StatusCode >= 500 {
			transportErrors++
		}
		// A redirect to another plugin's metadata cannot identify this plugin.
		if strings.HasPrefix(component, "wordpress:plugin:") && response.Request != nil && response.Request.URL.EscapedPath() != req.URL.EscapedPath() {
			response.Body.Close()
			continue
		}
		header := response.Header.Clone()
		data, contentType, err := responseData(response)
		if err != nil {
			continue
		}
		if component != "php:php-src" && c.baselineValid && contentType == c.baselineType && evidenceSignature(data, probe.Path) == c.baseline {
			continue
		}
		value, err := EvidenceVersion(component, probe, data, contentType)
		if probe.Kind == "php_header" {
			value, err = PHPHeaderVersion(header)
		}
		if err != nil {
			continue
		}
		candidates = append(candidates, EvidenceCandidate{Source: probe.Source, Rank: probe.Rank, Version: value, RetrievedAt: time.Now().UTC(), Validation: EvidenceValidation{Kind: probe.Kind, ContentType: contentType, Bytes: int64(len(data))}})
	}
	observation, err := ResolveEvidence(component, candidates)
	if err == nil && transportErrors > 0 {
		err = fmt.Errorf("%d evidence requests failed", transportErrors)
	}
	return observation, err
}

// Only explicit PHP product/version declarations count. HTML prose, cookies,
// file extensions and generic server names cannot identify a PHP version.
var phpProduct = regexp.MustCompile(`(?i)(?:^|[ ,;\t])PHP/([^ ,;\t]+)`)
var phpStable = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func PHPHeaderVersion(header http.Header) (string, error) {
	selected := ""
	for _, line := range header.Values("X-Powered-By") {
		for _, m := range phpProduct.FindAllStringSubmatch(line, -1) {
			if !phpStable.MatchString(m[1]) {
				return "", fmt.Errorf("unsupported PHP version declaration")
			}
			value, err := NormalizeVersion(m[1])
			if err != nil {
				return "", err
			}
			if selected != "" && value != selected {
				return "", fmt.Errorf("conflicting PHP version declarations")
			}
			selected = value
		}
	}
	if selected == "" {
		return "", fmt.Errorf("PHP version is not disclosed")
	}
	return selected, nil
}
