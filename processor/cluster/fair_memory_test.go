package cluster

import (
	"testing"
	"time"
)

func TestLiveSnapshotAllowsSmallJobBesideLegacyBurst(t *testing.T) {
	for _, tc := range []struct{ available, usage, old int }{{3197, 51, 2116}, {3181, 63, 2109}, {3114, 110, 2106}} {
		w := Worker{MaxTasks: 2, CurrentLoad: 1, Resources: ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 2, TotalMemoryMB: 3812, AvailableMemoryMB: tc.available, JobMemoryMB: tc.usage, Cgroups: true}}
		r := EffectiveReservation(w, Resources{CPUCores: 1, MemoryMB: tc.old, MemoryBurst: true})
		if r.MemoryMB != 1334 {
			t.Fatalf("unfair legacy reservation: %+v", r)
		}
		if reason := Admission(w, Program{}.Resources(), WorkerLimits{}, r, 0); reason != "" {
			t.Fatalf("idle CPU slot rejected: %s", reason)
		}
		explicit := Resources{CPUCores: 1, MemoryMB: tc.old}
		if EffectiveReservation(w, explicit) != explicit {
			t.Fatal("explicit reservation was weakened")
		}
	}
}

func TestResidentMemoryNotSubtractedTwice(t *testing.T) {
	w := Worker{MaxTasks: 4, CurrentLoad: 1, Resources: ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 1741, JobMemoryMB: 1024, Cgroups: true}}
	r := Resources{CPUCores: 1, MemoryMB: 1024}
	if reason := Admission(w, Program{}.Resources(), WorkerLimits{}, r, 1); reason != "" {
		t.Fatalf("resident bytes double counted: %s", reason)
	}
	w.Resources.AvailableMemoryMB--
	if reason := Admission(w, Program{}.Resources(), WorkerLimits{}, r, 1); reason != "waiting for available memory" {
		t.Fatalf("host headroom lost: %s", reason)
	}
	w.Resources.AvailableMemoryMB = 3000
	w.Resources.JobMemoryMB = 2700
	if reason := Admission(w, Program{}.Resources(), WorkerLimits{}, r, 1); reason != "waiting for shared job memory budget" {
		t.Fatalf("burst usage ignored: %s", reason)
	}
}

func TestAutomaticJobsShareSlotsAndKeepCPUAndMemoryBounds(t *testing.T) {
	w := Worker{MaxTasks: 4, Resources: ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 4096, Cgroups: true}}
	reserved := Resources{}
	for n := 0; n < 4; n++ {
		r, reason := AutomaticResources(w, reserved)
		if reason != "" {
			t.Fatalf("slot %d: %s", n, reason)
		}
		reserved.CPUCores += r.CPUCores
		reserved.MemoryMB += r.MemoryMB
		w.CurrentLoad++
	}
	if reserved.MemoryMB > JobMemoryBudgetMB(4096) {
		t.Fatal("shared pool exceeded")
	}
	if _, reason := AutomaticResources(w, reserved); reason == "" {
		t.Fatal("fifth task overcommitted worker")
	}
}
