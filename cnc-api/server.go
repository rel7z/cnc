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

	// SSE broker — guards sseClients only; never held at the same time as s.mu.
	sseMu      sync.Mutex
	sseClients map[chan SSEEvent]struct{}
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
		workers:    make(map[string]*Worker),
		jobs:       make(map[string]*Job),
		tasks:      make(map[string]*Task),
		jobTasks:   make(map[string][]string),
		taskQueue:  make(chan *Task, DefaultTaskQueueSize),
		config:     config,
		ctx:        ctx,
		cancel:     cancel,
		sseClients: make(map[chan SSEEvent]struct{}),
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
	mux.HandleFunc("/api/events", s.handleEventsAPI)

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

// ── SSE broker ────────────────────────────────────────────────────────────────

// subscribe registers a new SSE client and returns its event channel.
func (s *Server) subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()
	return ch
}

// unsubscribe removes an SSE client and closes its channel.
func (s *Server) unsubscribe(ch chan SSEEvent) {
	s.sseMu.Lock()
	delete(s.sseClients, ch)
	s.sseMu.Unlock()
	close(ch)
}

// broadcast sends an event to all connected SSE clients.
// Non-blocking: slow clients are silently dropped (their channel buffer is full).
// Must NOT be called while holding s.mu.
func (s *Server) broadcast(event SSEEvent) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for ch := range s.sseClients {
		select {
		case ch <- event:
		default:
			// client too slow — skip this event for them
		}
	}
}

// buildSnapshot builds a full-state snapshot under s.mu.RLock.
func (s *Server) buildSnapshot() SSEEvent {
	s.mu.RLock()
	workers := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	stats := s.computeStats()
	s.mu.RUnlock()

	return SSEEvent{
		Type: SSEEventSnapshot,
		Payload: SSESnapshot{
			Stats:   stats,
			Workers: workers,
			Jobs:    jobs,
		},
	}
}

// computeStats builds the stats map. Must be called while s.mu is held (at least RLock).
func (s *Server) computeStats() map[string]int {
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
	return stats
}

// broadcastStats broadcasts a stats_update event. Must NOT hold s.mu when called.
func (s *Server) broadcastStats() {
	s.mu.RLock()
	stats := s.computeStats()
	s.mu.RUnlock()
	s.broadcast(SSEEvent{Type: SSEEventStats, Payload: stats})
}

// handleEventsAPI is the SSE endpoint for browser clients.
// GET /api/events
func (s *Server) handleEventsAPI(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.subscribe()
	defer s.unsubscribe(ch)

	// Send initial snapshot so the client has state immediately.
	snapshot := s.buildSnapshot()
	if data, err := json.Marshal(snapshot.Payload); err == nil {
		fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
		flusher.Flush()
	}

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Payload)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		}
	}
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
	workerCopy := p.Worker // copy for broadcast (SendCh is json:"-")
	s.mu.Unlock()

	log.Printf("Worker registered: %s (max_tasks=%d)", p.Worker.ID, p.Worker.MaxTasks)

	// Acknowledge registration.
	ack, _ := NewMessage("ack", map[string]string{"status": "ok", "worker_id": p.Worker.ID})
	select {
	case sendCh <- ack:
	default:
	}

	// Broadcast after releasing s.mu.
	s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
	s.broadcastStats()

	return p.Worker.ID
}

func (s *Server) handleWorkerHeartbeat(msg *Message) {
	var p WorkerHeartbeatPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		return
	}
	s.mu.Lock()
	var workerCopy *Worker
	if w, ok := s.workers[p.WorkerID]; ok {
		w.LastSeen = time.Now()
		w.Status = p.Status
		w.CurrentLoad = p.CurrentLoad
		cp := *w
		workerCopy = &cp
	}
	s.mu.Unlock()

	if workerCopy != nil {
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: *workerCopy})
	}
}

func (s *Server) handleTaskResult(msg *Message) {
	var p TaskResultPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		log.Printf("Invalid task result payload: %v", err)
		return
	}
	log.Printf("Task result: task=%s worker=%s err=%q", p.TaskID, p.WorkerID, p.Error)

	s.mu.Lock()

	task, ok := s.tasks[p.TaskID]
	if !ok {
		log.Printf("Result for unknown task %s", p.TaskID)
		s.mu.Unlock()
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

	// Capture copies for broadcast before releasing the lock.
	taskCopy := *task
	var jobCopy *Job
	if j, ok := s.jobs[task.JobID]; ok {
		cp := *j
		jobCopy = &cp
	}
	var workerCopy *Worker
	if w, ok := s.workers[p.WorkerID]; ok {
		cp := *w
		workerCopy = &cp
	}

	s.mu.Unlock()

	// Broadcast after releasing s.mu.
	s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
	if jobCopy != nil {
		s.broadcast(SSEEvent{Type: SSEEventJob, Payload: *jobCopy})
	}
	if workerCopy != nil {
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: *workerCopy})
	}
	s.broadcastStats()
}

// ── Job processing ────────────────────────────────────────────────────────────

// submitJob validates and stores a job, then kicks off async processing.
func (s *Server) submitJob(job *Job) {
	if job.Workers <= 0 {
		// Default to number of currently online workers, minimum 1.
		s.mu.RLock()
		for _, w := range s.workers {
			if w.Status == WorkerStatusOnline || w.Status == WorkerStatusBusy {
				job.Workers++
			}
		}
		s.mu.RUnlock()
		if job.Workers <= 0 {
			job.Workers = 1
		}
	}
	// TimeoutSeconds == NoTimeout (-1) means no deadline.
	// Any other non-positive value is treated as the default timeout.
	if job.TimeoutSeconds == 0 {
		job.TimeoutSeconds = DefaultTimeout
	}

	s.mu.Lock()
	s.jobCounter++
	job.ID = fmt.Sprintf("job_%d_%d", time.Now().Unix(), s.jobCounter)
	job.Status = "pending"
	job.CreatedAt = time.Now()
	s.jobs[job.ID] = job
	jobCopy := *job
	s.mu.Unlock()

	log.Printf("Job submitted: %s  command=%q  workers=%d  input=%s", job.ID, job.Command, job.Workers, job.InputFile)

	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})
	s.broadcastStats()

	go s.processJob(job)
}

func (s *Server) processJob(job *Job) {
	s.mu.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	jobCopy := *job
	s.mu.Unlock()

	// Broadcast running status.
	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})

	// Split into exactly job.Workers equal parts using a temp dir under DataDir.
	chunkDir := filepath.Join(s.config.DataDir, "chunks", job.ID)
	splitFiles, err := s.splitInputFileByCount(job.InputFile, job.Workers, chunkDir)
	if err != nil {
		log.Printf("Job %s split error: %v", job.ID, err)
		s.mu.Lock()
		job.Status = "failed"
		failedCopy := *job
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventJob, Payload: failedCopy})
		s.broadcastStats()
		return
	}
	log.Printf("Job %s: split into %d chunks", job.ID, len(splitFiles))

	s.mu.Lock()
	job.TotalTasks = len(splitFiles)
	taskIDs := make([]string, 0, len(splitFiles))

	for i, chunkPath := range splitFiles {
		taskID := fmt.Sprintf("task_%s_%d", job.ID, i)

		// Substitute {input} placeholder with the chunk path.
		renderedCmd := strings.ReplaceAll(job.Command, "{input}", chunkPath)

		task := &Task{
			ID:    taskID,
			JobID: job.ID,
			Type:  "shell",
			Payload: map[string]interface{}{
				"command":         renderedCmd,
				"input_file":      chunkPath,
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
	w, ok := s.workers[workerID]
	var workerCopy *Worker
	if ok && w.Status != WorkerStatusOffline {
		w.Status = WorkerStatusOffline
		w.SendCh = nil
		s.requeueWorkerTasks(workerID)
		log.Printf("Worker %s disconnected", workerID)
		cp := *w
		workerCopy = &cp
	}
	s.mu.Unlock()

	if workerCopy != nil {
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: *workerCopy})
		s.broadcastStats()
	}
}

// ── File splitting ────────────────────────────────────────────────────────────

// splitInputFileByCount divides inputFile into exactly n parts, distributing
// lines as evenly as possible across parts. It does a two-pass approach:
// first count lines, then write equal-sized buckets. For very large files the
// line count pass is fast (sequential scan, no allocation per line).
func (s *Server) splitInputFileByCount(inputFile string, n int, outputDir string) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("split count must be > 0")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// ── Pass 1: count total lines ─────────────────────────────────────────
	f, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}

	var totalLines int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, FileReadBufferSize), 10*FileReadBufferSize)
	for sc.Scan() {
		totalLines++
	}
	if err := sc.Err(); err != nil {
		f.Close()
		return nil, fmt.Errorf("count lines: %w", err)
	}
	f.Close()

	if totalLines == 0 {
		return nil, fmt.Errorf("input file is empty")
	}

	// Cap n to actual line count so we never produce empty parts.
	if int64(n) > totalLines {
		n = int(totalLines)
	}

	linesPerPart := totalLines / int64(n)
	remainder := int(totalLines % int64(n)) // first `remainder` parts get linesPerPart+1

	// ── Pass 2: write parts ───────────────────────────────────────────────
	f, err = os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("open input file (pass 2): %w", err)
	}
	defer f.Close()

	sc = bufio.NewScanner(f)
	sc.Buffer(make([]byte, FileReadBufferSize), 10*FileReadBufferSize)

	splitFiles := make([]string, 0, n)

	for part := 0; part < n; part++ {
		quota := linesPerPart
		if part < remainder {
			quota++
		}

		partPath := filepath.Join(outputDir, fmt.Sprintf("part_%04d.txt", part+1))
		splitFiles = append(splitFiles, partPath)

		out, err := os.Create(partPath)
		if err != nil {
			return nil, fmt.Errorf("create part file: %w", err)
		}

		var written int64
		for written < quota && sc.Scan() {
			if _, err := fmt.Fprintln(out, sc.Text()); err != nil {
				out.Close()
				return nil, fmt.Errorf("write part file: %w", err)
			}
			written++
		}
		out.Close()

		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("scan input file: %w", err)
		}
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
	stats := s.computeStats()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, stats)
}
