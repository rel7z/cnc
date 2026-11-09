package cnc

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// DrainAndStop withdraws capacity without cancelling current execution or its
// lease. Tasks already in the transport are allowed to finish as well.
func (w *WorkerAgent) DrainAndStop(ctx context.Context) error {
	w.draining.Store(true)
	quiet, poll := w.drainQuiet, w.drainPoll
	if quiet <= 0 {
		quiet = 10 * time.Second
	}
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	log.Printf("Worker %s draining: no new capacity; active tasks retain their leases", w.config.WorkerID)
	var idleSince, advertised time.Time
	for {
		now := time.Now()
		if advertised.IsZero() || now.Sub(advertised) >= 5*time.Second {
			if err := w.register(); err != nil {
				idleSince = time.Time{}
			} else {
				advertised = now
			}
		}
		w.mu.RLock()
		active := len(w.tasks)
		w.mu.RUnlock()
		if active == 0 && !advertised.IsZero() {
			if idleSince.IsZero() {
				idleSince = now
			}
			if now.Sub(idleSince) >= quiet {
				log.Printf("Worker %s drained; stopping after final result delivery", w.config.WorkerID)
				w.Stop()
				return nil
			}
		} else {
			idleSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err() // A timed-out observer must not kill work.
		case <-w.ctx.Done():
			return w.ctx.Err()
		case <-time.After(poll):
		}
	}
}

type drainCapability struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func (w *WorkerAgent) deliverTaskResult(id string, result *TaskResult, reason string) {
	w.mu.RLock()
	task := w.tasks[id]
	reconnectable := task != nil && task.Type == "enum"
	w.mu.RUnlock()
	for w.ctx.Err() == nil {
		if err := w.sendResult(id, result, reason); err == nil {
			return
		}
		if !w.draining.Load() && !reconnectable {
			return
		}
		// Keep the task reservation while the final transport is unavailable.
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (w *WorkerAgent) writeDrainCapability() error {
	start, err := workerProcessStart("/proc", os.Getpid())
	if err != nil {
		return nil
	} // Non-Linux workers do not use the systemd guard.
	raw, err := json.Marshal(drainCapability{PID: os.Getpid(), Start: start})
	if err != nil {
		return err
	}
	path := filepath.Join(w.config.DataDir, "worker-drain-ready.json")
	file, err := os.CreateTemp(w.config.DataDir, ".worker-drain-ready-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
