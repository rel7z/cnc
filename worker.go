package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerAgent connects to the server, receives shell tasks, executes them
// as subprocesses, and reports results back.
type WorkerAgent struct {
	mu     sync.RWMutex
	config *WorkerConfig
	worker *Worker

	// active tasks: taskID → *Task
	tasks map[string]*Task

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// TCP connection state — protected by connMu
	connMu  sync.Mutex
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder

	heartbeatTicker *time.Ticker
	stats           workerStats
	reconnecting    atomic.Bool
}

type WorkerConfig struct {
	ServerAddr string `json:"server_addr"`
	WorkerID   string `json:"worker_id"`
	MaxTasks   int    `json:"max_tasks"`
	DataDir    string `json:"data_dir"`
}

type workerStats struct {
	TasksCompleted uint64
	TasksFailed    uint64
	BytesProcessed uint64
	StartTime      time.Time
}

func DefaultWorkerConfig() *WorkerConfig {
	hostname, _ := os.Hostname()
	return &WorkerConfig{
		ServerAddr: "localhost:9090",
		WorkerID:   fmt.Sprintf("worker_%s_%d", hostname, os.Getpid()),
		MaxTasks:   runtime.NumCPU() * 2,
		DataDir:    "./worker_data",
	}
}

func LoadWorkerConfig(path string) (*WorkerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultWorkerConfig(), err
	}
	var cfg WorkerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.ServerAddr == "" {
		cfg.ServerAddr = "localhost:9090"
	}
	if cfg.WorkerID == "" {
		hostname, _ := os.Hostname()
		cfg.WorkerID = fmt.Sprintf("worker_%s_%d", hostname, os.Getpid())
	}
	if cfg.MaxTasks == 0 {
		cfg.MaxTasks = runtime.NumCPU() * 2
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./worker_data"
	}
	return &cfg, nil
}

func NewWorkerAgent(config *WorkerConfig) *WorkerAgent {
	if config == nil {
		config = DefaultWorkerConfig()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerAgent{
		config: config,
		worker: &Worker{
			ID:         config.WorkerID,
			Address:    config.ServerAddr,
			Status:     WorkerStatusOnline,
			MaxTasks:   config.MaxTasks,
			Registered: time.Now(),
		},
		tasks:  make(map[string]*Task),
		ctx:    ctx,
		cancel: cancel,
		stats:  workerStats{StartTime: time.Now()},
	}
}

// Start connects to the server, registers, and begins processing tasks.
// It blocks until the worker is stopped or fatally fails to reconnect.
func (w *WorkerAgent) Start() error {
	if err := os.MkdirAll(w.config.DataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := w.connect(); err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	if err := w.register(); err != nil {
		return fmt.Errorf("register with server: %w", err)
	}

	w.wg.Add(3)
	go w.heartbeatLoop()
	go w.messageLoop()
	go w.statsReporter()

	log.Printf("Worker %s started — server %s  max_tasks=%d",
		w.config.WorkerID, w.config.ServerAddr, w.config.MaxTasks)

	w.wg.Wait()
	return nil
}

func (w *WorkerAgent) Stop() {
	log.Printf("Stopping worker %s...", w.config.WorkerID)
	w.cancel()
	w.connMu.Lock()
	if w.conn != nil {
		w.conn.Close()
	}
	w.connMu.Unlock()
	if w.heartbeatTicker != nil {
		w.heartbeatTicker.Stop()
	}
	w.wg.Wait()
	log.Printf("Worker %s stopped", w.config.WorkerID)
}

// ── Connection management ─────────────────────────────────────────────────────

func (w *WorkerAgent) connect() error {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	conn, err := net.DialTimeout("tcp", w.config.ServerAddr, 10*time.Second)
	if err != nil {
		return err
	}
	w.conn = conn
	w.encoder = json.NewEncoder(conn)
	w.decoder = json.NewDecoder(conn)
	log.Printf("Connected to server %s", w.config.ServerAddr)
	return nil
}

func (w *WorkerAgent) register() error {
	msg, err := NewMessage(MsgTypeRegisterWorker, RegisterWorkerPayload{Worker: *w.worker})
	if err != nil {
		return err
	}
	return w.send(msg)
}

func (w *WorkerAgent) send(msg *Message) error {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	if w.encoder == nil {
		return fmt.Errorf("not connected")
	}
	return w.encoder.Encode(msg)
}

func (w *WorkerAgent) reconnect() {
	if !w.reconnecting.CompareAndSwap(false, true) {
		return
	}
	defer w.reconnecting.Store(false)

	log.Println("Connection lost — reconnecting...")
	for attempt := 1; attempt <= 10; attempt++ {
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}

		if err := w.connect(); err != nil {
			log.Printf("Reconnect attempt %d/10 failed: %v", attempt, err)
			continue
		}
		if err := w.register(); err != nil {
			log.Printf("Re-registration attempt %d/10 failed: %v", attempt, err)
			continue
		}
		log.Println("Reconnected successfully")
		w.wg.Add(1)
		go w.messageLoop()
		return
	}
	log.Println("Failed to reconnect after 10 attempts — shutting down")
	w.cancel()
}

// ── Loops ─────────────────────────────────────────────────────────────────────

func (w *WorkerAgent) heartbeatLoop() {
	defer w.wg.Done()
	w.heartbeatTicker = time.NewTicker(5 * time.Second)
	defer w.heartbeatTicker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.heartbeatTicker.C:
			w.mu.RLock()
			load := len(w.tasks)
			w.mu.RUnlock()

			msg, _ := NewMessage(MsgTypeWorkerHeartbeat, WorkerHeartbeatPayload{
				WorkerID:    w.config.WorkerID,
				Status:      WorkerStatusOnline,
				CurrentLoad: load,
			})
			if err := w.send(msg); err != nil {
				log.Printf("Heartbeat send failed: %v", err)
			}
		}
	}
}

func (w *WorkerAgent) messageLoop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		w.connMu.Lock()
		dec := w.decoder
		w.connMu.Unlock()

		if dec == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var msg Message
		if err := dec.Decode(&msg); err != nil {
			if err != io.EOF {
				log.Printf("Message read error: %v", err)
			}
			if !w.reconnecting.Load() {
				go w.reconnect()
			}
			return
		}
		w.handleMessage(&msg)
	}
}

func (w *WorkerAgent) statsReporter() {
	defer w.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			log.Printf("Worker %s — completed=%d failed=%d bytes=%d uptime=%s",
				w.config.WorkerID,
				atomic.LoadUint64(&w.stats.TasksCompleted),
				atomic.LoadUint64(&w.stats.TasksFailed),
				atomic.LoadUint64(&w.stats.BytesProcessed),
				time.Since(w.stats.StartTime).Round(time.Second),
			)
		}
	}
}

// ── Message handling ──────────────────────────────────────────────────────────

func (w *WorkerAgent) handleMessage(msg *Message) {
	switch msg.Type {
	case MsgTypeAssignTask:
		w.handleAssignTask(msg)
	case MsgTypeShutdownWorker:
		log.Println("Received shutdown from server")
		w.cancel()
	default:
		// "ack" and unknown types are silently ignored
	}
}

func (w *WorkerAgent) handleAssignTask(msg *Message) {
	var p AssignTaskPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		log.Printf("Invalid assign-task payload: %v", err)
		return
	}
	task := &p.Task
	log.Printf("Received task %s (job %s)", task.ID, task.JobID)

	w.mu.Lock()
	w.tasks[task.ID] = task
	w.mu.Unlock()

	go w.executeTask(task)
}

// ── Task execution ────────────────────────────────────────────────────────────

func (w *WorkerAgent) executeTask(task *Task) {
	result, err := w.executeShellTask(task)

	w.mu.Lock()
	delete(w.tasks, task.ID)
	w.mu.Unlock()

	if err != nil {
		atomic.AddUint64(&w.stats.TasksFailed, 1)
		log.Printf("Task %s failed: %v", task.ID, err)
		w.sendResult(task.ID, nil, err.Error())
	} else {
		atomic.AddUint64(&w.stats.TasksCompleted, 1)
		if result != nil {
			atomic.AddUint64(&w.stats.BytesProcessed, uint64(result.BytesOut))
		}
		log.Printf("Task %s completed", task.ID)
		w.sendResult(task.ID, result, "")
	}
}

// executeShellTask dispatches to the correct execution mode.
func (w *WorkerAgent) executeShellTask(task *Task) (*TaskResult, error) {
	// Extract payload fields with safe type assertions.
	command, _ := task.Payload["command"].(string)
	execModeStr, _ := task.Payload["exec_mode"].(string)
	inputFile, _ := task.Payload["input_file"].(string)
	outputFile, _ := task.Payload["output_file"].(string)
	timeoutSec, _ := task.Payload["timeout_seconds"].(float64)

	if command == "" {
		return nil, fmt.Errorf("task payload missing 'command'")
	}
	if inputFile == "" {
		return nil, fmt.Errorf("task payload missing 'input_file'")
	}

	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeout * time.Second
	}

	// Ensure the output directory exists before execution.
	if outputFile != "" {
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			return nil, fmt.Errorf("create output dir: %w", err)
		}
	}

	switch ExecMode(execModeStr) {
	case ExecModePipe:
		return w.execPipe(task.ID, command, inputFile, outputFile, timeout)
	default: // ExecModeFile and anything unrecognised
		return w.execFile(command, outputFile, timeout)
	}
}

// execFile runs the pre-rendered command as a shell subprocess.
// {input} and {output} were already substituted server-side.
// stderr is captured; a non-zero exit code is treated as an error.
func (w *WorkerAgent) execFile(command, outputFile string, timeout time.Duration) (*TaskResult, error) {
	ctx, cancel := context.WithTimeout(w.ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := stderr.String()
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("command failed: %s", detail)
	}

	// Stat the output file so we can report bytes written.
	var bytesOut int64
	if outputFile != "" {
		if info, err := os.Stat(outputFile); err == nil {
			bytesOut = info.Size()
		}
	}

	return &TaskResult{
		OutputFile: outputFile,
		BytesOut:   bytesOut,
		Message:    "ok",
	}, nil
}

// execPipe feeds the input chunk via stdin and captures stdout to outputFile.
// This mode does not require {input}/{output} placeholders in the command.
func (w *WorkerAgent) execPipe(taskID, command, inputFile, outputFile string, timeout time.Duration) (*TaskResult, error) {
	ctx, cancel := context.WithTimeout(w.ctx, timeout)
	defer cancel()

	// Determine where to write stdout.
	if outputFile == "" {
		outputFile = filepath.Join(w.config.DataDir, fmt.Sprintf("result_%s.txt", taskID))
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			return nil, fmt.Errorf("create output dir: %w", err)
		}
	}

	in, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}
	defer in.Close()

	out, err := os.Create(outputFile)
	if err != nil {
		return nil, fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = in
	cmd.Stdout = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := stderr.String()
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("command failed: %s", detail)
	}

	info, err := out.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat output file: %w", err)
	}

	return &TaskResult{
		OutputFile: outputFile,
		BytesOut:   info.Size(),
		Message:    "ok",
	}, nil
}

// sendResult reports the task outcome back to the server.
func (w *WorkerAgent) sendResult(taskID string, result *TaskResult, errMsg string) {
	msg, err := NewMessage(MsgTypeTaskResult, TaskResultPayload{
		TaskID:   taskID,
		WorkerID: w.config.WorkerID,
		Result:   result,
		Error:    errMsg,
	})
	if err != nil {
		log.Printf("Failed to build result message for task %s: %v", taskID, err)
		return
	}
	if err := w.send(msg); err != nil {
		log.Printf("Failed to send result for task %s: %v", taskID, err)
	}
}

// copyReader is a helper used in pipe mode when we need to tee a reader.
// Kept for potential future use; currently unused.
var _ = io.Copy
