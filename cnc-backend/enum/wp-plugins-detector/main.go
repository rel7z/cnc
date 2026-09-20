package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"wp-scanner/scanner"
	"wp-scanner/writer"
)

func main() {
	domainsFile := flag.String("domains", "wordpress.txt", "File containing domains (one per line)")
	pluginsFile := flag.String("plugins", "plugins.txt", "File containing plugin slugs (one per line, optionally slug:version)")
	concurrency := flag.Int("c", 50, "Number of concurrent workers")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP request timeout (e.g. 10s, 15s)")
	outputDir := flag.String("output", "results", "Directory to write per-plugin result files")
	flag.Parse()

	domains, err := loadLines(*domainsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot read domains file %q: %v\n", *domainsFile, err)
		os.Exit(1)
	}

	entries, err := loadPluginEntries(*pluginsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot read plugins file %q: %v\n", *pluginsFile, err)
		os.Exit(1)
	}

	if len(domains) == 0 {
		fmt.Fprintln(os.Stderr, "[ERROR] Domains file is empty.")
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "[ERROR] Plugins file is empty.")
		os.Exit(1)
	}

	slugs := scanner.Slugs(entries)

	pinned := 0
	for _, e := range entries {
		if e.TargetVersion != "" {
			pinned++
		}
	}

	fmt.Printf("[*] Loaded %d domains, %d plugins (%d with version filter) → %d total probes\n",
		len(domains), len(slugs), pinned, len(domains)*len(slugs))
	fmt.Printf("[*] Concurrency: %d workers | Timeout: %s | Output: %s/\n\n",
		*concurrency, *timeout, *outputDir)

	// Open result files upfront — writes happen in real time as results arrive
	w, err := writer.New(entries, *outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to open result files: %v\n", err)
		os.Exit(1)
	}

	// Stream results from the scanner directly into the writer
	resultsCh := scanner.Run(domains, slugs, *concurrency, *timeout)
	for r := range resultsCh {
		w.Write(r)
	}

	// Flush, close files, collect final counts
	summary, err := w.Finalize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to finalize results: %v\n", err)
		os.Exit(1)
	}

	printSummary(summary, len(domains))
}

// loadLines reads a file and returns non-empty, non-comment lines trimmed of whitespace.
// Lines that start with a URL scheme (http:// or https://) have it stripped so the
// scanner always works with bare hostnames.
func loadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := strings.Split(string(data), "\n")
	var lines []string
	for _, l := range raw {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		// Strip scheme so both "example.com" and "https://example.com" work
		l = strings.TrimPrefix(l, "https://")
		l = strings.TrimPrefix(l, "http://")
		// Skip lines that still contain "://" — they were malformed to begin with
		if strings.Contains(l, "://") {
			fmt.Fprintf(os.Stderr, "[WARN] Skipping malformed domain line: %q\n", l)
			continue
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// loadPluginEntries reads plugins.txt and parses each line into a PluginEntry.
func loadPluginEntries(path string) ([]scanner.PluginEntry, error) {
	lines, err := loadLines(path)
	if err != nil {
		return nil, err
	}
	entries := make([]scanner.PluginEntry, len(lines))
	for i, l := range lines {
		entries[i] = scanner.ParsePluginEntry(l)
	}
	return entries, nil
}

// printSummary prints the final per-plugin result table.
func printSummary(summary []writer.PluginSummary, totalDomains int) {
	fmt.Printf("\n%-30s  %-12s  %7s  %7s  %8s  %7s\n",
		"Plugin", "TargetVer", "Found", "Blocked", "Matched", "Total")
	fmt.Println(strings.Repeat("─", 80))
	for _, s := range summary {
		tv := s.TargetVersion
		if tv == "" {
			tv = "(any)"
		}
		matched := "-"
		if s.TargetVersion != "" {
			matched = fmt.Sprintf("%d", s.VersionMatch)
		}
		fmt.Printf("%-30s  %-12s  %7d  %7d  %8s  %7d\n",
			s.Slug, tv, s.Found, s.Blocked, matched, totalDomains)
	}
	fmt.Println(strings.Repeat("─", 80))
}
