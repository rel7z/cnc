package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"cnc-enum/version"
)

type checkpointLine struct {
	Name string
	Text string
}

type checkpointTarget struct {
	Target          version.TargetLocator
	Lines           []checkpointLine
	Records         []version.ResultRecordV1
	Errors          int64
	Successful      bool
	HomepageOutcome string `json:",omitempty"`
}

type checkpointWrite struct {
	data []byte
	done chan error
}

type scanCheckpoint struct {
	writes chan checkpointWrite
	closed chan struct{}
}

func checkpointBinding(options pipelineOptions, wp, joomla []string) (string, error) {
	input, err := os.Open(options.Input)
	if err != nil {
		return "", err
	}
	defer input.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, input); err != nil {
		return "", err
	}
	metadata, err := json.Marshal([]any{
		1, options.JobID, options.ShardID, options.Digest, fmt.Sprintf("%x", digest.Sum(nil)),
		options.Timeout, options.WPPlugins != "", wp, options.JoomlaExts != "", joomla,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x\n", sha256.Sum256(metadata)), nil
}

func openScanCheckpoint(directory, binding string, restore func(checkpointTarget) error) (_ *scanCheckpoint, err error) {
	path := filepath.Join(directory, ".checkpoint-v1")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			file.Close()
		}
	}()
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(file)
	header, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) {
		// A crash during initialization cannot have committed any targets.
		if err = file.Truncate(0); err != nil {
			return nil, err
		}
		if _, err = file.WriteAt([]byte(binding), 0); err != nil {
			return nil, err
		}
		if err = file.Sync(); err != nil {
			return nil, err
		}
		for _, path := range []string{directory, filepath.Dir(directory)} {
			dir, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			err = dir.Sync()
			dir.Close()
			if err != nil {
				return nil, err
			}
		}
		_, err = file.Seek(int64(len(binding)), io.SeekStart)
		if err != nil {
			return nil, err
		}
		return startScanCheckpoint(file), nil
	}
	if err != nil {
		return nil, err
	}
	if header != binding {
		return nil, fmt.Errorf("checkpoint does not match scan inputs")
	}
	offset := int64(len(header))
	for {
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			if len(line) > 0 {
				if err := file.Truncate(offset); err != nil {
					return nil, err
				}
			}
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				return nil, err
			}
			return startScanCheckpoint(file), nil
		}
		if readErr != nil {
			return nil, readErr
		}
		var target checkpointTarget
		if err := json.Unmarshal(line, &target); err != nil {
			return nil, fmt.Errorf("invalid committed checkpoint record: %w", err)
		}
		if err := restore(target); err != nil {
			return nil, err
		}
		offset += int64(len(line))
	}
}

// Group concurrent commits so fsync latency does not serialize all 256
// scanner slots. A target is reported only after its whole batch is durable.
func startScanCheckpoint(file *os.File) *scanCheckpoint {
	checkpoint := &scanCheckpoint{
		writes: make(chan checkpointWrite, 64),
		closed: make(chan struct{}),
	}
	go func() {
		defer close(checkpoint.closed)
		defer file.Close()
		var failed error
		batch := make([]checkpointWrite, 0, 64)
		writer := bufio.NewWriterSize(file, 64*1024)
		for first := range checkpoint.writes {
			batch = append(batch[:0], first)
			timer := time.NewTimer(5 * time.Millisecond)
		gather:
			for len(batch) < cap(batch) {
				select {
				case next := <-checkpoint.writes:
					batch = append(batch, next)
				case <-timer.C:
					break gather
				}
			}
			timer.Stop()
			if failed == nil {
				for _, request := range batch {
					if _, failed = writer.Write(request.data); failed != nil {
						break
					}
				}
				if failed == nil {
					failed = writer.Flush()
				}
				if failed == nil {
					failed = file.Sync()
				}
			}
			for _, request := range batch {
				request.done <- failed
			}
			clear(batch)
		}
	}()
	return checkpoint
}

func (c *scanCheckpoint) append(target checkpointTarget) error {
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	c.writes <- checkpointWrite{data: append(data, '\n'), done: done}
	return <-done
}

func (c *scanCheckpoint) close() {
	close(c.writes)
	<-c.closed
}
