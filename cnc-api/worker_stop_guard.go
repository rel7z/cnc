package cnc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func workerProcessStart(proc string, pid int) (string, error) {
	raw, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	end := strings.LastIndex(string(raw), ") ")
	if end < 0 {
		return "", fmt.Errorf("invalid worker process metadata")
	}
	fields := strings.Fields(string(raw)[end+2:])
	if len(fields) < 20 {
		return "", fmt.Errorf("incomplete worker process metadata")
	}
	return fields[19], nil
}

// Count children across every Go OS thread; /task/<main-pid>/children alone
// misses subprocesses spawned by other threads. Never inspect command lines.
func workerChildCount(proc string, pid int) (int, error) {
	threads, err := os.ReadDir(filepath.Join(proc, strconv.Itoa(pid), "task"))
	if err != nil {
		return 0, err
	}
	children := map[string]bool{}
	for _, thread := range threads {
		raw, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "task", thread.Name(), "children"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, child := range strings.Fields(string(raw)) {
			children[child] = true
		}
	}
	return len(children), nil
}

// DrainWorkerProcess is the stable systemd ExecStop guard. New workers handle
// TERM by draining; old workers must first have no children for ten seconds.
// It only reads process/capability metadata, never tool files or task contents.
func DrainWorkerProcess(ctx context.Context, pid int, dataDir string) error {
	if pid <= 1 || pid == os.Getpid() {
		return fmt.Errorf("invalid worker PID")
	}
	start, err := workerProcessStart("/proc", pid)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var capability drainCapability
	if raw, e := os.ReadFile(filepath.Join(dataDir, "worker-drain-ready.json")); e == nil {
		_ = json.Unmarshal(raw, &capability)
	}
	modern := capability.PID == pid && capability.Start == start
	var quietSince time.Time
	if !modern {
		log.Printf("Legacy worker %d: deferring shutdown until active child processes finish", pid)
	}
	for !modern {
		current, e := workerProcessStart("/proc", pid)
		if os.IsNotExist(e) || e == nil && current != start {
			return nil
		}
		if e != nil {
			return e
		}
		children, e := workerChildCount("/proc", pid)
		if e != nil {
			return e
		}
		if children == 0 {
			if quietSince.IsZero() {
				quietSince = time.Now()
			}
			if time.Since(quietSince) >= 10*time.Second {
				break
			}
		} else {
			quietSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	// Revalidate PID identity immediately before signalling.
	current, e := workerProcessStart("/proc", pid)
	if os.IsNotExist(e) || e == nil && current != start {
		return nil
	}
	if e != nil {
		return e
	}
	if err = syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	for {
		current, e = workerProcessStart("/proc", pid)
		if os.IsNotExist(e) || e == nil && current != start {
			return nil
		}
		if e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
