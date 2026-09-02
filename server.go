package cnc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Server is the CNC command-and-control server. It accepts worker connections
// over TCP, exposes an HTTP API for job management, splits input files into
// chunks, and dispatches tasks to available workers.
type Server struct {
	mu          sync.RWMutex
	workers     map[string]*Worker
	jobs        map[string]*Job
	tasks       map[string]*Task
	jobTasks    map[string][]string // jobID -> []taskID
	taskQueue   chan *Task
	httpServer  *http.Server
	tcpListener net.Listener
	config      *ServerConfig
	jobCounter  int
	taskCounter int
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

type ServerConfig struct {
	HTTPAddr     string `json:"http_addr"`
	TCPAddr      string `json:"tcp_addr"`
	DataDir      string `json:"data_dir"`
	MaxRetries   int    `json:"max_retries"`
	HeartbeatTTL string `json:"heartbeat_ttl"`
}

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		HTTPAddr:     DefaultHTTPPort,
		TCPAddr:      DefaultTCPPort,
		DataDir:      "./cnc_data",
		MaxRetries:   DefaultMaxRetries,
		HeartbeatTTL: "30s",
	}
}

func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultServerConfig(), err
	}
	var config ServerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if config.HTTPAddr == "" {
		config.HTTPAddr = DefaultHTTPPort
	}
	if config.TCPAddr == "" {
		config.TCPAddr = DefaultTCPPort
	}
	if config.DataDir == "" {
		config.DataDir = "./cnc_data"
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = DefaultMaxRetries
	}
	if config.HeartbeatTTL == "" {
		config.HeartbeatTTL = "30s"
	}
	return &config, nil
}

func (s *Server) heartbeatTTL() time.Duration {
	d, err := time.ParseDuration(s.config.HeartbeatTTL)
	if err != nil {
		return DefaultHeartbeatTTL
	}
	return d
}

func NewServer(config *ServerConfig) *Server {
	if config == nil {
		config = DefaultServerConfig()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		workers:   make(map[string]*Worker),
		jobs:      make(map[string]*Job),
		tasks:     make(map[string]*Task),
		jobTasks:  make(map[string][]string),
		taskQueue: make(chan *Task, DefaultTaskQueueSize),
		config:    config,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start runs all server components. It blocks on the HTTP listener.
func (s *Server) Start() error {
	if err := os.MkdirAll(s.config.DataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	s.wg.Add(1)
	go s.taskDispatcher()

	s.wg.Add(1)
	go s.heartbeatChecker()

	s.wg.Add(1)
	go s.tcpServer()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/workers", s.handleWorkersAPI)
	mux.HandleFunc("/api/jobs", s.handleJobsAPI)
	mux.HandleFunc("/api/jobs/", s.handleJobByIDAPI)
	mux.HandleFunc("/api/tasks", s.handleTasksAPI)
	mux.HandleFunc("/api/stats", s.handleStatsAPI)

	s.httpServer = &http.Server{
		Addr:    s.config.HTTPAddr,
		Handler: mux,
	}

	log.Printf("CNC Server starting — HTTP %s  TCP %s", s.config.HTTPAddr, s.config.TCPAddr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Stop() {
	log.Println("Stopping CNC Server...")
	s.cancel()
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx) //nolint:errcheck
	}
	if s.tcpListener != nil {
		s.tcpListener.Close()
	}
	s.wg.Wait()
	log.Println("CNC Server stopped")
}

// ── TCP server ───────────────────────────────────────────────────────────────

func (s *Server) tcpServer() {
	defer s.wg.Done()

	ln, err := net.Listen("tcp", s.config.TCPAddr)
	if err != nil {
		log.Printf("TCP listen error: %v", err)
		return
	}
	s.tcpListener = ln
	defer ln.Close()
	log.Printf("TCP server listening on %s", s.config.TCPAddr)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.ctx.Done():
					return
				default:
					log.Printf("TCP accept error: %v", err)
					continue
				}
			}
			s.wg.Add(1)
			go s.handleTCPConn(conn)
		}
	}
}

// handleTCPConn manages one persistent worker connection.
// Inbound messages are decoded in a read loop.
// Outbound messages are sent by a dedicated write goroutine via Worker.SendCh,
// which eliminates the TCP blocking bug caused by holding the mutex while writing.
func (s *Server) handleTCPConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()
	log.Printf("New TCP connection from %s", conn.RemoteAddr())

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	// sendCh is a buffered channel owned by this connection.
	// The write goroutine below is the only writer to the TCP socket.
	sendCh := make(chan *Message, 256)

	// Write goroutine — drains sendCh and writes to the socket.
	writeCtx, writeCancel := context.WithCancel(s.ctx)
	defer writeCancel()

	go func() {
		for {
			select {
			case <-writeCtx.Done():
				return
			case msg, ok := <-sendCh:
				if !ok {
					return
				}
				if err := encoder.Encode(msg); err != nil {
					log.Printf("TCP write error to %s: %v", conn.RemoteAddr(), err)
					writeCancel()
					return
				}
			}
		}
	}()

	// Read loop — decode messages and dispatch handlers.
	var registeredWorkerID string
	for {
		select {
		case <-writeCtx.Done():
			// write goroutine died or server is shutting down
			goto cleanup
		default:
		}

		var msg Message
		if err := decoder.Decode(&msg); err != nil {
			if err != io.EOF {
				log.Printf("TCP read error from %s: %v", conn.RemoteAddr(), err)
			}
			goto cleanup
		}

		switch msg.Type {
		case MsgTypeRegisterWorker:
			registeredWorkerID = s.handleRegisterWorker(&msg, sendCh)

		case MsgTypeWorkerHeartbeat:
			s.handleWorkerHeartbeat(&msg)

		case MsgTypeTaskResult:
			s.handleTaskResult(&msg)

		case MsgTypeShutdownWorker:
			log.Printf("Worker at %s requested shutdown", conn.RemoteAddr())
			goto cleanup

		default:
			log.Printf("Unknown message type from %s: %s", conn.RemoteAddr(), msg.Type)
		}
	}

cleanup:
	writeCancel()
	if registeredWorkerID != "" {
		s.markWorkerOffline(registeredWorkerID)
	}
}

// ── Message handlers ─────────────────────────────────────────────────────────

func (s *Server) handleRegisterWorker(msg *Message, sendCh chan *Message) string {
	var p RegisterWorkerPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		log.Printf("Invalid register payload: %v", err)
		return ""
	}

	s.mu.Lock()
	p.Worker.Registered = time.Now()
	p.Worker.LastSeen = time.Now()
	p.Worker.Status = WorkerStatusOnline
	p.Worker.SendCh = sendCh
	s.workers[p.Worker.ID] = &p.Worker
	s.mu.Unlock()

	log.Printf("Worker registered: %s (max_tasks=%d)", p.Worker.ID, p.Worker.MaxTasks)

	// Acknowledge registration.
	ack, _ := NewMessage("ack", map[string]string{"status": "ok", "worker_id": p.Worker.ID})
	select {
	case sendCh <- ack:
	default:
	}

	return p.Worker.ID
}

func (s *Server) handleWorkerHeartbeat(msg *Message) {
	var p WorkerHeartbeatPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		return
	}
	s.mu.Lock()
	if w, ok := s.workers[p.WorkerID]; ok {
		w.LastSeen = time.Now()
		w.Status = p.Status
		w.CurrentLoad = p.CurrentLoad
	}
	s.mu.Unlock()
}

func (s *Server) handleTaskResult(msg *Message) {
	var p TaskResultPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		log.Printf("Invalid task result payload: %v", err)
		return
	}
	log.Printf("Task result: task=%s worker=%s err=%q", p.TaskID, p.WorkerID, p.Error)

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[p.TaskID]
	if !ok {
		log.Printf("Result for unknown task %s", p.TaskID)
		return
	}

	now := time.Now()
	if p.Error != "" {
		task.Error = p.Error
		task.RetryCount++
		if task.RetryCount < s.config.MaxRetries {
			task.Status = TaskStatusPending
			task.AssignedTo = ""
			go func(t *Task) { s.taskQueue <- t }(task)
			log.Printf("Task %s requeued (retry %d/%d)", task.ID, task.RetryCount, s.config.MaxRetries)
		} else {
			task.Status = TaskStatusFailed
			log.Printf("Task %s permanently failed after %d retries", task.ID, task.RetryCount)
		}
	} else {
		task.Status = TaskStatusCompleted
		task.CompletedAt = &now
		task.Result = p.Result
	}

	s.updateJobProgress(task.JobID)

	if w, ok := s.workers[p.WorkerID]; ok {
		w.CurrentLoad--
		if w.CurrentLoad < 0 {
			w.CurrentLoad = 0
		}
		if w.CurrentLoad < w.MaxTasks {
			w.Status = WorkerStatusOnline
		}
	}
}

// ── Job processing ────────────────────────────────────────────────────────────

// submitJob validates and stores a job, then kicks off async processing.
func (s *Server) submitJob(job *Job) {
	if job.ExecMode == "" {
		job.ExecMode = ExecModeFile
	}
	if job.SplitSize <= 0 {
		job.SplitSize = DefaultSplitSize
	}
	if job.TimeoutSeconds <= 0 {
		job.TimeoutSeconds = DefaultTimeout
	}

	s.mu.Lock()
	s.jobCounter++
	job.ID = fmt.Sprintf("job_%d_%d", time.Now().Unix(), s.jobCounter)
	job.Status = "pending"
	job.CreatedAt = time.Now()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	log.Printf("Job submitted: %s  command=%q  mode=%s  input=%s", job.ID, job.Command, job.ExecMode, job.InputFile)
	go s.processJob(job)
}

func (s *Server) processJob(job *Job) {
	s.mu.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	s.mu.Unlock()

	splitFiles, err := s.splitInputFile(job.InputFile, job.SplitSize, job.OutputDir)
	if err != nil {
		log.Printf("Job %s split error: %v", job.ID, err)
		s.mu.Lock()
		job.Status = "failed"
		s.mu.Unlock()
		return
	}
	log.Printf("Job %s: split into %d chunks", job.ID, len(splitFiles))

	s.mu.Lock()
	job.TotalTasks = len(splitFiles)
	taskIDs := make([]string, 0, len(splitFiles))

	for i, chunkPath := range splitFiles {
		taskID := fmt.Sprintf("task_%s_%d", job.ID, i)

		// Determine output file path for this chunk.
		outputFile := filepath.Join(job.OutputDir, fmt.Sprintf("result_part_%04d.txt", i+1))

		// Render the command: replace {input} and {output} placeholders.
		renderedCmd := strings.ReplaceAll(job.Command, "{input}", chunkPath)
		renderedCmd = strings.ReplaceAll(renderedCmd, "{output}", outputFile)

		task := &Task{
			ID:    taskID,
			JobID: job.ID,
			Type:  "shell",
			Payload: map[string]interface{}{
				"command":         renderedCmd,
				"exec_mode":       string(job.ExecMode),
				"input_file":      chunkPath,
				"output_file":     outputFile,
				"timeout_seconds": job.TimeoutSeconds,
			},
			Status:    TaskStatusPending,
			CreatedAt: time.Now(),
		}
		s.tasks[taskID] = task
		taskIDs = append(taskIDs, taskID)

		select {
		case s.taskQueue <- task:
		default:
			// Queue full — push in background so we don't hold the lock.
			go func(t *Task) { s.taskQueue <- t }(task)
		}
	}
	s.jobTasks[job.ID] = taskIDs
	s.mu.Unlock()
}

func (s *Server) updateJobProgress(jobID string) {
	job, ok := s.jobs[jobID]
	if !ok {
		return
	}
	taskIDs := s.jobTasks[jobID]

	completed, failed := 0, 0
	for _, tid := range taskIDs {
		t, ok := s.tasks[tid]
		if !ok {
			continue
		}
		switch t.Status {
		case TaskStatusCompleted:
			completed++
		case TaskStatusFailed:
			failed++
		}
	}
	job.Completed = completed
	job.Failed = failed

	if completed+failed >= job.TotalTasks && job.TotalTasks > 0 {
		job.Status = "completed"
		now := time.Now()
		job.CompletedAt = &now
		log.Printf("Job %s completed: %d ok  %d failed", jobID, completed, failed)
	}
}

// ── Task dispatcher ───────────────────────────────────────────────────────────

func (s *Server) taskDispatcher() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case task := <-s.taskQueue:
			s.dispatchTask(task)
		}
	}
}

func (s *Server) dispatchTask(task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if task.Status != TaskStatusPending {
		return
	}

	// Find an available worker whose capabilities match ("shell" is universal).
	var chosen *Worker
	for _, w := range s.workers {
		if w.Status == WorkerStatusOnline && w.CurrentLoad < w.MaxTasks && w.SendCh != nil {
			chosen = w
			break
		}
	}

	if chosen == nil {
		// No worker available — requeue after a short delay.
		go func() {
			time.Sleep(200 * time.Millisecond)
			s.taskQueue <- task
		}()
		return
	}

	task.Status = TaskStatusAssigned
	task.AssignedTo = chosen.ID
	now := time.Now()
	task.StartedAt = &now
	chosen.CurrentLoad++
	if chosen.CurrentLoad >= chosen.MaxTasks {
		chosen.Status = WorkerStatusBusy
	}

	msg, err := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: *task})
	if err != nil {
		log.Printf("Failed to build assign-task message: %v", err)
		s.requeueTask(task, chosen)
		return
	}

	// Non-blocking send into the worker's send channel.
	// If the channel is full the worker is overloaded — requeue.
	select {
	case chosen.SendCh <- msg:
		log.Printf("Dispatched task %s → worker %s", task.ID, chosen.ID)
	default:
		log.Printf("Worker %s send buffer full, requeueing task %s", chosen.ID, task.ID)
		s.requeueTask(task, chosen)
	}
}

func (s *Server) requeueTask(task *Task, w *Worker) {
	task.Status = TaskStatusPending
	task.AssignedTo = ""
	if w != nil {
		w.CurrentLoad--
		if w.CurrentLoad < 0 {
			w.CurrentLoad = 0
		}
	}
	go func() { s.taskQueue <- task }()
}

// ── Heartbeat checker ─────────────────────────────────────────────────────────

func (s *Server) heartbeatChecker() {
	defer s.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, w := range s.workers {
				if time.Since(w.LastSeen) > s.heartbeatTTL() && w.Status != WorkerStatusOffline {
					w.Status = WorkerStatusOffline
					w.SendCh = nil
					log.Printf("Worker %s marked offline (last seen %s ago)", id, time.Since(w.LastSeen).Round(time.Second))
					s.requeueWorkerTasks(id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) requeueWorkerTasks(workerID string) {
	for _, task := range s.tasks {
		if task.AssignedTo != workerID {
			continue
		}
		if task.Status != TaskStatusAssigned && task.Status != TaskStatusRunning {
			continue
		}
		task.RetryCount++
		if task.RetryCount < s.config.MaxRetries {
			task.Status = TaskStatusPending
			task.AssignedTo = ""
			go func(t *Task) { s.taskQueue <- t }(task)
			log.Printf("Requeued task %s from offline worker %s", task.ID, workerID)
		} else {
			task.Status = TaskStatusFailed
			task.Error = "worker went offline, max retries exceeded"
			s.updateJobProgress(task.JobID)
		}
	}
}

func (s *Server) markWorkerOffline(workerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[workerID]
	if !ok {
		return
	}
	if w.Status != WorkerStatusOffline {
		w.Status = WorkerStatusOffline
		w.SendCh = nil
		s.requeueWorkerTasks(workerID)
		log.Printf("Worker %s disconnected", workerID)
	}
}

// ── File splitting ────────────────────────────────────────────────────────────

func (s *Server) splitInputFile(inputFile string, splitSize int64, outputDir string) ([]string, error) {
	f, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}
	defer f.Close()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	var splitFiles []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, FileReadBufferSize), 10*FileReadBufferSize)

	partNum := 0
	var current *os.File
	var currentSize int64

	for scanner.Scan() {
		line := scanner.Text()
		lineSize := int64(len(line)) + 1

		if current == nil || currentSize+lineSize > splitSize {
			if current != nil {
				current.Close()
			}
			partNum++
			path := filepath.Join(outputDir, fmt.Sprintf("part_%04d.txt", partNum))
			splitFiles = append(splitFiles, path)
			current, err = os.Create(path)
			if err != nil {
				return nil, fmt.Errorf("create part file: %w", err)
			}
			currentSize = 0
		}

		if _, err := fmt.Fprintln(current, line); err != nil {
			current.Close()
			return nil, fmt.Errorf("write part file: %w", err)
		}
		currentSize += lineSize
	}
	if current != nil {
		current.Close()
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan input file: %w", err)
	}
	return splitFiles, nil
}

// ── HTTP API ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (s *Server) handleWorkersAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	list := make([]*Worker, 0, len(s.workers))
	for _, wk := range s.workers {
		list = append(list, wk)
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleJobsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var job Job
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if job.Command == "" {
			http.Error(w, "command is required", http.StatusBadRequest)
			return
		}
		if job.InputFile == "" {
			http.Error(w, "input_file is required", http.StatusBadRequest)
			return
		}
		if job.OutputDir == "" {
			http.Error(w, "output_dir is required", http.StatusBadRequest)
			return
		}
		s.submitJob(&job)
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "job_id": job.ID})

	case http.MethodGet:
		s.mu.RLock()
		jobs := make([]*Job, 0, len(s.jobs))
		for _, j := range s.jobs {
			jobs = append(jobs, j)
		}
		s.mu.RUnlock()
		writeJSON(w, http.StatusOK, jobs)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleJobByIDAPI(w http.ResponseWriter, r *http.Request) {
	// Path: /api/jobs/{id}
	jobID := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	if jobID == "" {
		http.Error(w, "job id required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		job, ok := s.jobs[jobID]
		s.mu.RUnlock()
		if !ok {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, job)

	case http.MethodDelete:
		s.mu.Lock()
		job, ok := s.jobs[jobID]
		if ok {
			job.Status = "cancelled"
		}
		s.mu.Unlock()
		if !ok {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTasksAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleStatsAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]int{
		"workers_total":   len(s.workers),
		"workers_online":  0,
		"jobs_total":      len(s.jobs),
		"jobs_running":    0,
		"tasks_total":     len(s.tasks),
		"tasks_pending":   0,
		"tasks_running":   0,
		"tasks_completed": 0,
		"tasks_failed":    0,
	}

	for _, wk := range s.workers {
		if wk.Status == WorkerStatusOnline || wk.Status == WorkerStatusBusy {
			stats["workers_online"]++
		}
	}
	for _, j := range s.jobs {
		if j.Status == "running" {
			stats["jobs_running"]++
		}
	}
	for _, t := range s.tasks {
		switch t.Status {
		case TaskStatusPending:
			stats["tasks_pending"]++
		case TaskStatusAssigned, TaskStatusRunning:
			stats["tasks_running"]++
		case TaskStatusCompleted:
			stats["tasks_completed"]++
		case TaskStatusFailed:
			stats["tasks_failed"]++
		}
	}

	writeJSON(w, http.StatusOK, stats)
}
