// Package detection is the shared, bounded public CMS fingerprint engine.
package detection

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
)

const BodyLimit = 512 * 1024

var Names = []string{"wordpress", "joomla", "drupal", "shopify", "ghost", "magento", "wix", "squarespace"}

type Result struct {
	CMS, Outcome, URL string
	Status            int
	Signals           []string
	Err               error
}
type evidence struct {
	strong  bool
	signals map[string]bool
}

func Blocked(status int, body []byte) bool {
	if status == 401 || status == 403 || status == 429 {
		return true
	}
	s := strings.ToLower(string(body))
	for _, marker := range []string{"cf-chl-", "captcha-delivery.com", "challenge-platform"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	if !strings.Contains(s, "access denied") &&
		!(strings.Contains(s, "verify") && strings.Contains(s, "human")) &&
		!strings.Contains(s, "checking your browser") {
		return false
	}
	z := html.NewTokenizer(strings.NewReader(s))
	active := ""
	var heading strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken:
			if tag := z.Token().Data; active == "" && (tag == "title" || tag == "h1" || tag == "h2") {
				active = tag
				heading.Reset()
			}
		case html.EndTagToken:
			if z.Token().Data == active {
				value := strings.Join(strings.Fields(heading.String()), " ")
				if value == "access denied" || value == "verify you are human" || value == "checking your browser" {
					return true
				}
				active = ""
			}
		case html.TextToken:
			if active != "" {
				heading.Write(z.Text())
			}
		}
	}
}

// Analyze requires structural evidence; prose and brand mentions are not signals.
func Analyze(body []byte, headers http.Header) (string, []string) {
	all := map[string]*evidence{}
	add := func(cms, signal string, strong bool) {
		e := all[cms]
		if e == nil {
			e = &evidence{signals: map[string]bool{}}
			all[cms] = e
		}
		e.signals[signal] = true
		e.strong = e.strong || strong
	}
	header := func(cms, key, token string, strong bool) {
		for _, v := range headers.Values(key) {
			if v != "" && (token == "" || strings.Contains(strings.ToLower(v), token)) {
				add(cms, "header:"+key, strong)
				return
			}
		}
	}
	header("wordpress", "Link", "https://api.w.org/", true)
	for _, cms := range Names {
		header(cms, "X-Powered-By", cms, true)
		header(cms, "X-Generator", cms, true)
	}
	for key, cms := range map[string]string{"X-Drupal-Cache": "drupal", "X-Drupal-Dynamic-Cache": "drupal", "X-Shopify-Store": "shopify", "X-Shopify-Stage": "shopify", "X-Magento-Tags": "magento", "X-Magento-Cache-Control": "magento", "X-Wix-Request-Id": "wix", "X-Squarespace-Request-Id": "squarespace"} {
		header(cms, key, "", true)
	}
	response := &http.Response{Header: headers}
	for _, c := range response.Cookies() {
		n := strings.ToLower(c.Name)
		for prefix, cms := range map[string]string{"wordpress_": "wordpress", "wp-settings-": "wordpress", "joomla_": "joomla", "jfcookie": "joomla", "sess": "drupal", "sseSS": "drupal", "_shopify_": "shopify", "ghost-admin": "ghost", "mage-": "magento", "wix": "wix", "ss_cvisit": "squarespace"} {
			if strings.HasPrefix(n, strings.ToLower(prefix)) {
				add(cms, "cookie", false)
			}
		}
	}
	z := html.NewTokenizer(strings.NewReader(string(body)))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		attrs := map[string]string{}
		for _, a := range t.Attr {
			attrs[strings.ToLower(a.Key)] = strings.ToLower(a.Val)
		}
		if t.Data == "input" && attrs["type"] == "hidden" && attrs["name"] == "option" && attrs["value"] == "com_login" {
			add("joomla", "administrator-form", true)
		}
		if t.Data == "meta" && attrs["name"] == "generator" {
			for _, cms := range Names {
				if strings.HasPrefix(attrs["content"], cms) {
					add(cms, "generator", true)
				}
			}
		}
		for _, key := range []string{"src", "href", "action"} {
			v := attrs[key]
			if v == "" {
				continue
			}
			u, _ := url.Parse(v)
			if u == nil {
				continue
			}
			p := u.Path
			h := u.Hostname()
			for marker, cms := range map[string]string{"/wp-content/": "wordpress", "/wp-includes/": "wordpress", "/wp-json": "wordpress", "/media/system/": "joomla", "/media/jui/": "joomla", "/media/vendor/joomla": "joomla", "/sites/default/": "drupal", "/core/misc/drupal": "drupal", "/misc/drupal": "drupal", "/mage/": "magento", "/skin/frontend/": "magento", "/static/frontend/": "magento", "/ghost/": "ghost"} {
				if strings.Contains(p, marker) {
					add(cms, "asset:"+marker, false)
				}
			}
			if p == "/wp-login.php" {
				add("wordpress", "login", true)
			}
			for host, cms := range map[string]string{"cdn.shopify.com": "shopify", "wixstatic.com": "wix", "wix.com": "wix", "squarespace.com": "squarespace", "ghost.org": "ghost"} {
				if (h == host || strings.HasSuffix(h, "."+host)) && (t.Data == "script" || t.Data == "link" && attrs["rel"] == "stylesheet") {
					add(cms, "host:"+host, cms != "ghost")
				}
			}
			if strings.Contains(u.RawQuery, "option=com_") {
				add("joomla", "component-link", false)
			}
		}
	}
	// Structured runtime objects are CMS-specific; plain matching words are not.
	lower := strings.ToLower(string(body))
	for marker, cms := range map[string]string{"drupalsettings": "drupal", "drupal.settings": "drupal", "shopify.theme": "shopify", "window.shopify": "shopify", "wixbi": "wix", "static.squarespace": "squarespace"} {
		if strings.Contains(lower, marker) {
			add(cms, "runtime", false)
		}
	}
	// Preserve compact machine-generated asset listings (no HTML/prose).
	if !strings.Contains(lower, "<") {
		tokens := strings.Fields(lower)
		for _, token := range tokens {
			for marker, cms := range map[string]string{"wp-content": "wordpress", "wp-includes": "wordpress", "wp-json": "wordpress", "sites/default": "drupal", "core/modules": "drupal", "com_content": "joomla", "com_users": "joomla"} {
				if token == marker {
					add(cms, "asset:"+marker, false)
				}
			}
		}
	}
	type candidate struct {
		name   string
		strong bool
		n      int
	}
	candidates := []candidate{}
	for cms, e := range all {
		if e.strong || len(e.signals) >= 2 {
			candidates = append(candidates, candidate{cms, e.strong, len(e.signals)})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.strong != b.strong {
			return a.strong
		}
		if a.n != b.n {
			return a.n > b.n
		}
		return a.name < b.name
	})
	if len(candidates) == 0 {
		return "unknown", nil
	}
	if len(candidates) > 1 && candidates[0].strong == candidates[1].strong && candidates[0].n == candidates[1].n {
		return "unknown", []string{"conflicting CMS evidence"}
	}
	c := candidates[0]
	signals := []string{}
	for s := range all[c.name].signals {
		signals = append(signals, s)
	}
	sort.Strings(signals)
	return c.name, signals
}
func StructuralSignature(body []byte) string {
	z := html.NewTokenizer(strings.NewReader(string(body)))
	var out strings.Builder
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken || tt == html.EndTagToken {
			t := z.Token()
			out.WriteString(fmt.Sprint(tt) + ":" + t.Data + ";")
			for _, a := range t.Attr {
				if a.Key == "type" || a.Key == "name" || a.Key == "class" {
					out.WriteString(a.Key + "=" + a.Val + ";")
				}
			}
		}
	}
	return out.String()
}
func MissingPage(body []byte) bool {
	z := html.NewTokenizer(strings.NewReader(string(body)))
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken:
			inTitle = z.Token().Data == "title"
		case html.EndTagToken:
			inTitle = false
		case html.TextToken:
			if inTitle {
				v := strings.ToLower(string(z.Text()))
				if strings.Contains(v, "404") || strings.Contains(v, "not found") || strings.Contains(v, "page missing") {
					return true
				}
			}
		}
	}
}
func readResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, BodyLimit+1))
	if e != nil {
		return nil, e
	}
	if len(body) > BodyLimit {
		return nil, fmt.Errorf("response exceeds %d bytes", BodyLimit)
	}
	return body, nil
}
func Fetch(ctx context.Context, client *http.Client, address string) (*http.Response, []byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, nil, e
	}
	req.Header.Set("User-Agent", "cnc/public-discovery")
	resp, e := client.Do(req)
	if e != nil {
		return nil, nil, e
	}
	body, e := readResponse(resp)
	return resp, body, e
}
func Classify(ctx context.Context, client *http.Client, raw string) Result {
	explicit := strings.Contains(raw, "://")
	address := raw
	if !explicit {
		address = "https://" + raw
	}
	result := Result{CMS: "unknown", URL: address, Outcome: "error"}
	resp, body, err := Fetch(ctx, client, address)
	// Only a failed HTTPS connection justifies trying HTTP. A received but
	// malformed or oversized response must not trigger a second request.
	if err != nil && resp == nil && !errors.Is(err, ErrIncompleteResponse) && !explicit && ctx.Err() == nil {
		address = "http://" + raw
		resp, body, err = Fetch(ctx, client, address)
		result.URL = address
	}
	if err != nil {
		result.Err = err
		return result
	}
	result.URL = resp.Request.URL.String()
	result.Status = resp.StatusCode
	if Blocked(resp.StatusCode, body) {
		result.Outcome = "blocked"
		return result
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Err = fmt.Errorf("HTTP %d", resp.StatusCode)
		return result
	}
	if MissingPage(body) {
		result.Err = fmt.Errorf("soft 404 homepage")
		return result
	}
	result.Outcome = "unknown"
	result.CMS, result.Signals = Analyze(body, resp.Header)
	if result.CMS != "unknown" {
		result.Outcome = "detected"
		return result
	}
	base := *resp.Request.URL
	base.RawQuery = ""
	base.Fragment = ""
	dir := path.Dir(base.Path)
	if strings.HasSuffix(base.Path, "/") {
		dir = base.Path
	}
	probes := []string{"wp-json/", "administrator/"}
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "joomla") || strings.Contains(lower, "com_") {
		probes = []string{"administrator/", "administrator/manifests/files/joomla.xml"}
	}
	if strings.Contains(lower, "/media/system/") {
		probes = []string{"administrator/", "administrator/manifests/files/joomla.xml"}
	} else if strings.Contains(lower, "drupalsettings") || strings.Contains(lower, "/sites/default/") {
		probes = []string{"core/misc/drupal.js", "user/login"}
	} else if strings.Contains(lower, "wp-content") || strings.Contains(lower, "wp-includes") {
		probes = []string{"wp-json/", "readme.html"}
	}

	for _, p := range probes {
		u := base
		u.Path = path.Join(dir, p)
		if strings.HasSuffix(p, "/") {
			u.Path += "/"
		}
		r, b, e := Fetch(ctx, client, u.String())
		if e != nil {
			if ctx.Err() != nil {
				result.Err = e
				result.Outcome = "error"
				return result
			}
			continue
		}
		if r.StatusCode < 200 || r.StatusCode >= 300 || (Blocked(r.StatusCode, b) || MissingPage(b)) {
			continue
		}
		if r.Request.URL.String() == result.URL || string(b) == string(body) || StructuralSignature(b) != "" && StructuralSignature(b) == StructuralSignature(body) {
			continue
		}
		cms, signals := Analyze(b, r.Header)
		if p == "wp-json/" {
			var payload struct {
				Namespaces []string                   `json:"namespaces"`
				Routes     map[string]json.RawMessage `json:"routes"`
			}
			if json.Unmarshal(b, &payload) == nil {
				for _, n := range payload.Namespaces {
					if n == "wp/v2" && len(payload.Routes) > 0 {
						cms = "wordpress"
						signals = []string{"WordPress REST namespaces and routes"}
					}
				}
			}
		}
		if p == "administrator/manifests/files/joomla.xml" {
			var manifest struct {
				XMLName xml.Name
				Type    string `xml:"type,attr"`
				Name    string `xml:"name"`
			}
			if xml.Unmarshal(b, &manifest) == nil && manifest.XMLName.Local == "extension" && manifest.Type == "file" && manifest.Name == "files_joomla" {
				cms = "joomla"
				signals = []string{"Joomla core manifest"}
			}
		}
		if p == "core/misc/drupal.js" && strings.Contains(string(b), "Drupal.behaviors") && strings.Contains(string(b), "Drupal.attachBehaviors") {
			cms = "drupal"
			signals = []string{"Drupal core JavaScript API"}
		}
		if p == "readme.html" {
			v := strings.ToLower(string(b))
			if strings.Contains(v, "wordpress") && strings.Contains(v, "readme") && strings.Contains(v, "<h1") && strings.Contains(v, "version ") {
				cms = "wordpress"
				signals = []string{"WordPress versioned readme"}
			}
		}

		if cms != "unknown" {
			result.CMS = cms
			result.Signals = signals
			result.Outcome = "detected"
			return result
		}
	}
	return result
}
