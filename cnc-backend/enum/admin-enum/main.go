package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"admin-enum/scanner"
	"admin-enum/writer"
)

func main() {
	domainsFile := flag.String("domains", "", "File containing target domains (one per line). Required.")
	urlsFile := flag.String("urls", "url-list.txt", "File containing URL paths to probe (one per line)")
	keyFile := flag.String("key", "", "File with keywords to match in body (one per line)")
	titleFile := flag.String("title", "", "File with titles to match in <title> tag (one per line)")
	allTrue := flag.Bool("alltrue", false, "Require ALL keywords AND ALL titles to match (default: any match)")
	concurrency := flag.Int("c", 50, "Number of concurrent workers")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP request timeout (e.g. 10s, 30s)")
	output := flag.String("output", "results.txt", "Output file path")
	noBaseline := flag.Bool("no-baseline", false, "Disable catch-all / body-size dedup checks (faster, more false positives)")
	flag.Parse()

	if *domainsFile == "" {
		fatalf("[ERROR] -domains is required.\n")
	}
	domains, err := loadLines(*domainsFile)
	if err != nil {
		fatalf("[ERROR] Cannot read domains file %q: %v\n", *domainsFile, err)
	}
	if len(domains) == 0 {
		fatalf("[ERROR] Domains file %q is empty.\n", *domainsFile)
	}

	rawURLs, err := loadLines(*urlsFile)
	if err != nil {
		fatalf("[ERROR] Cannot read URL list file %q: %v\n", *urlsFile, err)
	}
	if len(rawURLs) == 0 {
		fatalf("[ERROR] URL list file %q is empty.\n", *urlsFile)
	}

	entries := make([]scanner.URLEntry, len(rawURLs))
	for i, raw := range rawURLs {
		entries[i] = scanner.ParseURLEntry(raw)
	}

	var keywords []string
	if *keyFile != "" {
		keywords, err = loadLines(*keyFile)
		if err != nil {
			fatalf("[ERROR] Cannot read key file %q: %v\n", *keyFile, err)
		}
	}

	var titles []string
	if *titleFile != "" {
		titles, err = loadLines(*titleFile)
		if err != nil {
			fatalf("[ERROR] Cannot read title file %q: %v\n", *titleFile, err)
		}
	}

	if len(keywords) == 0 && len(titles) == 0 {
		fatalf("[ERROR] At least one of -key or -title must be provided.\n")
	}

	absolute := 0
	for _, e := range entries {
		if e.IsAbsolute {
			absolute++
		}
	}
	paths := len(entries) - absolute

	fmt.Printf("[*] Domains   : %d  (%s)\n", len(domains), *domainsFile)
	fmt.Printf("[*] URL paths : %d  (%d relative, %d absolute)\n", len(entries), paths, absolute)
	fmt.Printf("[*] Keywords  : %d\n", len(keywords))
	fmt.Printf("[*] Titles    : %d\n", len(titles))
	fmt.Printf("[*] Probes    : %d\n", len(domains)*paths+absolute)
	baselineStatus := "enabled"
	if *noBaseline {
		baselineStatus = "disabled"
	}
	fmt.Printf("[*] Workers   : %d  |  Timeout: %s  |  Output: %s\n", *concurrency, *timeout, *output)
	fmt.Printf("[*] Baseline  : %s\n\n", baselineStatus)

	w, err := writer.New(*output)
	if err != nil {
		fatalf("[ERROR] Cannot open output file: %v\n", err)
	}

	resultsCh := scanner.Run(domains, entries, keywords, titles, *allTrue, *concurrency, *timeout, *noBaseline)
	for r := range resultsCh {
		w.Write(r)
	}

	summary, err := w.Finalize()
	if err != nil {
		fatalf("[ERROR] Failed to finalize output: %v\n", err)
	}

	fmt.Printf("\n%s\n", strings.Repeat("─", 60))
	fmt.Printf("  Confirmed : %d\n", summary.Confirmed)
	fmt.Printf("  Protected : %d\n", summary.Protected)
	fmt.Printf("  Total     : %d\n", summary.Confirmed+summary.Protected)
	fmt.Printf("  Saved to  : %s\n", summary.OutputPath)
	fmt.Printf("%s\n", strings.Repeat("─", 60))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

func loadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := strings.Split(string(data), "\n")
	var out []string
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}
