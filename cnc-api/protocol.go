package cnc

import (
	"cnc-enum/version"
	"encoding/json"
	"github.com/user/cnc-processor/cluster"
	"time"
)

// Configuration constants
const (
	DefaultTaskQueueSize = 10000
	DefaultMaxRetries    = 3
	DefaultHeartbeatTTL  = 30 * time.Second
	FileReadBufferSize   = 64 * 1024
	DefaultHTTPPort      = ":8080"
	DefaultTCPPort       = ":9090"
	DefaultTimeout       = 300 // seconds
	NoTimeout            = -1  // sentinel: run with no deadline
)

// JobMode determines how a job distributes work across workers.
type JobMode string

const (
	// JobModeSpread splits the input file into N parts and sends one part per worker.
	JobModeSpread JobMode = "spread"
	// JobModeBroadcast sends the same command to every online worker (no file splitting).
	JobModeBroadcast JobMode = "broadcast"
	// JobModeServer executes the command locally on the server host machine (not sent to workers).
	JobModeServer JobMode = "server"
)

// TaskStatus tracks the lifecycle of a single task.
type TaskStatus string

const (
	TaskStatusPending             TaskStatus = "pending"
	TaskStatusAssigned            TaskStatus = "assigned"
	TaskStatusRunning             TaskStatus = "running"
	TaskStatusCompleted           TaskStatus = "completed"
	TaskStatusCompletedWithErrors TaskStatus = "completed_with_errors"
	TaskStatusFailed              TaskStatus = "failed"
	TaskStatusCancelled           TaskStatus = "cancelled"
)

// WorkerStatus tracks whether a worker is available.
type WorkerStatus string

const (
	WorkerStatusOnline  WorkerStatus = "online"
	WorkerStatusOffline WorkerStatus = "offline"
	WorkerStatusBusy    WorkerStatus = "busy"
)

// Task is a single unit of work dispatched to a worker.
// Payload keys set by the server:
//
//	"command"         string  — shell command with {input} placeholder
//	"download_url"    string  — URL path for the worker to download its chunk (spread only)
//	"dest_name"       string  — filename inside this task's temporary HOME (spread only)
//	"chunk_path"      string  — server-side path, for logging (spread only)
//	"timeout_seconds" float64 — kill timeout (-1 = no deadline)
type Task struct {
	ExecutionResources  *cluster.Resources     `json:"execution_resources,omitempty"`
	QueueReason         string                 `json:"queue_reason,omitempty"`
	ProcessorAssignment *cluster.Assignment    `json:"-"`
	ProcessorInstall    *cluster.Install       `json:"-"`
	EnumPublication     string                 `json:"enum_publication,omitempty"`
	ID                  string                 `json:"id"`
	JobID               string                 `json:"job_id"`
	Type                string                 `json:"type"` // always "shell"
	Payload             map[string]interface{} `json:"payload"`
	Status              TaskStatus             `json:"status"`
	AssignedTo          string                 `json:"assigned_to,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	StartedAt           *time.Time             `json:"started_at,omitempty"`
	CompletedAt         *time.Time             `json:"completed_at,omitempty"`
	Result              *TaskResult            `json:"result,omitempty"`
	Error               string                 `json:"error,omitempty"`
	RetryCount          int                    `json:"retry_count"`
	Priority            int                    `json:"priority"`
}

// TaskResult holds the outcome of a completed task.
type TaskResult struct {
	Stdout            string `json:"stdout,omitempty"`
	Stderr            string `json:"stderr,omitempty"`
	ExitCode          int    `json:"exit_code"`
	PublicationStatus string `json:"publication_status,omitempty"`
}

// Worker represents a connected worker agent.
type Worker struct {
	EnumRequests        map[string]version.BudgetMetrics `json:"enum_requests,omitempty"`
	Resources           cluster.ResourceSnapshot         `json:"resources"`
	ProcessorProtocol   string                           `json:"processor_protocol,omitempty"`
	ProcessorVersions   []string                         `json:"processor_versions,omitempty"`
	ProcessorAttempts   []string                         `json:"processor_attempts,omitempty"`
	ProcessorWorkspaces []string                         `json:"processor_workspaces,omitempty"`
	ID                  string                           `json:"id"`
	Address             string                           `json:"address"`
	Status              WorkerStatus                     `json:"status"`
	MaxTasks            int                              `json:"max_tasks"`
	CurrentLoad         int                              `json:"current_load"`
	LastSeen            time.Time                        `json:"last_seen"`
	Registered          time.Time                        `json:"registered"`
	Metadata            map[string]string                `json:"metadata,omitempty"`
	SendCh              chan *Message                    `json:"-"` // non-blocking send channel; not serialised
}

// Job is a user-submitted unit of work.
type Job struct {
	ReverseIP      *ReverseIPInputSummary `json:"reverse_ip_input,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Enum           *EnumJobConfig         `json:"enum,omitempty"`
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Command        string                 `json:"command"` // e.g. "node index.js {input}"
	Mode           JobMode                `json:"mode"`    // "spread" or "broadcast"
	InputFile      string                 `json:"input_file"`
	OutputFile     string                 `json:"output_file,omitempty"` // output file in watcher dir for tool jobs
	Workers        int                    `json:"workers"`               // number of parts to split into
	TimeoutSeconds int                    `json:"timeout_seconds"`       // 0 = DefaultTimeout, -1 = no timeout
	TotalTasks     int                    `json:"total_tasks"`
	Completed      int                    `json:"completed"`
	Failed         int                    `json:"failed"`
	Status         string                 `json:"status"`
	CreatedAt      time.Time              `json:"created_at"`
	StartedAt      *time.Time             `json:"started_at,omitempty"`
	CompletedAt    *time.Time             `json:"completed_at,omitempty"`
}

// ── Tool Schemas ───────────────────────────────────────────────────────────────

// ToolOption defines a configurable parameter for a tool.
type ToolOption struct {
	Name        string      `json:"name"`        // e.g. "threads"
	Flag        string      `json:"flag"`        // e.g. "-t"
	Description string      `json:"description"` // e.g. "Number of threads"
	Type        string      `json:"type"`        // "string", "number", "boolean"
	Default     interface{} `json:"default,omitempty"`
}

// SharedFileSpec describes a server-side file path that must be uploaded
// to the CNC server and distributed to workers before execution.
// Each spec produces a curl pre-download step and a flag injected into the command.
type SharedFileSpec struct {
	Name        string `json:"name"`           // field key used in the launch API payload
	Label       string `json:"label"`          // human-readable label shown in the UI
	Flag        string `json:"flag"`           // CLI flag passed to the tool
	Description string `json:"description"`    // tooltip / helper text
	Required    bool   `json:"required"`       // whether the UI should mark this field as required
	Type        string `json:"type,omitempty"` // "textarea" or "text" (default)
}

// ToolDefinition defines an external tool that can be executed on workers or on the server.
type ToolDefinition struct {
	ID               string           `json:"id"`                          // e.g. "cms-scan"
	Name             string           `json:"name"`                        // e.g. "CMS Scanner"
	Description      string           `json:"description"`                 // e.g. "Scans CMS targets"
	Executable       string           `json:"executable"`                  // e.g. "cms-scan"
	Scope            string           `json:"scope,omitempty"`             // "server" or "worker" (default: "worker")
	DefaultMode      JobMode          `json:"default_mode,omitempty"`      // "spread" or "broadcast" (default for worker tools)
	InputFlag        string           `json:"input_flag,omitempty"`        // flag for input file (e.g. "-l", "-f", default: "-l")
	OutputFlag       string           `json:"output_flag,omitempty"`       // flag for output file (e.g. "-o", "-out")
	InputLabel       string           `json:"input_label,omitempty"`       // UI label for the input file field
	InputPlaceholder string           `json:"input_placeholder,omitempty"` // UI placeholder for the input file field
	SharedFiles      []SharedFileSpec `json:"shared_files,omitempty"`      // extra server-side files distributed to all workers
	Options          []ToolOption     `json:"options"`
}

// Message is the envelope for all TCP communication.
type Message struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"request_id,omitempty"`
}

// Message type constants.
const (
	MsgTypeRegisterWorker  = "register_worker"
	MsgTypeWorkerHeartbeat = "worker_heartbeat"
	MsgTypeAssignTask      = "assign_task"
	MsgTypeTaskStream      = "task_stream"
	MsgTypeTaskResult      = "task_result"
	MsgTypeCancelTask      = "cancel_task"
	MsgTypeShutdownWorker  = "shutdown_worker"
)

// Payload structs

type RegisterWorkerPayload struct {
	Worker Worker `json:"worker"`
}

type WorkerHeartbeatPayload struct {
	EnumRequests        map[string]version.BudgetMetrics `json:"enum_requests,omitempty"`
	Resources           cluster.ResourceSnapshot         `json:"resources"`
	ProcessorProtocol   string                           `json:"processor_protocol,omitempty"`
	ProcessorVersions   []string                         `json:"processor_versions,omitempty"`
	ActiveTasks         []string                         `json:"active_tasks,omitempty"`
	ProcessorAttempts   []string                         `json:"processor_attempts,omitempty"`
	ProcessorWorkspaces []string                         `json:"processor_workspaces,omitempty"`
	WorkerID            string                           `json:"worker_id"`
	Status              WorkerStatus                     `json:"status"`
	CurrentLoad         int                              `json:"current_load"`
}

type TaskStreamPayload struct {
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
	Chunk    string `json:"chunk"`
	IsStderr bool   `json:"is_stderr"`
}

type AssignTaskPayload struct {
	ProcessorAssignment *cluster.Assignment `json:"processor_assignment,omitempty"`
	ProcessorInstall    *cluster.Install    `json:"processor_install,omitempty"`
	Task                Task                `json:"task"`
}

type CancelTaskPayload struct {
	TaskID string `json:"task_id"`
}

type TaskResultPayload struct {
	TaskID   string      `json:"task_id"`
	WorkerID string      `json:"worker_id"`
	Result   *TaskResult `json:"result,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// NewMessage serialises payload into a Message envelope.
func NewMessage(msgType string, payload interface{}) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:      msgType,
		Payload:   data,
		Timestamp: time.Now(),
	}, nil
}

// UnmarshalPayload deserialises the raw payload into v.
func (m *Message) UnmarshalPayload(v interface{}) error {
	return json.Unmarshal(m.Payload, v)
}

// ── SSE types ──────────────────────────────────────────────────────────────────

type SSEEventType string

const (
	SSEEventSnapshot SSEEventType = "snapshot"
	SSEEventWorker   SSEEventType = "worker_update"
	SSEEventJob      SSEEventType = "job_update"
	SSEEventTask     SSEEventType = "task_update"
	SSEEventStats    SSEEventType = "stats_update"
)

type SSEEvent struct {
	Type    SSEEventType `json:"type"`
	Payload interface{}  `json:"payload"`
}

type SSESnapshot struct {
	Stats   map[string]int `json:"stats"`
	Workers []*Worker      `json:"workers"`
	Jobs    []*Job         `json:"jobs"`
	Tasks   []*Task        `json:"tasks"`
}
