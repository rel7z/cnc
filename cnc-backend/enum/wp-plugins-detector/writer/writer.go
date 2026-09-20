// Package writer provides a real-time, streaming result writer for wp-scanner.
// Each plugin gets its own open file handle. Results are flushed to disk
// immediately as probes complete — no buffering until scan end.
//
// Files are created lazily: a result file is only written to disk when the
// first matching result arrives. Plugins with zero hits produce no files.
package writer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wp-scanner/scanner"
)

// PluginSummary holds the aggregated counts for one plugin after a full scan.
type PluginSummary struct {
	Slug          string
	TargetVersion string
	Found         int
	Blocked       int
	VersionMatch  int // domains that matched TargetVersion exactly
}

// pluginHandle holds open file handles and live counters for one plugin slug.
type pluginHandle struct {
	entry     scanner.PluginEntry
	outputDir string
	startedAt string // timestamp captured at New(), reused in lazy headers

	// <slug>.txt — all found + blocked entries (nil until first hit)
	fullFile *os.File
	fullBuf  *bufio.Writer

	// <slug>-<version>.txt — domain-only, version-matched (nil until first version match)
	filtFile *os.File
	filtBuf  *bufio.Writer

	// counters
	found        int
	blocked      int
	versionMatch int

	mu sync.Mutex // guards file writes and counters for this handle
}

// Writer manages per-plugin streaming file handles.
type Writer struct {
	outputDir string
	handles   map[string]*pluginHandle
	mu        sync.Mutex // guards handles map
}

// New creates a Writer and registers all plugin entries.
// No files are created at this point — creation is deferred until the first
// matching result arrives for each plugin.
func New(entries []scanner.PluginEntry, outputDir string) (*Writer, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	timestamp := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")

	w := &Writer{
		outputDir: outputDir,
		handles:   make(map[string]*pluginHandle, len(entries)),
	}

	// Build slug → entry map, preferring entries that have a TargetVersion.
	entryMap := make(map[string]scanner.PluginEntry, len(entries))
	for _, e := range entries {
		existing, ok := entryMap[e.Slug]
		if !ok || (e.TargetVersion != "" && existing.TargetVersion == "") {
			entryMap[e.Slug] = e
		}
	}

	for _, e := range entryMap {
		w.handles[e.Slug] = &pluginHandle{
			entry:     e,
			outputDir: outputDir,
			startedAt: timestamp,
		}
	}

	return w, nil
}

// ensureFullOpen creates and writes the header for <slug>.txt on first call.
// Must be called with h.mu held.
func (h *pluginHandle) ensureFullOpen() error {
	if h.fullFile != nil {
		return nil
	}
	path := filepath.Join(h.outputDir, h.entry.Slug+".txt")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	h.fullFile = f
	h.fullBuf = bufio.NewWriterSize(f, 4096)

	tv := h.entry.TargetVersion
	if tv == "" {
		tv = "(any)"
	}
	fmt.Fprintf(h.fullBuf, "# Plugin        : %s\n", h.entry.Slug)
	fmt.Fprintf(h.fullBuf, "# Version filter: %s\n", tv)
	fmt.Fprintf(h.fullBuf, "# Started       : %s\n", h.startedAt)
	fmt.Fprintln(h.fullBuf, strings.Repeat("-", 40))
	h.fullBuf.Flush()
	return nil
}

// ensureFiltOpen creates and writes the header for <slug>-<version>.txt on first call.
// Must be called with h.mu held.
func (h *pluginHandle) ensureFiltOpen() error {
	if h.filtFile != nil {
		return nil
	}
	safeVer := strings.ReplaceAll(h.entry.TargetVersion, "/", "-")
	path := filepath.Join(h.outputDir, h.entry.Slug+"-"+safeVer+".txt")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	h.filtFile = f
	h.filtBuf = bufio.NewWriterSize(f, 4096)

	fmt.Fprintf(h.filtBuf, "# Plugin  : %s\n", h.entry.Slug)
	fmt.Fprintf(h.filtBuf, "# Version : %s\n", h.entry.TargetVersion)
	fmt.Fprintf(h.filtBuf, "# Started : %s\n", h.startedAt)
	fmt.Fprintln(h.filtBuf, strings.Repeat("-", 40))
	h.filtBuf.Flush()
	return nil
}

// Write appends a single probe result to the appropriate file(s) immediately.
// Files are created on demand — nothing is written if the plugin has no hits.
// Safe to call from multiple goroutines concurrently.
func (w *Writer) Write(r scanner.Result) {
	w.mu.Lock()
	h, ok := w.handles[r.Slug]
	w.mu.Unlock()
	if !ok {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	switch r.Status {
	case scanner.StatusFound:
		h.found++
		if err := h.ensureFullOpen(); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
			return
		}
		fmt.Fprintln(h.fullBuf, r.Domain)
		h.fullBuf.Flush()

		// Version-filtered file: only exact version matches
		if h.entry.TargetVersion != "" && r.Version == h.entry.TargetVersion {
			h.versionMatch++
			if err := h.ensureFiltOpen(); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
				return
			}
			fmt.Fprintln(h.filtBuf, r.Domain)
			h.filtBuf.Flush()
		}

	case scanner.StatusBlocked:
		h.blocked++
	}
}

// Finalize flushes all buffers, appends a footer with final counts to each open
// file, and closes handles. Plugins with zero hits have no files — they are
// included in the summary with zero counts only.
// Call once after all results have been written.
func (w *Writer) Finalize() ([]PluginSummary, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var summaries []PluginSummary
	timestamp := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")

	for _, h := range w.handles {
		h.mu.Lock()

		// Only write footer if the file was actually opened (i.e. had hits)
		if h.fullFile != nil {
			fmt.Fprintln(h.fullBuf, strings.Repeat("-", 40))
			fmt.Fprintf(h.fullBuf, "# Finished : %s\n", timestamp)
			fmt.Fprintf(h.fullBuf, "# Found    : %d\n", h.found)
			h.fullBuf.Flush()
			h.fullFile.Close()
		}

		if h.filtFile != nil {
			fmt.Fprintln(h.filtBuf, strings.Repeat("-", 40))
			fmt.Fprintf(h.filtBuf, "# Finished : %s\n", timestamp)
			fmt.Fprintf(h.filtBuf, "# Matches  : %d domains\n", h.versionMatch)
			h.filtBuf.Flush()
			h.filtFile.Close()
		}

		summaries = append(summaries, PluginSummary{
			Slug:          h.entry.Slug,
			TargetVersion: h.entry.TargetVersion,
			Found:         h.found,
			Blocked:       h.blocked,
			VersionMatch:  h.versionMatch,
		})

		h.mu.Unlock()
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Slug < summaries[j].Slug
	})

	return summaries, nil
}
