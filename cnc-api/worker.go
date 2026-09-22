package cnc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	
	// task cancellation functions: taskID -> cancel func
	taskCancels   map[string]context.CancelFunc
	taskCancelsMu sync.Mutex

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

	// serverHTTPAddr is set once from the ack message after registration.
	// Protected by httpAddrMu.
	httpAddrMu     sync.RWMutex
	serverHTTPAddr string
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
		tasks:       make(map[string]*Task),
		taskCancels: make(map[string]context.CancelFunc),
		ctx:         ctx,
		cancel:      cancel,
		stats:       workerStats{StartTime: time.Now()},
	}
}

// getServerHTTPAddr returns the HTTP address to use when downloading chunks.
// It uses the address received from the server ack if available, otherwise
// derives it from the configured TCP address by replacing the port segment.
func (w *WorkerAgent) getServerHTTPAddr() string {
	w.httpAddrMu.RLock()
	addr := w.serverHTTPAddr
	w.httpAddrMu.RUnlock()
	if addr != "" {
		return addr
	}
	// Fallback: derive HTTP addr from TCP addr.
	// Strip the host:port, replace the port with the default HTTP port.
	host, _, err := net.SplitHostPort(w.config.ServerAddr)
	if err != nil {
		// Can't parse — use the address as-is with default port.
		return "http://" + w.config.ServerAddr
	}
	if host == "" {
		host = "localhost"
	}
	return "http://" + host + DefaultHTTPPort
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
	for attempt := 1; ; attempt++ {
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(backoff(attempt)):
		}

		if err := w.connect(); err != nil {
			log.Printf("Reconnect attempt %d failed: %v (retrying...)", attempt, err)
			continue
		}
		if err := w.register(); err != nil {
			log.Printf("Re-registration attempt %d failed: %v (retrying...)", attempt, err)
			continue
		}
		log.Println("Reconnected successfully")
		w.wg.Add(1)
		go w.messageLoop()
		return
	}
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
			log.Printf("Worker %s — completed=%d failed=%d uptime=%s",
				w.config.WorkerID,
				atomic.LoadUint64(&w.stats.TasksCompleted),
				atomic.LoadUint64(&w.stats.TasksFailed),
				time.Since(w.stats.StartTime).Round(time.Second),
			)
		}
	}
}

// ── Message handling ──────────────────────────────────────────────────────────

func (w *WorkerAgent) handleMessage(msg *Message) {
	switch msg.Type {
	case "ack":
		// Extract http_addr from ack payload and store it.
		var ackPayload map[string]string
		if err := msg.UnmarshalPayload(&ackPayload); err == nil {
			if httpAddr, ok := ackPayload["http_addr"]; ok && httpAddr != "" {
				// Normalise: if the addr is just ":8080", prepend the server host.
				if strings.HasPrefix(httpAddr, ":") {
					host, _, _ := net.SplitHostPort(w.config.ServerAddr)
					if host == "" {
						host = "localhost"
					}
					httpAddr = host + httpAddr
				}
				fullAddr := "http://" + httpAddr
				w.httpAddrMu.Lock()
				w.serverHTTPAddr = fullAddr
				w.httpAddrMu.Unlock()
				log.Printf("Server HTTP address set to %s", fullAddr)
			}
		}
	case MsgTypeAssignTask:
		w.handleAssignTask(msg)
	case MsgTypeCancelTask:
		var p CancelTaskPayload
		if err := msg.UnmarshalPayload(&p); err == nil {
			log.Printf("Received cancellation request for task %s", p.TaskID)
			w.taskCancelsMu.Lock()
			if c, ok := w.taskCancels[p.TaskID]; ok {
				c()
			}
			w.taskCancelsMu.Unlock()
		}
	case MsgTypeShutdownWorker:
		log.Println("Received shutdown from server")
		w.cancel()
	default:
		// Unknown types are silently ignored.
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
	result, infraErr := w.executeShellTask(task)

	w.mu.Lock()
	delete(w.tasks, task.ID)
	w.mu.Unlock()

	if infraErr != nil {
		// Infrastructure error: couldn't even start the task.
		atomic.AddUint64(&w.stats.TasksFailed, 1)
		log.Printf("Task %s infrastructure error: %v", task.ID, infraErr)
		w.sendResult(task.ID, nil, infraErr.Error())
		return
	}

	if result.ExitCode != 0 {
		atomic.AddUint64(&w.stats.TasksFailed, 1)
		log.Printf("Task %s failed (exit=%d): %s", task.ID, result.ExitCode, result.Stderr)
		w.sendResult(task.ID, result, "")
	} else {
		atomic.AddUint64(&w.stats.TasksCompleted, 1)
		log.Printf("Task %s completed (exit=0)", task.ID)
		w.sendResult(task.ID, result, "")
	}
}

// executeShellTask downloads the assigned chunk (if present) to the worker's
// home directory, then runs the command. Returns an infrastructure error if
// the task can't be set up at all; otherwise always returns a *TaskResult
// (with ExitCode reflecting the command's exit status).
func (w *WorkerAgent) executeShellTask(task *Task) (*TaskResult, error) {
	command, _ := task.Payload["command"].(string)
	downloadURL, _ := task.Payload["download_url"].(string)
	destName, _ := task.Payload["dest_name"].(string)
	timeoutSec, _ := task.Payload["timeout_seconds"].(float64)

	if command == "" {
		return nil, fmt.Errorf("task payload missing 'command'")
	}

	// ── 1. Download chunk to ~/<destName> (spread mode only) ─────────────
	var localPath string
	if downloadURL != "" && destName != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		localPath = filepath.Join(home, destName)

		fullURL := w.getServerHTTPAddr() + downloadURL

		log.Printf("Downloading chunk %s → %s", fullURL, localPath)
		if err := downloadFile(fullURL, localPath); err != nil {
			return nil, fmt.Errorf("download chunk: %w", err)
		}
	}

	// ── 2. Substitute {input} with the local file path ───────────────────
	if localPath != "" {
		command = strings.ReplaceAll(command, "{input}", localPath)
	}

	// ── 3. Build execution context ────────────────────────────────────────
	var ctx context.Context
	var cancel context.CancelFunc
	if int(timeoutSec) == NoTimeout {
		ctx, cancel = context.WithCancel(w.ctx)
	} else {
		timeout := time.Duration(timeoutSec) * time.Second
		if timeout <= 0 {
			timeout = DefaultTimeout * time.Second
		}
		ctx, cancel = context.WithTimeout(w.ctx, timeout)
	}
	
	w.taskCancelsMu.Lock()
	w.taskCancels[task.ID] = cancel
	w.taskCancelsMu.Unlock()
	
	defer func() {
		cancel()
		w.taskCancelsMu.Lock()
		delete(w.taskCancels, task.ID)
		w.taskCancelsMu.Unlock()
	}()

	// ── 4. Run the command ────────────────────────────────────────────────
	cwd, _ := os.Getwd()
	execDir := ""
	if exe, err := os.Executable(); err == nil {
		execDir = filepath.Dir(exe)
	}

	// Prepare PATH to include local tools and common binary directories
	pathEnv := os.Getenv("PATH")
	var extraPaths []string
	if cwd != "" {
		extraPaths = append(extraPaths, filepath.Join(cwd, "tools"), cwd)
	}
	if execDir != "" && execDir != cwd {
		extraPaths = append(extraPaths, filepath.Join(execDir, "tools"), execDir)
	}
	extraPaths = append(extraPaths, "/usr/local/bin", "/opt/homebrew/bin")

	newPath := strings.Join(append(extraPaths, pathEnv), string(os.PathListSeparator))

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = os.Environ()
	pathFound := false
	for i, env := range cmd.Env {
		if strings.HasPrefix(env, "PATH=") {
			cmd.Env[i] = "PATH=" + newPath
			pathFound = true
			break
		}
	}
	if !pathFound {
		cmd.Env = append(cmd.Env, "PATH="+newPath)
	}

	var stdout, stderr bytes.Buffer
	
	stdoutStream := &BufferedStreamWriter{w: w, taskID: task.ID, isStderr: false}
	stderrStream := &BufferedStreamWriter{w: w, taskID: task.ID, isStderr: true}
	
	cmd.Stdout = io.MultiWriter(&stdout, stdoutStream)
	cmd.Stderr = io.MultiWriter(&stderr, stderrStream)

	runErr := cmd.Run()
	stdoutStream.Close()
	stderrStream.Close()

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Non-exit error (killed by signal, context cancelled, etc.)
			exitCode = 1
		}
	}

	stderrOutput := stderr.String()
	if exitCode == 127 && !strings.Contains(stderrOutput, "[diagnostic]") {
		stderrOutput = strings.TrimRight(stderrOutput, "\n") +
			"\n[diagnostic] Command not found (exit 127). Ensure the tool is built in ./tools/ or installed in worker PATH.\n"
	}

	result := &TaskResult{
		Stdout:   stdout.String(),
		Stderr:   stderrOutput,
		ExitCode: exitCode,
	}
	return result, nil
}

// downloadFile fetches url and writes it to dest, creating or truncating the file.
func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
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

// backoff returns a wait duration for reconnect attempt n.
// Caps at 30 seconds so it doesn't wait forever between retries.
func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 3 * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// ── Streaming support ─────────────────────────────────────────────────────────

type BufferedStreamWriter struct {
	w        *WorkerAgent
	taskID   string
	isStderr bool
	mu       sync.Mutex
	buf      bytes.Buffer
	timer    *time.Timer
}

func (b *BufferedStreamWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf.Write(p)

	if b.timer == nil {
		b.timer = time.AfterFunc(250*time.Millisecond, b.flush)
	}

	if b.buf.Len() >= 4096 {
		b.flushLocked()
	}

	return len(p), nil
}

func (b *BufferedStreamWriter) flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushLocked()
}

func (b *BufferedStreamWriter) flushLocked() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if b.buf.Len() == 0 {
		return
	}
	
	chunk := b.buf.String()
	b.buf.Reset()

	msg, err := NewMessage(MsgTypeTaskStream, TaskStreamPayload{
		TaskID:   b.taskID,
		WorkerID: b.w.config.WorkerID,
		Chunk:    chunk,
		IsStderr: b.isStderr,
	})
	if err == nil {
		_ = b.w.send(msg)
	}
}

func (b *BufferedStreamWriter) Close() error {
	b.flush()
	return nil
}
