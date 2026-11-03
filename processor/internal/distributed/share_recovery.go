package distributed

import (
	"context"
	"log"
	"time"

	"github.com/user/cnc-processor/cluster"
)

const shareRecoveryGrace = time.Minute

func (c *Controller) recoverWorkerShares(ctx context.Context, workers []cluster.Worker, now time.Time) error {
	assigned, err := c.Store.PendingShareWorkers(ctx)
	if err != nil {
		return err
	}
	available := map[string]bool{}
	var targets []string
	for _, w := range workers {
		if (w.Status == "online" || w.Status == "busy") && w.Protocol == cluster.ProtocolVersion && w.MaxTasks > 0 && freshCPU(w) {
			available[w.ID] = true
			targets = append(targets, w.ID)
		}
	}
	if c.unavailableSince == nil {
		c.unavailableSince = map[string]time.Time{}
	}
	pending := map[string]bool{}
	for _, id := range assigned {
		pending[id] = true
		if available[id] {
			delete(c.unavailableSince, id)
			continue
		}
		since, ok := c.unavailableSince[id]
		if !ok {
			c.unavailableSince[id] = now
			continue
		}
		if now.Sub(since) < shareRecoveryGrace || len(targets) == 0 {
			continue
		}
		count, err := c.Store.ReassignWaitingShares(ctx, id, targets)
		if err != nil {
			return err
		}
		if count > 0 {
			log.Printf("Recovered %d queued processor shares from unavailable worker %s", count, id)
		}
	}
	for id := range c.unavailableSince {
		if !pending[id] {
			delete(c.unavailableSince, id)
		}
	}
	return nil
}
