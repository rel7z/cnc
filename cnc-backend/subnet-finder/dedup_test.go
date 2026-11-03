package main

import (
	"context"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
)

func TestDiskDedupScalesWithoutGrowingHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("large index fixture")
	}
	previous := debug.SetMemoryLimit(128 << 20)
	defer debug.SetMemoryLimit(previous)
	store, err := newDiskSet(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const count = 300000
	for i := 0; i < count; i++ {
		fresh, err := store.Add(context.Background(), 2, strconv.Itoa(i))
		if err != nil || !fresh {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	for _, i := range []int{0, 12345, count - 1} {
		fresh, err := store.Add(context.Background(), 2, strconv.Itoa(i))
		if err != nil || fresh {
			t.Fatal("exact dedup lost earlier key")
		}
	}
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	if mem.HeapAlloc > 64<<20 {
		t.Fatalf("heap retained too much index state: %d", mem.HeapAlloc)
	}
	t.Logf("%d exact keys stored; retained Go heap %.1f MiB", count, float64(mem.HeapAlloc)/(1<<20))
}
