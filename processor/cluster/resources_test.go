package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResourceAdmission(t *testing.T) {
	w := Worker{MaxTasks: 8, Resources: ResourceSnapshot{SampledAt: time.Now().UnixMilli(), CPUCores: 4, TotalMemoryMB: 4096, AvailableMemoryMB: 3072, Cgroups: true}}
	r := Program{}.Resources()
	if reason := Admission(w, r, WorkerLimits{}, Resources{}, 0); reason != "" {
		t.Fatal(reason)
	}
	w.Resources.CPUPercent = 85
	if !strings.Contains(Admission(w, r, WorkerLimits{}, Resources{}, 0), "CPU usage") {
		t.Fatal("CPU overload admitted")
	}
	w.Resources.CPUPercent = 0
	w.Resources.AvailableMemoryMB = 1024
	if !strings.Contains(Admission(w, r, WorkerLimits{}, Resources{}, 0), "memory") {
		t.Fatal("memory overload admitted")
	}
	w.Resources.AvailableMemoryMB = 3072
	w.Resources.Cgroups = false
	if !strings.Contains(Admission(w, r, WorkerLimits{}, Resources{}, 1), "processor slot") {
		t.Fatal("unenforced concurrency admitted")
	}
	w.Resources.Cgroups = true
	if Admission(w, r, WorkerLimits{}, Resources{}, 2) == "" {
		t.Fatal("default maximum bypassed")
	}
	w.Resources.SampledAt = time.Now().Add(-20 * time.Second).UnixMilli()
	if !strings.Contains(Admission(w, r, WorkerLimits{}, Resources{}, 0), "telemetry") {
		t.Fatal("stale resources admitted")
	}
}
func TestMergeTextIdempotentAndRetainsOtherWriters(t *testing.T) {
	root := t.TempDir()
	if err := MergeText(root, "result.txt", []string{"first\n", "second\n", "first\n"}); err != nil {
		t.Fatal(err)
	}
	if err := MergeText(root, "result.txt", []string{"second\n", "third\n"}); err != nil {
		t.Fatal(err)
	}
}

func TestMergeTextPreservesConcurrentSourceContent(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "result.txt")
	os.WriteFile(p, []byte("CNC line\n"), 0600)
	if err := MergeText(root, "result.txt", []string{"processor line\n", "CNC line\n", "processor line\n"}); err != nil {
		t.Fatal(err)
	}
	if err := MergeText(root, "result.txt", []string{"processor line\n"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "CNC line\nprocessor line\n" {
		t.Fatalf("lost or duplicated output: %q", data)
	}
	os.WriteFile(p, []byte("partial"), 0600)
	if MergeText(root, "result.txt", []string{"new\n"}) == nil {
		t.Fatal("merged into unfinished CNC line")
	}
}
