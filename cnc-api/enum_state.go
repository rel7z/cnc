package cnc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"cnc-enum/version"
)

type enumDiskState struct {
	SchemaVersion    int    `json:"schema_version"`
	Job              Job    `json:"job"`
	Tasks            []Task `json:"tasks"`
	Token            string `json:"transfer_token"`
	ArtifactFilename string `json:"artifact_filename"`
}

// Caller holds s.mu. Persist before dispatch or acknowledging a publication.
func (s *Server) persistEnumJobLocked(jobID string) error {
	job := s.jobs[jobID]
	if job == nil || job.Enum == nil {
		return nil
	}
	state := enumDiskState{SchemaVersion: 1, Job: *job, Token: job.Enum.Token, ArtifactFilename: job.Enum.ArtifactFilename, Tasks: []Task{}}
	for _, id := range s.jobTasks[jobID] {
		if task := s.tasks[id]; task != nil {
			state.Tasks = append(state.Tasks, *task)
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	directory := filepath.Join(s.config.DataDir, "enum-jobs", jobID)
	file, err := os.CreateTemp(directory, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), filepath.Join(directory, "state.json")); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Server) recoverEnumJobs() error {
	directory := filepath.Join(s.config.DataDir, "enum-jobs")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	pending := []*Task{}
	unprepared := []*Job{}
	for _, entry := range entries {
		if !entry.IsDir() || !version.ValidTransportID(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name(), "state.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var state enumDiskState
		if json.Unmarshal(data, &state) != nil || state.SchemaVersion != 1 || state.Job.ID != entry.Name() || state.Job.Enum == nil || state.Token == "" || filepath.Base(state.ArtifactFilename) != state.ArtifactFilename {
			return fmt.Errorf("invalid persisted enum job")
		}
		job := state.Job
		job.Enum.Token = state.Token
		job.Enum.ArtifactFilename = state.ArtifactFilename
		if _, err := version.LoadArtifact(filepath.Join(directory, job.ID, state.ArtifactFilename), job.Enum.ArtifactSHA256, job.ID); err != nil {
			return fmt.Errorf("invalid persisted enum artifact")
		}
		s.jobs[job.ID] = &job
		s.jobTasks[job.ID] = []string{}
		for _, stored := range state.Tasks {
			task := stored
			if !version.ValidTransportID(task.ID) || task.JobID != job.ID || task.Type != "enum" {
				return fmt.Errorf("invalid persisted enum task")
			}
			chunk, _ := task.Payload["chunk_path"].(string)
			relative, err := filepath.Rel(filepath.Join(directory, job.ID, "chunks"), chunk)
			if err != nil || !filepath.IsLocal(relative) {
				return fmt.Errorf("persisted enum task has an invalid chunk")
			}
			if task.Status == TaskStatusAssigned || task.Status == TaskStatusRunning {
				now := time.Now()
				task.Status = TaskStatusFailed
				task.CompletedAt = &now
				task.Error = "interrupted by controller restart; retained publications can still transfer"
			}
			s.tasks[task.ID] = &task
			s.jobTasks[job.ID] = append(s.jobTasks[job.ID], task.ID)
			if task.Status == TaskStatusPending {
				pending = append(pending, &task)
			}
		}
		if len(state.Tasks) == 0 && (job.Status == "pending" || job.Status == "running") {
			unprepared = append(unprepared, &job)
		} else {
			s.updateJobProgress(job.ID)
		}
		if err := s.persistEnumJobLocked(job.ID); err != nil {
			return err
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	for _, task := range pending {
		select {
		case s.taskQueue <- task:
		default:
			go func(task *Task) {
				select {
				case s.taskQueue <- task:
				case <-s.ctx.Done():
				}
			}(task)
		}
	}
	for _, job := range unprepared {
		go s.processSpreadJob(job)
	}
	if len(entries) > 0 {
		log.Printf("Recovered %d pending enum shards", len(pending))
	}
	return nil
}

// Spread into one balanced shard per requested worker. Preserve complete lines
// and the existing 256 MiB worker transfer limit without making extra shards.
func splitEnumInput(inputPath, directory string, workerCount ...int) ([]string, error) {
	file, err := os.Open(inputPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	workers := 1
	if len(workerCount) > 0 && workerCount[0] > 0 {
		workers = workerCount[0]
	}
	count := 0
	counter := bufio.NewScanner(file)
	counter.Buffer(make([]byte, 64*1024), 1024*1024)
	for counter.Scan() {
		if len(counter.Bytes()) > 0 {
			count++
		}
	}
	if err := counter.Err(); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("no enum input targets")
	}
	if workers > count {
		workers = count
	}
	baseLines, extra := count/workers, count%workers
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	paths := []string{}
	var buffer bytes.Buffer
	lines := 0
	flush := func() error {
		if lines == 0 {
			return nil
		}
		name := fmt.Sprintf("chunk-%06d.txt", len(paths))
		path, err := version.WriteImmutable(directory, name, buffer.Bytes())
		if os.IsExist(err) {
			path = filepath.Join(directory, name)
			old, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(old, buffer.Bytes()) {
				return fmt.Errorf("enum chunk collision")
			}
			err = nil
		}
		if err != nil {
			return err
		}
		paths = append(paths, path)
		buffer.Reset()
		lines = 0
		return nil
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		linesPerShard := baseLines
		if len(paths) < extra {
			linesPerShard++
		}
		if lines >= linesPerShard {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		if buffer.Len()+len(line)+1 > 256*1024*1024 {
			return nil, fmt.Errorf("enum shard exceeds the worker transfer limit")
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
		lines++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no enum input targets")
	}
	return paths, nil
}
