package cnc

import (
	"encoding/json"
	"time"
)

// Configuration constants
const (
	DefaultSplitSize     = 10 * 1024 * 1024 // 10MB
	DefaultTaskQueueSize = 10000
	DefaultMaxRetries    = 3
	DefaultHeartbeatTTL  = 30 * time.Second
	FileReadBufferSize   = 64 * 1024
	DefaultHTTPPort      = ":8080"
	DefaultTCPPort       = ":9090"
	DefaultTimeout       = 300 // seconds
)

// ExecMode controls how a shell command receives input and produces output.
type ExecMode string

const (
	// ExecModeFile: worker passes file paths; command uses {input} and {output} placeholders.
	ExecModeFile ExecMode = "file"
	// ExecModePipe: input chunk is fed via stdin; stdout is captured as output.
	ExecModePipe ExecMode = "pipe"
)

// TaskStatus tracks the lifecycle of a single task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusAssigned  TaskStatus = "assigned"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// WorkerStatus tracks whether a worker is available.
type WorkerStatus string

const (
	WorkerStatusOnline  WorkerStatus = "online"
	WorkerStatusOffline WorkerStatus = "offline"
	WorkerStatusBusy    WorkerStatus = "busy"
)

// Task is a single unit of work dispatched to a worker.
// Payload always contains the following keys (set by the server):
//   - "command"          string  — fully rendered shell command (file mode) or raw command (pipe mode)
//   - "exec_mode"        string  — "file" or "pipe"
//   - "input_file"       string  — path to the input chunk (file mode) or the file to pipe in (pipe mode)
//   - "output_file"      string  — expected output path (file mode); worker writes stdout here (pipe mode)
//   - "timeout_seconds"  float64 — seconds before the subprocess is killed (0 = use default)
type Task struct {
	ID          string                 `json:"id"`
	JobID       string                 `json:"job_id"`
	Type        string                 `json:"type"` // always "shell"
	Payload     map[string]interface{} `json:"payload"`
	Status      TaskStatus             `json:"status"`
	AssignedTo  string                 `json:"assigned_to,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Result      *TaskResult            `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	RetryCount  int                    `json:"retry_count"`
	Priority    int                    `json:"priority"`
}

// TaskResult holds the outcome of a completed task.
type TaskResult struct {
	OutputFile string                 `json:"output_file,omitempty"`
	Stats      map[string]interface{} `json:"stats,omitempty"`
	BytesOut   int64                  `json:"bytes_out"`
	Message    string                 `json:"message,omitempty"`
}

// Worker represents a connected worker agent.
type Worker struct {
	ID          string            `json:"id"`
	Address     string            `json:"address"`
	Status      WorkerStatus      `json:"status"`
	MaxTasks    int               `json:"max_tasks"`
	CurrentLoad int               `json:"current_load"`
	LastSeen    time.Time         `json:"last_seen"`
	Registered  time.Time         `json:"registered"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	SendCh      chan *Message      `json:"-"` // non-blocking send channel; not serialised
}

// Job is a user-submitted unit of work. The server splits the input file
// and distributes one Task per chunk to available workers.
type Job struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Command        string     `json:"command"`          // e.g. "nmap -iL {input} -oN {output}"
	ExecMode       ExecMode   `json:"exec_mode"`        // "file" or "pipe"
	TimeoutSeconds int        `json:"timeout_seconds"`  // 0 = DefaultTimeout
	InputFile      string     `json:"input_file"`
	OutputDir      string     `json:"output_dir"`
	SplitSize      int64      `json:"split_size"`       // 0 = DefaultSplitSize
	TotalTasks     int        `json:"total_tasks"`
	Completed      int        `json:"completed"`
	Failed         int        `json:"failed"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Message is the envelope for all TCP communication.
type Message struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"request_id,omitempty"`
}

// Message type constants — only what is actually used over TCP.
const (
	MsgTypeRegisterWorker  = "register_worker"
	MsgTypeWorkerHeartbeat = "worker_heartbeat"
	MsgTypeAssignTask      = "assign_task"
	MsgTypeTaskResult      = "task_result"
	MsgTypeSubmitJob       = "submit_job"
	MsgTypeJobStatus       = "job_status"
	MsgTypeJobList         = "job_list"
	MsgTypeCancelJob       = "cancel_job"
	MsgTypeShutdownWorker  = "shutdown_worker"
	MsgTypeError           = "error"
)

// --- Payload structs ---

type RegisterWorkerPayload struct {
	Worker Worker `json:"worker"`
}

type WorkerHeartbeatPayload struct {
	WorkerID    string       `json:"worker_id"`
	Status      WorkerStatus `json:"status"`
	CurrentLoad int          `json:"current_load"`
}

type AssignTaskPayload struct {
	Task Task `json:"task"`
}

type TaskResultPayload struct {
	TaskID   string      `json:"task_id"`
	WorkerID string      `json:"worker_id"`
	Result   *TaskResult `json:"result,omitempty"`
	Error    string      `json:"error,omitempty"`
}

type SubmitJobPayload struct {
	Job Job `json:"job"`
}

type JobStatusPayload struct {
	JobID string `json:"job_id"`
}

type JobListPayload struct {
	Jobs []Job `json:"jobs"`
}

type CancelJobPayload struct {
	JobID string `json:"job_id"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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
