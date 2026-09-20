// Package writer provides a streaming, real-time results writer for admin-enum.
// All confirmed and protected hits are flushed to disk immediately as they arrive.
package writer

import (
	"bufio"
	"fmt"
	"os"
	"sync"

	"admin-enum/scanner"
)

// Writer streams scan results into a single flat results.txt file.
type Writer struct {
	f       *os.File
	buf     *bufio.Writer
	mu      sync.Mutex
	path    string

	confirmed int
	protected int
}

func New(outputPath string) (*Writer, error) {
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("create output file %q: %w", outputPath, err)
	}

	w := &Writer{
		f:    f,
		buf:  bufio.NewWriterSize(f, 8192),
		path: outputPath,
	}

	return w, nil
}

// Write appends a single result to the output file if it is worth recording
// (CONFIRMED or PROTECTED). Other statuses are silently dropped.
// Safe to call from multiple goroutines.
func (w *Writer) Write(r scanner.Result) {
	if r.Status != scanner.StatusConfirmed && r.Status != scanner.StatusProtected {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	fmt.Fprintln(w.buf, r.FullURL)
	w.buf.Flush()

	switch r.Status {
	case scanner.StatusConfirmed:
		w.confirmed++
	case scanner.StatusProtected:
		w.protected++
	}
}

// Summary holds final counts returned by Finalize.
type Summary struct {
	Confirmed int
	Protected int
	OutputPath string
}

func (w *Writer) Finalize() (Summary, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.buf.Flush(); err != nil {
		return Summary{}, fmt.Errorf("flush: %w", err)
	}
	if err := w.f.Close(); err != nil {
		return Summary{}, fmt.Errorf("close: %w", err)
	}

	return Summary{
		Confirmed:  w.confirmed,
		Protected:  w.protected,
		OutputPath: w.path,
	}, nil
}
