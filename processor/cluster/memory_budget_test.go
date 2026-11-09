package cluster

import (
	"testing"
	"time"
)

func TestSharedMemoryBudget(t *testing.T) {
	for _, total := range []int{3915, 4096, 7941} {
		budget := JobMemoryBudgetMB(total)
		if budget != total*70/100 || budget+WorkerMemoryReserveMB(total) != total {
			t.Fatal("incorrect 70/30 split")
		}
		w := Worker{MaxTasks: 8, Resources: ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: total, AvailableMemoryMB: total, Cgroups: true}}
		r, reason := AutomaticResources(w, Resources{})
		if reason != "" || r.MemoryMB != FairMemoryShareMB(w) || !r.MemoryBurst {
			t.Fatalf("idle worker cannot allocate its fair share: %+v %s", r, reason)
		}
		if _, reason = AutomaticResources(w, r); reason != "" {
			t.Fatalf("first task monopolized the pool: %s", reason)
		}
		reserved := Program{}.Resources()
		r, reason = AutomaticResources(w, reserved)
		if reason != "" || r.MemoryMB != FairMemoryShareMB(w) || r.MemoryMB+reserved.MemoryMB > budget {
			t.Fatalf("processor reservation not shared: %+v %s", r, reason)
		}
		if Admission(w, Resources{CPUCores: 1, MemoryMB: budget + 1}, WorkerLimits{}, Resources{}, 0) == "" {
			t.Fatal("oversized explicit reservation admitted")
		}
		w.Resources.AvailableMemoryMB = WorkerMemoryReserveMB(total) + 400
		if _, reason = AutomaticResources(w, Resources{}); reason == "" {
			t.Fatal("host headroom ignored")
		}
		if JobMemoryEmergencyMB(total) <= budget || total-JobMemoryEmergencyMB(total) < 512 {
			t.Fatal("burst ceiling does not retain emergency worker headroom")
		}
		w.Resources.AvailableMemoryMB = total
		w.Resources.JobMemoryMB = budget + 1
		w.CurrentLoad = 1
		if reason := Admission(w, Program{}.Resources(), WorkerLimits{}, Resources{}, 0); reason != "waiting for running job memory burst to finish" {
			t.Fatalf("burst usage must block another task: %s", reason)
		}
		w.CurrentLoad = 0
		if reason := Admission(w, Program{}.Resources(), WorkerLimits{}, Resources{}, 0); reason != "" {
			t.Fatalf("idle cached pages must not deadlock intake: %s", reason)
		}
	}
}
