package cnc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpreadFiftyLinesBalanced(t *testing.T) {
	for _, tc := range []struct {
		workers int
		counts  []int
	}{
		{5, []int{10, 10, 10, 10, 10}},
		{6, []int{9, 9, 8, 8, 8, 8}},
		{60, nil},
	} {
		t.Run(fmt.Sprint(tc.workers), func(t *testing.T) {
			dir := t.TempDir()
			var lines []string
			for i := 0; i < 50; i++ {
				lines = append(lines, fmt.Sprintf("fixture-line-%02d", i))
			}
			input := filepath.Join(dir, "input.txt")
			// A final line without a newline must also be assigned exactly once.
			if err := os.WriteFile(input, []byte(strings.Join(lines, "\n")), 0600); err != nil {
				t.Fatal(err)
			}
			parts, err := (&Server{}).splitInputFileByCount(input, tc.workers, filepath.Join(dir, "chunks"), "input.txt")
			if err != nil {
				t.Fatal(err)
			}
			counts := tc.counts
			if counts == nil {
				counts = make([]int, 50)
				for i := range counts {
					counts[i] = 1
				}
			}
			if len(parts) != len(counts) {
				t.Fatalf("parts=%d, want %d", len(parts), len(counts))
			}
			var received []string
			for i, part := range parts {
				data, err := os.ReadFile(part)
				if err != nil {
					t.Fatal(err)
				}
				chunk := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
				if len(chunk) != counts[i] {
					t.Fatalf("part %d lines=%d, want %d", i, len(chunk), counts[i])
				}
				received = append(received, chunk...)
			}
			if strings.Join(received, "\n") != strings.Join(lines, "\n") {
				t.Fatal("split lost, duplicated, or reordered input lines")
			}
		})
	}
}

func TestSpreadAutomaticallyUsesAvailableWorkers(t *testing.T) {
	for _, count := range []int{5, 6} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			s := NewServer(&ServerConfig{DataDir: dir})
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("fixture-worker-%d", i)
				s.workers[id] = &Worker{ID: id, Status: WorkerStatusOnline, Resources: memoryFixtureSnapshot(), MaxTasks: 2, SendCh: make(chan *Message, 2)}
			}
			input := filepath.Join(dir, "fixture.txt")
			if err := os.WriteFile(input, []byte(strings.Repeat("fixture\n", 50)), 0600); err != nil {
				t.Fatal(err)
			}
			job := &Job{ID: "fixture", InputFile: input, Command: "true", Status: "queued"}
			s.jobs[job.ID] = job
			s.processSpreadJob(job)
			if job.Workers != count || job.TotalTasks != count {
				t.Fatalf("workers=%d tasks=%d, want %d", job.Workers, job.TotalTasks, count)
			}
			for i := 0; i < count; i++ {
				select {
				case task := <-s.taskQueue:
					s.dispatchTask(task)
				default:
					t.Fatal("not all chunks were queued immediately")
				}
			}
			for id, worker := range s.workers {
				if len(worker.SendCh) != 1 {
					t.Fatalf("%s received %d assignments, want one", id, len(worker.SendCh))
				}
			}
		})
	}
}

func TestSpreadEnumJobUsesAvailableWorkers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	s := NewServer(&ServerConfig{DataDir: dir})
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("enum-worker-%d", i)
		s.workers[id] = &Worker{ID: id, Status: WorkerStatusOnline, Resources: memoryFixtureSnapshot(), MaxTasks: 2, SendCh: make(chan *Message, 2)}
	}
	input := filepath.Join(dir, "targets.txt")
	if err := os.WriteFile(input, []byte(strings.Repeat("https://example.com\n", 5000)), 0600); err != nil {
		t.Fatal(err)
	}
	job := &Job{ID: "enum_fixture", InputFile: input, Mode: JobModeSpread, Status: "queued", Enum: &EnumJobConfig{Token: "test", ArtifactSHA256: "abc"}}
	s.jobs[job.ID] = job
	s.processSpreadJob(job)
	if job.Workers != 3 || job.TotalTasks != 3 {
		t.Fatalf("workers=%d tasks=%d, want 3", job.Workers, job.TotalTasks)
	}
}

