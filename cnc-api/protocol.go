package cnc

import (
	"encoding/json"
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
// Payload keys set by the server:
//
//	"command"         string  — shell command with {input} already substituted
//	"input_file"      string  — path to the chunk file on the server
//	"timeout_seconds" float64 — kill timeout (0 = DefaultTimeout)
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
}

// TaskResult holds the outcome of a completed task.
type TaskResult struct {
	Message string `json:"message,omitempty"`
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

// Job is a user-submitted unit of work.
// The server splits the input file into exactly Workers parts and
// dispatches one chunk per worker.
type Job struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Command        string     `json:"command"`         // e.g. "node index.js {input}"
	InputFile      string     `json:"input_file"`
	Workers        int        `json:"workers"`         // number of parts to split into
	TimeoutSeconds int        `json:"timeout_seconds"` // 0 = DefaultTimeout
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

// Message type constants.
const (
	MsgTypeRegisterWorker  = "register_worker"
	MsgTypeWorkerHeartbeat = "worker_heartbeat"
	MsgTypeAssignTask      = "assign_task"
	MsgTypeTaskResult      = "task_result"
	MsgTypeShutdownWorker  = "shutdown_worker"
)

// Payload structs

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
}
