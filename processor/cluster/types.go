// Package cluster defines the processor/worker contract without server dependencies.
package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const ProtocolVersion = "processor-v2"
const LeaseDuration = 30 * time.Second
const HeartbeatInterval = 5 * time.Second
const MaxLogBytes = 4 << 20
const MaxArtifactBytes = 64 << 20

// Config remains compatible with existing routes and export_results.
type Config struct {
	ExecutionMode  string                          `json:"execution_mode,omitempty"`
	Routes         map[string][]string             `json:"routes"`
	ExportResults  []string                        `json:"export_results"`
	Programs       map[string]Program              `json:"programs,omitempty"`
	WorkerLimits   WorkerLimits                    `json:"worker_limits,omitempty"`
	AutoBalance    AutoBalancePolicy               `json:"auto_balance,omitempty"`
	FileProcessing map[string]FileProcessingPolicy `json:"file_processing,omitempty"`
}

// FileProcessingPolicy controls orchestration without inspecting a program.
type FileProcessingPolicy struct {
	Distribution   string `json:"distribution,omitempty"`
	RequireSummary bool   `json:"require_summary,omitempty"`
}

type AutoBalancePolicy struct {
	Enabled             bool    `json:"enabled"`
	PauseCPUPercent     float64 `json:"pause_cpu_percent"`
	EmergencyCPUPercent float64 `json:"emergency_cpu_percent"`
	RecoverCPUPercent   float64 `json:"recover_cpu_percent"`
	CooldownSeconds     int     `json:"cooldown_seconds"`
	InterruptRunning    *bool   `json:"interrupt_running,omitempty"`
}

func (p AutoBalancePolicy) Validate() error {
	if !p.Enabled {
		return nil
	}
	if p.PauseCPUPercent <= 0 || p.PauseCPUPercent >= p.RecoverCPUPercent || p.RecoverCPUPercent >= p.EmergencyCPUPercent || p.EmergencyCPUPercent > 100 || p.CooldownSeconds < 10 {
		return fmt.Errorf("auto balance requires 0 < pause CPU < recovery CPU < emergency CPU <= 100 and cooldown >= 10 seconds")
	}
	return nil
}

type Program struct {
	Rank             *int             `json:"rank,omitempty"`
	AutoPublish      AutoPublish      `json:"auto_publish,omitempty"`
	Entrypoint       string           `json:"entrypoint,omitempty"`
	Manifest         string           `json:"manifest,omitempty"`
	Folder           string           `json:"folder,omitempty"`
	Definition       *ToolManifest    `json:"definition,omitempty"`
	Script           *ScriptExecution `json:"script,omitempty"`
	Files            []string         `json:"files,omitempty"`
	PackageLock      string           `json:"package_lock,omitempty"`
	RequirementsLock string           `json:"requirements_lock,omitempty"`
	ParallelSafe     bool             `json:"parallel_safe,omitempty"`
	RetrySafe        bool             `json:"retry_safe,omitempty"`
	MaxParallel      int              `json:"max_parallel,omitempty"`
	CPUCores         int              `json:"cpu_cores,omitempty"`
	MemoryMB         int              `json:"memory_mb,omitempty"`
	Threads          int              `json:"threads,omitempty"`
	TimeoutSeconds   int              `json:"timeout_seconds,omitempty"`
}

// A script path is the simple form; object settings retain their existing defaults.
func (p *Program) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var path string
		if err := json.Unmarshal(data, &path); err != nil {
			return err
		}
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("program script path must not be empty")
		}
		if strings.HasSuffix(path, ".json") {
			*p = Program{Manifest: path}
		} else {
			*p = Program{Entrypoint: path, AutoPublish: AutoPublish{Enabled: true, Directory: "both"}}
		}
		return nil
	}
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("program must be a script path or settings object")
	}
	type settings Program
	var value settings
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = Program(value)
	return nil
}

func (p Program) PriorityRank() int {
	if p.Rank == nil {
		return 1
	}
	return *p.Rank
}

// AutoPublish discovers direct text outputs without changing the program.
type AutoPublish struct {
	Enabled   bool   `json:"enabled"`
	Directory string `json:"directory,omitempty"`
}

func (p Program) Limit() int {
	if p.MaxParallel <= 0 {
		return 6
	}
	return p.MaxParallel
}

type Bundle struct {
	Script           *ScriptExecution  `json:"script,omitempty"`
	AutoPublish      AutoPublish       `json:"auto_publish,omitempty"`
	ProgramID        string            `json:"program_id"`
	Entrypoint       string            `json:"entrypoint"`
	PackageLock      string            `json:"package_lock,omitempty"`
	RequirementsLock string            `json:"requirements_lock,omitempty"`
	Files            map[string][]byte `json:"files"`
	Resources        Resources         `json:"resources"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
}
type Install struct {
	WorkerLimits WorkerLimits `json:"worker_limits"`
	Version      string       `json:"version"`
	BundleURL    string       `json:"bundle_url"`
	Token        string       `json:"token"`
}
type Assignment struct {
	WorkerLimits   WorkerLimits `json:"worker_limits"`
	RequireCgroups bool         `json:"require_cgroups"`
	AttemptID      string       `json:"attempt_id"`
	RunID          string       `json:"run_id"`
	ProgramID      string       `json:"program_id"`
	Version        string       `json:"version"`
	WorkerID       string       `json:"worker_id"`
	BaseURL        string       `json:"base_url"`
	Token          string       `json:"token"`
	Export         bool         `json:"export"`
	PublishOnly    bool         `json:"publish_only"`
	TimeoutSeconds int          `json:"timeout_seconds"`
	Resources      Resources    `json:"resources"`
}
type Worker struct {
	Workspaces     []string          `json:"processor_workspaces,omitempty"`
	Address        string            `json:"address,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ProcessorLoad  int               `json:"processor_load"`
	Reservations   Resources         `json:"reservations"`
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	CurrentLoad    int               `json:"current_load"`
	MaxTasks       int               `json:"max_tasks"`
	Protocol       string            `json:"processor_protocol"`
	Versions       []string          `json:"processor_versions"`
	ActiveAttempts []string          `json:"processor_attempts,omitempty"`
	Resources      ResourceSnapshot  `json:"resources"`
}
type Artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Completion struct {
	ExitCode    int        `json:"exit_code"`
	Error       string     `json:"error,omitempty"`
	Interrupted bool       `json:"interrupted,omitempty"`
	Artifacts   []Artifact `json:"artifacts"`
}
type Lease struct {
	Seconds int    `json:"seconds"`
	Phase   string `json:"phase"`
}

// Defaults keep independent input chunks small and worker admission conservative.
type WorkerLimits struct {
	MaxParallel     int     `json:"max_parallel,omitempty"`
	CPUPercent      float64 `json:"cpu_percent,omitempty"`
	ReserveMemoryMB int     `json:"reserve_memory_mb,omitempty"`
}
type Resources struct {
	MemoryBurst bool `json:"memory_burst,omitempty"`
	CPUCores    int  `json:"cpu_cores"`
	MemoryMB    int  `json:"memory_mb"`
	Threads     int  `json:"threads"`
}

func (p Program) Resources() Resources {
	r := Resources{CPUCores: p.CPUCores, MemoryMB: p.MemoryMB, Threads: p.Threads}
	if r.CPUCores <= 0 {
		r.CPUCores = 1
	}
	if r.MemoryMB <= 0 {
		r.MemoryMB = 512
	}
	if r.Threads <= 0 {
		r.Threads = 1
	}
	return r
}

type ResourceSnapshot struct {
	JobMemoryMB       int                  `json:"job_memory_mb"`
	Capabilities      *RuntimeCapabilities `json:"capabilities,omitempty"`
	SampledAt         int64                `json:"sampled_at"`
	CPUPercent        float64              `json:"cpu_percent"`
	CPUCores          int                  `json:"cpu_cores"`
	TotalMemoryMB     int                  `json:"total_memory_mb"`
	AvailableMemoryMB int                  `json:"available_memory_mb"`
	Cgroups           bool                 `json:"cgroups"`
}
type Publication struct {
	Name       string   `json:"name"`
	Generation string   `json:"generation"`
	Sequence   int64    `json:"sequence"`
	Lines      []string `json:"lines"`
}
