package watcher

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"

	"github.com/user/cnc-processor/internal/store"
)

var ErrWorkersUnavailable = errors.New("waiting for online workers before splitting input")

// A bounded snapshot keeps large inputs streaming. Within each snapshot and
// across subsequent appends the share counts differ by at most one record.
func (c *Capturer) captureSpread(ctx context.Context, name string, programs, workers []string, reader *bufio.Reader, h hash.Hash, cursor store.CaptureCursor) error {
	if len(workers) == 0 {
		return ErrWorkersUnavailable
	}
	workers = append([]string(nil), workers...)
	sort.Strings(workers)
	for i, w := range workers {
		if w == "" || (i > 0 && workers[i-1] == w) {
			return errors.New("invalid spread worker list")
		}
	}
	for {
		start := cursor.Offset
		var lines []string
		partial := false
		for cursor.Offset-start < 8<<20 {
			if err := ctx.Err(); err != nil {
				return err
			}
			var line []byte
			var readErr error
			for {
				part, err := reader.ReadSlice('\n')
				line = append(line, part...)
				if len(line) > 1<<20 {
					return errors.New("line exceeds 1 MiB; source retained")
				}
				if err == bufio.ErrBufferFull {
					continue
				}
				readErr = err
				break
			}
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			if len(line) > 0 && line[len(line)-1] == '\n' {
				lines = append(lines, string(line))
				h.Write(line)
				cursor.Offset += int64(len(line))
			} else {
				partial = len(line) > 0
			}
			if readErr == io.EOF {
				break
			}
		}
		if cursor.Offset == start {
			if partial {
				return ErrPartial
			}
			return nil
		}
		filtered, err := c.Store.FilterCaptured(ctx, name, lines)
		if err != nil {
			return err
		}
		count, err := c.Store.SpreadRecordCount(ctx, name, cursor.Generation)
		if err != nil {
			return err
		}
		groups := make([][]string, len(workers))
		for i, line := range filtered {
			j := (int(count%int64(len(workers))) + i) % len(workers)
			groups[j] = append(groups[j], line)
		}
		var batches []store.Batch
		for i, group := range groups {
			if len(group) == 0 {
				continue
			}
			data, err := json.Marshal(group)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			batches = append(batches, store.Batch{SourceID: "capture-spread", BatchID: fmt.Sprintf("%s:%d:%d:%s", cursor.Generation, start, cursor.Offset, workers[i]), Filename: name, Generation: cursor.Generation, PayloadFingerprint: hex.EncodeToString(sum[:]), RecordsJSON: string(data), PreferredWorker: workers[i]})
		}
		cursor.PrefixHash = hex.EncodeToString(h.Sum(nil))
		if err := c.Store.AcceptSpreadCaptured(ctx, batches, programs, cursor); err != nil {
			if errors.Is(err, store.ErrUnavailable) {
				return ErrPaused
			}
			return err
		}
		if partial {
			return ErrPartial
		}
	}
}
