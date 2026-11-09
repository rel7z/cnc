package cnc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnumLargeInputBalancesAcrossWorkers(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	data := []byte(strings.Repeat("https://example.invalid/owned\n", 100001))
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err := splitEnumInput(input, filepath.Join(dir, "chunks"), 3)
	if err != nil || len(chunks) != 3 {
		t.Fatalf("want one shard per worker: count=%d error=%v", len(chunks), err)
	}
	var combined bytes.Buffer
	for i, path := range chunks {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("invalid chunk: %v", err)
		}
		want := 33333
		if i < 2 {
			want++
		}
		if bytes.Count(body, []byte{'\n'}) != want {
			t.Fatal("shards are not balanced")
		}
		combined.Write(body)
	}
	if !bytes.Equal(data, combined.Bytes()) {
		t.Fatal("input lines lost, duplicated, or reordered")
	}
	// Restart preparation must reuse identical immutable chunks.
	reused, err := splitEnumInput(input, filepath.Join(dir, "chunks"), 3)
	if err != nil || len(reused) != len(chunks) {
		t.Fatalf("not idempotent: %v", err)
	}
}

func TestEnumDoesNotCreateExtraShardsAtOldByteCap(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	data := []byte(strings.Repeat(strings.Repeat("a", 512*1024)+"\n", 65))
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err := splitEnumInput(input, filepath.Join(dir, "chunks"), 1)
	if err != nil || len(chunks) != 1 {
		t.Fatalf("one worker must receive one shard: count=%d error=%v", len(chunks), err)
	}
	var combined bytes.Buffer
	for _, path := range chunks {
		body, err := os.ReadFile(path)
		if err != nil || len(body) <= 16*1024*1024 {
			t.Fatalf("old artificial byte cap is still applied: %v", err)
		}
		combined.Write(body)
	}
	if !bytes.Equal(data, combined.Bytes()) {
		t.Fatal("large line was split or lost")
	}
}

func TestWordPressBulkCapsMillionTargetShards(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "bulk.txt")
	line := []byte("https://example.invalid/\n")
	data := bytes.Repeat(line, 1_000_001)
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err := splitEnumInputWithLimit(input, filepath.Join(dir, "chunks"), 2, wordpressBulkShardTargets)
	if err != nil || len(chunks) != 12 {
		t.Fatalf("expected 12 bulk shards, got %d: %v", len(chunks), err)
	}
	total := 0
	for _, path := range chunks {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Count(body, []byte{'\n'})
		if lines == 0 || lines > wordpressBulkShardTargets || !bytes.Equal(body, data[total:total+len(body)]) {
			t.Fatalf("invalid bulk shard: %d lines", lines)
		}
		total += len(body)
	}
	if total != len(data) {
		t.Fatalf("lost input: %d of %d bytes", total, len(data))
	}
}
