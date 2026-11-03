package cnc

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

func isDorkTask(task *Task) bool {
	command, _ := task.Payload["command"].(string)
	words := strings.Fields(command)
	return len(words) > 0 && filepath.Base(strings.Trim(words[0], "\"'")) == "dork2"
}

// Called under s.mu. Provider capacity is shared across jobs and workers.
func (s *Server) dorkCapacityReason(task *Task) string {
	if !isDorkTask(task) {
		return ""
	}
	active := 0
	for _, other := range s.tasks {
		if (other.Status == TaskStatusAssigned || other.Status == TaskStatusRunning) && isDorkTask(other) {
			active++
		}
	}
	if active >= 5 {
		return "waiting for Apify capacity (maximum 5 active CNC search tasks)"
	}
	return ""
}

func (s *Server) retryCapacityTask(task *Task) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		select {
		case <-ctx.Done():
		case s.taskQueue <- task:
		}
	}()
}
