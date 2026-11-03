package distributed

import (
	"fmt"
	"strings"
	"time"
)

const workerRejectionBackoff = 5 * time.Second

type workerRetry struct {
	until  time.Time
	reason string
}

// Never expose arbitrary response bodies: they may contain private data from
// an intermediary. Only fixed control-plane messages are suitable for logs/UI.
func safeBrokerReason(body []byte) string {
	reason := strings.TrimSpace(string(body))
	switch reason {
	case "worker unavailable", "assignment conflict", "program version unavailable",
		"worker capacity unavailable", "worker send queue unavailable",
		"waiting for a CNC task slot", "waiting for fresh resource telemetry",
		"waiting for running job memory burst to finish", "waiting for a processor slot",
		"waiting for CPU usage below admission limit", "waiting for reserved CPU capacity",
		"waiting for shared job memory budget", "waiting for available memory",
		"invalid assignment", "invalid worker assignment", "invalid task", "Unauthorized":
		return reason
	default:
		return "unrecognized broker response (body withheld)"
	}
}

func (c *Controller) deferWorker(id string, err error) string {
	reason := fmt.Sprintf("worker %s: %v; retry after cooldown", id, err)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workerRetry == nil {
		c.workerRetry = map[string]workerRetry{}
	}
	c.workerRetry[id] = workerRetry{until: time.Now().Add(workerRejectionBackoff), reason: reason}
	return reason
}

func (c *Controller) workerRetryReason(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	retry, ok := c.workerRetry[id]
	if !ok {
		return ""
	}
	if !time.Now().Before(retry.until) {
		delete(c.workerRetry, id)
		return ""
	}
	return retry.reason
}
