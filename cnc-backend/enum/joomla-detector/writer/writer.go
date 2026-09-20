// Package writer provides a real-time, streaming result writer for joomla-scanner.
// Each extension gets its own output file, created lazily on the first hit.
// Output files contain only domain names — one per line, no headers or metadata.
package writer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"joomla-scanner/scanner"
)

// ExtSummary holds aggregated counts for one extension after a full scan.
type ExtSummary struct {
	Key           string
	TargetVersion string
	Found         int
	Blocked       int
	VersionMatch  int
}

// extHandle manages an open file handle and counters for a single extension.
type extHandle struct {
	entry     scanner.PluginEntry
	outputDir string

	// <key>.txt — found + blocked domains (nil until first hit)
	file *os.File
	buf  *bufio.Writer

	// <key>-<version>.txt — version-matched domains only (nil until first match)
	filtFile *os.File
	filtBuf  *bufio.Writer

	found        int
	blocked      int
	versionMatch int

	mu sync.Mutex
}

// Writer manages per-extension streaming file handles.
type Writer struct {
	outputDir string
	handles   map[string]*extHandle
	mu        sync.Mutex
}

// New initialises a Writer for the given extensions.
// No files are created until the first matching result arrives.
func New(entries []scanner.PluginEntry, outputDir string) (*Writer, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	w := &Writer{
		outputDir: outputDir,
		handles:   make(map[string]*extHandle, len(entries)),
	}

	// De-duplicate: prefer entries with a TargetVersion.
	entryMap := make(map[string]scanner.PluginEntry, len(entries))
	for _, e := range entries {
		existing, ok := entryMap[e.Key()]
		if !ok || (e.TargetVersion != "" && existing.TargetVersion == "") {
			entryMap[e.Key()] = e
		}
	}

	for _, e := range entryMap {
		w.handles[e.Key()] = &extHandle{
			entry:     e,
			outputDir: outputDir,
		}
	}

	return w, nil
}

// safeFilename converts a key like "plugin:system:astroid" to "plugin_system_astroid".
func safeFilename(key string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return r.Replace(key)
}

func (h *extHandle) ensureOpen() error {
	if h.file != nil {
		return nil
	}
	path := filepath.Join(h.outputDir, safeFilename(h.entry.Key())+".txt")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	h.file = f
	h.buf = bufio.NewWriterSize(f, 4096)
	return nil
}

func (h *extHandle) ensureFiltOpen() error {
	if h.filtFile != nil {
		return nil
	}
	safeVer := strings.ReplaceAll(h.entry.TargetVersion, "/", "-")
	path := filepath.Join(h.outputDir, safeFilename(h.entry.Key())+"-"+safeVer+".txt")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	h.filtFile = f
	h.filtBuf = bufio.NewWriterSize(f, 4096)
	return nil
}

// Write appends a domain to the appropriate file(s) immediately.
// Safe to call from multiple goroutines concurrently.
func (w *Writer) Write(r scanner.Result) {
	w.mu.Lock()
	h, ok := w.handles[r.Key]
	w.mu.Unlock()
	if !ok {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	switch r.Status {
	case scanner.StatusFound:
		h.found++
		if err := h.ensureOpen(); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
			return
		}
		fmt.Fprintln(h.buf, r.Domain)
		h.buf.Flush()

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
		if err := h.ensureOpen(); err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] %v\n", err)
			return
		}
		fmt.Fprintln(h.buf, r.Domain)
		h.buf.Flush()
	}
}

// Finalize flushes all buffers, closes handles, and returns a sorted summary.
// Call once after all results have been written.
func (w *Writer) Finalize() ([]ExtSummary, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var summaries []ExtSummary

	for _, h := range w.handles {
		h.mu.Lock()

		if h.file != nil {
			h.buf.Flush()
			h.file.Close()
		}
		if h.filtFile != nil {
			h.filtBuf.Flush()
			h.filtFile.Close()
		}

		summaries = append(summaries, ExtSummary{
			Key:           h.entry.Key(),
			TargetVersion: h.entry.TargetVersion,
			Found:         h.found,
			Blocked:       h.blocked,
			VersionMatch:  h.versionMatch,
		})

		h.mu.Unlock()
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Key < summaries[j].Key
	})

	return summaries, nil
}
