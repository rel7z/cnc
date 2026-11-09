package cluster

import "time"

// JobMemoryBudgetMB is the normal admission budget, not the emergency ceiling.
func JobMemoryBudgetMB(total int) int { return total * 70 / 100 }

// JobMemoryEmergencyMB permits borrowing spare RAM, retaining at least 15%
// or 512 MiB for the worker and other host services.
func JobMemoryEmergencyMB(total int) int {
	budget := total * 85 / 100
	if budget > total-512 {
		budget = total - 512
	}
	if budget < 0 {
		return 0
	}
	return budget
}

func WorkerMemoryReserveMB(total int) int { return total - JobMemoryBudgetMB(total) }

// FairMemoryShareMB is a soft reservation per runnable CPU/task slot. Burstable
// jobs may still borrow spare RAM under the existing shared cgroup hard limit.
func FairMemoryShareMB(w Worker) int {
	slots := w.MaxTasks
	if slots <= 0 || slots > w.Resources.CPUCores {
		slots = w.Resources.CPUCores
	}
	if !w.Resources.Cgroups || slots < 1 {
		slots = 1
	}
	share := JobMemoryBudgetMB(w.Resources.TotalMemoryMB) / slots
	if share < 512 {
		share = 512
	}
	return share
}

// EffectiveReservation preserves explicit reservations and fairly accounts for
// older automatic jobs that previously reserved the entire pool. This changes
// a burstable job's soft accounting, never its enforced hard memory ceiling.
func EffectiveReservation(w Worker, r Resources) Resources {
	if w.Resources.TotalMemoryMB > 0 && r.MemoryBurst && r.MemoryMB > FairMemoryShareMB(w) {
		r.MemoryMB = FairMemoryShareMB(w)
	}
	return r
}

func activeMemoryMB(w Worker, reserved Resources) int {
	if w.CurrentLoad == 0 && reserved.MemoryMB == 0 {
		return 0
	}
	return w.Resources.JobMemoryMB
}

// MemAvailable already excludes resident job memory. Only the unused portion
// of outstanding reservations must be subtracted again.
func UnusedMemoryReservationMB(w Worker, reserved Resources) int {
	unused := reserved.MemoryMB - activeMemoryMB(w, reserved)
	if unused < 0 {
		return 0
	}
	return unused
}

// AutomaticResources reserves a fair share, leaving room for other slots.
func AutomaticResources(w Worker, reserved Resources) (Resources, string) {
	r := Program{}.Resources()
	if w.Resources.SampledAt == 0 || time.Since(time.UnixMilli(w.Resources.SampledAt)) > 15*time.Second {
		return r, "waiting for fresh resource telemetry"
	}
	memory := JobMemoryBudgetMB(w.Resources.TotalMemoryMB) - reserved.MemoryMB
	if memory > FairMemoryShareMB(w) {
		memory = FairMemoryShareMB(w)
	}
	available := w.Resources.AvailableMemoryMB - WorkerMemoryReserveMB(w.Resources.TotalMemoryMB) - UnusedMemoryReservationMB(w, reserved)
	if memory > available {
		memory = available
	}
	if memory < r.MemoryMB {
		return r, "waiting for shared job memory budget"
	}
	r.MemoryMB = memory
	r.MemoryBurst = true
	if reason := Admission(w, r, WorkerLimits{}, reserved, 0); reason != "" {
		return r, reason
	}
	return r, ""
}
