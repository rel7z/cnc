package distributed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

type cpuEmergency struct {
	NextCheck time.Time `json:"next_check"`
}

func freshCPU(w cluster.Worker) bool {
	return w.Resources.SampledAt > 0 && time.Since(time.UnixMilli(w.Resources.SampledAt)) <= 15*time.Second
}

func (c *Controller) balancedWorkerLimits(w cluster.Worker) cluster.WorkerLimits {
	limits := c.config.WorkerLimits
	if !c.config.AutoBalance.Enabled {
		return limits
	}
	if limits.MaxParallel <= 0 {
		limits.MaxParallel = w.Resources.CPUCores
	}
	limits.CPUPercent = c.config.AutoBalance.PauseCPUPercent
	return limits
}

func (c *Controller) balancedProgramLimit(workers []cluster.Worker) int {
	capacity := 0
	for _, w := range workers {
		if (w.Status != "online" && w.Status != "busy") || w.Protocol != cluster.ProtocolVersion || !freshCPU(w) {
			continue
		}
		n := c.balancedWorkerLimits(w).MaxParallel
		if n > w.Resources.CPUCores {
			n = w.Resources.CPUCores
		}
		if n > w.MaxTasks {
			n = w.MaxTasks
		}
		if !w.Resources.Cgroups && n > 1 {
			n = 1
		}
		if n > 0 {
			capacity += n
		}
	}
	return capacity
}

func (c *Controller) cpuAdmissionReason(w cluster.Worker) string {
	p := c.config.AutoBalance
	if !p.Enabled {
		return ""
	}
	if !freshCPU(w) {
		return "waiting for fresh resource telemetry"
	}
	if w.Resources.CPUPercent >= p.PauseCPUPercent {
		return fmt.Sprintf("worker %s CPU %.1f%%: queued until below %.1f%%", w.ID, w.Resources.CPUPercent, p.PauseCPUPercent)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if state, ok := c.cpuEmergency[w.ID]; ok && time.Now().Before(state.NextCheck) {
		return fmt.Sprintf("worker %s CPU recovery: waiting for cooldown", w.ID)
	}
	return ""
}

// Shed one running processor attempt per worker per cooldown. The existing
// renewable lease fences execution on that worker (normally within 5 seconds).
// Non-processor jobs and staged/uploading results are never selected here.
func (c *Controller) balanceCPU(ctx context.Context, workers []cluster.Worker, active []store.Attempt) error {
	p := c.config.AutoBalance
	if !p.Enabled {
		return nil
	}
	if p.InterruptRunning != nil && !*p.InterruptRunning {
		c.mu.Lock()
		c.cpuEmergency = nil
		c.mu.Unlock()
		return nil
	}
	now := time.Now()
	for _, w := range workers {
		if !freshCPU(w) || w.Protocol != cluster.ProtocolVersion || (w.Status != "online" && w.Status != "busy") {
			continue
		}
		c.mu.Lock()
		if c.cpuEmergency == nil {
			c.cpuEmergency = map[string]cpuEmergency{}
		}
		state, emergency := c.cpuEmergency[w.ID]
		if emergency && now.Before(state.NextCheck) {
			c.mu.Unlock()
			continue
		}
		if emergency && w.Resources.CPUPercent <= p.RecoverCPUPercent {
			delete(c.cpuEmergency, w.ID)
			c.mu.Unlock()
			continue
		}
		if !emergency && w.Resources.CPUPercent <= p.EmergencyCPUPercent {
			c.mu.Unlock()
			continue
		}
		c.cpuEmergency[w.ID] = cpuEmergency{NextCheck: now.Add(time.Duration(p.CooldownSeconds) * time.Second)}
		c.mu.Unlock()
		for i := len(active) - 1; i >= 0; i-- {
			a := active[i]
			if a.WorkerID != w.ID || a.Phase != "running" {
				continue
			}
			reason := fmt.Sprintf("CPU protection: worker %s at %.1f%%; interrupted one processor attempt; reassess after %ds until CPU <= %.1f%%", w.ID, w.Resources.CPUPercent, p.CooldownSeconds, p.RecoverCPUPercent)
			if err := c.Store.FinishAttempt(ctx, a.ID, "interrupted", "", "", reason); err != nil && !errors.Is(err, store.ErrFenced) {
				return err
			}
			break
		}
	}
	return nil
}
