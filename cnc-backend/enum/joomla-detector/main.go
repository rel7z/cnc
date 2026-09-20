package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"joomla-scanner/scanner"
	"joomla-scanner/writer"
)

func main() {
	domainsFile := flag.String("domains", "joomla.txt", "File containing domains (one per line)")
	pluginsFile := flag.String("plugins", "plugins.txt", "File containing extension slugs (one per line)")
	concurrency := flag.Int("c", 50, "Number of concurrent workers")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP request timeout (e.g. 10s, 15s)")
	outputDir := flag.String("output", "results", "Directory to write per-extension result files")
	flag.Parse()

	domains, err := loadLines(*domainsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Cannot read domains file %q: %v\n", *domainsFile, err)
		os.Exit(1)
	}

	entries, err := loadExtEntries(*pluginsFile)
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

	// Deduplicate entries (prefer those with version filters)
	entries = scanner.Dedup(entries)

	pinned := 0
	for _, e := range entries {
		if e.TargetVersion != "" {
			pinned++
		}
	}

	fmt.Printf("[*] Loaded %d domains, %d extensions (%d with version filter) → %d total probes\n",
		len(domains), len(entries), pinned, len(domains)*len(entries))
	fmt.Printf("[*] Concurrency: %d workers | Timeout: %s | Output: %s/\n\n",
		*concurrency, *timeout, *outputDir)

	w, err := writer.New(entries, *outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to open result files: %v\n", err)
		os.Exit(1)
	}

	resultsCh := scanner.Run(domains, entries, *concurrency, *timeout)
	for r := range resultsCh {
		w.Write(r)
	}

	summary, err := w.Finalize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Failed to finalize results: %v\n", err)
		os.Exit(1)
	}

	printSummary(summary, len(domains))
}

// loadLines reads a file and returns non-empty, non-comment trimmed lines.
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
		lines = append(lines, l)
	}
	return lines, nil
}

// loadExtEntries reads plugins.txt and parses each line into a PluginEntry.
func loadExtEntries(path string) ([]scanner.PluginEntry, error) {
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

// printSummary prints the final per-extension result table.
func printSummary(summary []writer.ExtSummary, totalDomains int) {
	fmt.Printf("\n%-36s  %-12s  %7s  %7s  %8s  %7s\n",
		"Extension", "TargetVer", "Found", "Blocked", "Matched", "Total")
	fmt.Println(strings.Repeat("─", 82))
	for _, s := range summary {
		tv := s.TargetVersion
		if tv == "" {
			tv = "(any)"
		}
		matched := "-"
		if s.TargetVersion != "" {
			matched = fmt.Sprintf("%d", s.VersionMatch)
		}
		fmt.Printf("%-36s  %-12s  %7d  %7d  %8s  %7d\n",
			s.Key, tv, s.Found, s.Blocked, matched, totalDomains)
	}
	fmt.Println(strings.Repeat("─", 82))
}
