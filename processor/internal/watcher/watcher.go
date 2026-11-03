package watcher

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

// Capturer serializes periodic capture and CNC's pre-deletion barrier.
// The database commits each batch, its jobs and its cursor in one transaction.
type Capturer struct {
	Store  *store.Store
	Dir    string
	Routes map[string][]string
	// nil retains the legacy batch reader. Spread mode waits for a worker list
	// rather than consuming input with nowhere to place its shares.
	SpreadWorkers func(string) ([]string, bool)
	mu            sync.Mutex
	verified      map[string]string
}

var ErrPaused = errors.New("processing is paused; source retained")
var ErrPartial = errors.New("source has an incomplete line; source retained")

func (c *Capturer) ResetCheckpoints(ctx context.Context) (store.CheckpointReset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Store.ResetCaptureCheckpoints(ctx)
}

func (c *Capturer) Start(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		c.scan(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Capturer) scan(ctx context.Context) {
	enabled, err := c.Store.IsEnabled(ctx)
	if err != nil || !enabled {
		return
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		log.Printf("processor scan: %v", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if err := c.Capture(ctx, entry.Name()); err != nil && !errors.Is(err, ErrPartial) && !errors.Is(err, ErrPaused) && !errors.Is(err, ErrWorkersUnavailable) {
			log.Printf("processor capture %s: %v", entry.Name(), err)
		}
	}
}

func (c *Capturer) Capture(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name == "" || name == "." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return errors.New("invalid source filename")
	}
	var programs []string
	var err error
	if c.Routes != nil {
		programs = c.Routes[name]
	} else {
		programs, err = getProgramsForFile(name)
	}
	if err != nil {
		return err
	}
	if len(programs) == 0 {
		return nil
	}
	enabled, err := c.Store.IsEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrPaused
	}
	path := filepath.Join(c.Dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source must be a regular file")
	}
	// O_NOFOLLOW prevents swapping a symlink between Lstat and Open.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	cursor, err := c.Store.CaptureCursor(ctx, path)
	if err != nil {
		return err
	}
	// Only skip verification for the exact fully consumed snapshot. Change time
	// also detects same-size overwrites whose modification time was restored.
	snapshot := fmt.Sprintf("%s:%d:%d:%d:%d:%s:%s", identity, info.Size(), info.ModTime().UnixNano(), fileChangeTime(info), cursor.Offset, cursor.Generation, cursor.PrefixHash)
	if cursor.Offset == info.Size() && c.verified[path] == snapshot {
		return nil
	}
	h := sha256.New()
	if cursor.Identity != identity || cursor.Offset > info.Size() {
		cursor.Offset = 0
		cursor.Generation = ""
	}
	if cursor.Offset > 0 {
		if _, err = io.CopyN(h, f, cursor.Offset); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != cursor.PrefixHash {
			cursor.Offset = 0
			cursor.Generation = ""
		}
	}
	if cursor.Generation == "" {
		cursor.Generation = uuid.NewString()
		cursor.Identity = identity
		h.Reset()
		if _, err = f.Seek(0, 0); err != nil {
			return err
		}
	}
	if cursor.Offset == info.Size() {
		if c.verified == nil {
			c.verified = map[string]string{}
		}
		c.verified[path] = snapshot
		return nil
	}
	// Read only the observed snapshot. Subsequent appends are captured next time.
	reader := bufio.NewReader(io.LimitReader(f, info.Size()-cursor.Offset))
	if c.SpreadWorkers != nil {
		if workers, spread := c.SpreadWorkers(name); spread {
			return c.captureSpread(ctx, name, programs, workers, reader, h, cursor)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lines := make([]string, 0, 1000)
		start := cursor.Offset
		partial := false
		batchBytes := 0
		batchFull := false
		for len(lines) < 1000 && batchBytes < 1<<20 && !batchFull {
			// Bound individual records to 1 MiB. Never advance a cursor past a failed read.
			var line []byte
			for {
				part, e := reader.ReadSlice('\n')
				line = append(line, part...)
				if len(line) > 1<<20 {
					return errors.New("line exceeds 1 MiB; source retained")
				}
				if e == bufio.ErrBufferFull {
					continue
				}
				if e != nil && e != io.EOF {
					return e
				}
				if len(line) > 0 && line[len(line)-1] == '\n' {
					if batchBytes+len(line) > 1<<20 && len(lines) > 0 {
						if _, seekErr := f.Seek(cursor.Offset, 0); seekErr != nil {
							return seekErr
						}
						reader = bufio.NewReader(io.LimitReader(f, info.Size()-cursor.Offset))
						batchFull = true
						break
					}
					lines = append(lines, string(line))
					batchBytes += len(line)
					h.Write(line)
					cursor.Offset += int64(len(line))
				} else {
					partial = len(line) > 0
				}
				err = e
				break
			}
			if err == io.EOF {
				break
			}
		}
		if len(lines) > 0 {
			filtered, filterErr := c.Store.FilterCaptured(ctx, name, lines)
			if filterErr != nil {
				return filterErr
			}
			data, _ := json.Marshal(filtered)
			sum := sha256.Sum256(data)
			cursor.PrefixHash = hex.EncodeToString(h.Sum(nil))
			batch := store.Batch{SourceID: "capture", BatchID: fmt.Sprintf("%s:%d:%d", cursor.Generation, start, cursor.Offset), Filename: name, Generation: cursor.Generation, PayloadFingerprint: hex.EncodeToString(sum[:]), RecordsJSON: string(data)}
			routed := programs
			if len(filtered) == 0 {
				routed = nil
			}
			if err := c.Store.AcceptCaptured(ctx, batch, routed, cursor); err != nil {
				return err
			}
		}
		if partial {
			return ErrPartial
		}
		if err == io.EOF {
			return nil
		}
	}
}

func getProgramsForFile(filename string) ([]string, error) {
	appDir := os.Getenv("CNC_APP_DIR")
	if appDir == "" {
		appDir = "."
	}
	data, err := os.ReadFile(filepath.Join(appDir, "config.json"))
	if err != nil {
		return nil, err
	}
	var config cluster.Config
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if config, err = config.ResolveManifests(appDir); err != nil {
		return nil, err
	}
	return config.Routes[filename], nil
}

// Stop capture briefly while the editor atomically changes the registry and routes.
func (c *Capturer) Reconfigure(change func() (map[string][]string, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	routes, err := change()
	if err == nil {
		c.Routes = routes
	}
	return err
}
