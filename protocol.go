package cnc

import (
	"encoding/json"
	"time"
)

// Configuration constants
const (
	DefaultSplitSize      = 10 * 1024 * 1024 // 10MB
	DefaultTaskQueueSize  = 10000
	DefaultMaxRetries     = 3
	DefaultHeartbeatTTL   = 30 * time.Second
	DefaultPingTimeout    = 800 * time.Millisecond
	DefaultPortTimeout    = 3 * time.Second
	DefaultScanWorkers    = 1000 // Reduced from 5000 to avoid fd limits
	FileReadBufferSize    = 64 * 1024
	DefaultHTTPPort       = ":8080"
	DefaultTCPPort        = ":9090"
)

type TaskType string

const (
	TaskTypeDomainResolve TaskType = "domain_resolve"
	TaskTypeIPScan        TaskType = "ip_scan"
)

type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusAssigned   TaskStatus = "assigned"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusFailed     TaskStatus = "failed"
)

type WorkerStatus string

const (
	WorkerStatusOnline  WorkerStatus = "online"
	WorkerStatusOffline WorkerStatus = "offline"
	WorkerStatusBusy    WorkerStatus = "busy"
)

type Task struct {
	ID          string                 `json:"id"`
	JobID       string                 `json:"job_id"`
	Type        TaskType               `json:"type"`
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

type TaskResult struct {
	OutputFile string                 `json:"output_file,omitempty"`
	Stats      map[string]interface{} `json:"stats,omitempty"`
	LinesOut   int64                  `json:"lines_out"`
	BytesOut   int64                  `json:"bytes_out"`
	Message    string                 `json:"message,omitempty"`
}

type Worker struct {
	ID           string       `json:"id"`
	Address      string       `json:"address"`
	Status       WorkerStatus `json:"status"`
	Capabilities []string     `json:"capabilities"`
	MaxTasks     int          `json:"max_tasks"`
	CurrentLoad  int          `json:"current_load"`
	LastSeen     time.Time    `json:"last_seen"`
	Registered   time.Time    `json:"registered"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Encoder      Encoder      `json:"-"`
}

type Job struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        TaskType  `json:"type"`
	InputFile   string    `json:"input_file"`
	OutputDir   string    `json:"output_dir"`
	SplitSize   int64     `json:"split_size"`
	TotalTasks  int       `json:"total_tasks"`
	Completed   int       `json:"completed"`
	Failed      int       `json:"failed"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Workers     []string  `json:"workers"`
}

type Message struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"request_id,omitempty"`
}

const (
	MsgTypeRegisterWorker    = "register_worker"
	MsgTypeWorkerHeartbeat   = "worker_heartbeat"
	MsgTypeAssignTask        = "assign_task"
	MsgTypeTaskResult        = "task_result"
	MsgTypeTaskStatus        = "task_status"
	MsgTypeGetWorkers        = "get_workers"
	MsgTypeWorkersList       = "workers_list"
	MsgTypeSubmitJob         = "submit_job"
	MsgTypeJobStatus         = "job_status"
	MsgTypeJobList           = "job_list"
	MsgTypeCancelJob         = "cancel_job"
	MsgTypeSplitFile         = "split_file"
	MsgTypeFileSplitResult   = "file_split_result"
	MsgTypeShutdownWorker    = "shutdown_worker"
	MsgTypeError             = "error"
)

type RegisterWorkerPayload struct {
	Worker Worker `json:"worker"`
}

type WorkerHeartbeatPayload struct {
	WorkerID   string `json:"worker_id"`
	Status     WorkerStatus `json:"status"`
	CurrentLoad int    `json:"current_load"`
}

type AssignTaskPayload struct {
	Task Task `json:"task"`
}

type TaskResultPayload struct {
	TaskID   string     `json:"task_id"`
	WorkerID string     `json:"worker_id"`
	Result   *TaskResult `json:"result,omitempty"`
	Error    string     `json:"error,omitempty"`
}

type TaskStatusPayload struct {
	TaskID string     `json:"task_id"`
	Status TaskStatus `json:"status"`
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

type SplitFilePayload struct {
	InputFile string `json:"input_file"`
	SplitSize int64  `json:"split_size"`
	OutputDir string `json:"output_dir"`
	JobID     string `json:"job_id"`
}

type FileSplitResultPayload struct {
	JobID      string   `json:"job_id"`
	SplitFiles []string `json:"split_files"`
	TotalLines int64    `json:"total_lines"`
	Error      string   `json:"error,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

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

func (m *Message) UnmarshalPayload(v interface{}) error {
	return json.Unmarshal(m.Payload, v)
}

type Encoder interface {
	Encode(v interface{}) error
}