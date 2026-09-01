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
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Server struct {
	mu           sync.RWMutex
	workers      map[string]*Worker
	jobs         map[string]*Job
	tasks        map[string]*Task
	jobTasks     map[string][]string // jobID -> []taskID
	taskQueue    chan *Task
	upgrader     websocket.Upgrader
	httpServer   *http.Server
	tcpListener  net.Listener
	config       *ServerConfig
	jobCounter   int
	taskCounter  int
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
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

func (s *Server) getHeartbeatTTL() time.Duration {
	if s.config.HeartbeatTTL == "" {
		return DefaultHeartbeatTTL
	}
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
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) Start() error {
	if err := os.MkdirAll(s.config.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data dir: %w", err)
	}

	s.wg.Add(1)
	go s.taskDispatcher()

	s.wg.Add(1)
	go s.heartbeatChecker()

	s.wg.Add(1)
	go s.tcpServer()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/workers", s.handleWorkersAPI)
	mux.HandleFunc("/api/jobs", s.handleJobsAPI)
	mux.HandleFunc("/api/tasks", s.handleTasksAPI)
	mux.HandleFunc("/api/stats", s.handleStatsAPI)
	mux.HandleFunc("/api/split", s.handleSplitAPI)

	s.httpServer = &http.Server{
		Addr:    s.config.HTTPAddr,
		Handler: mux,
	}

	log.Printf("CNC Server starting on HTTP %s, TCP %s", s.config.HTTPAddr, s.config.TCPAddr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Stop() {
	log.Println("Stopping CNC Server...")
	s.cancel()
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}
	if s.tcpListener != nil {
		s.tcpListener.Close()
	}
	s.wg.Wait()
	log.Println("CNC Server stopped")
}

func (s *Server) tcpServer() {
	defer s.wg.Done()
	ln, err := net.Listen("tcp", s.config.TCPAddr)
	if err != nil {
		log.Printf("TCP server error: %v", err)
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
			go s.handleTCPConnection(conn)
		}
	}
}

func (s *Server) handleTCPConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	log.Printf("New TCP connection from %s", conn.RemoteAddr())

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)
	var encoderMu sync.Mutex

	// Helper to send with lock
	sendResponse := func(v interface{}) error {
		encoderMu.Lock()
		defer encoderMu.Unlock()
		return encoder.Encode(v)
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var msg Message
			if err := decoder.Decode(&msg); err != nil {
				if err != io.EOF {
					log.Printf("TCP decode error from %s: %v", conn.RemoteAddr(), err)
				}
				return
			}
			// Handle synchronously with response helper
			s.handleMessageSync(&msg, sendResponse)
		}
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("New WebSocket connection from %s", r.RemoteAddr)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				log.Printf("WebSocket read error: %v", err)
				return
			}
			s.handleMessage(&msg, &wsEncoder{conn: conn})
		}
	}
}

type ResponseFunc func(interface{}) error

func (s *Server) handleMessageSync(msg *Message, sendResponse ResponseFunc) {
	switch msg.Type {
	case MsgTypeRegisterWorker:
		s.handleRegisterWorkerSync(msg, sendResponse)
	case MsgTypeWorkerHeartbeat:
		s.handleWorkerHeartbeatSync(msg)
	case MsgTypeTaskResult:
		s.handleTaskResultSync(msg)
	case MsgTypeGetWorkers:
		s.handleGetWorkersSync(sendResponse)
	case MsgTypeSubmitJob:
		s.handleSubmitJobSync(msg, sendResponse)
	case MsgTypeJobStatus:
		s.handleJobStatusSync(msg, sendResponse)
	case MsgTypeJobList:
		s.handleJobListSync(sendResponse)
	case MsgTypeCancelJob:
		s.handleCancelJobSync(msg, sendResponse)
	case MsgTypeSplitFile:
		s.handleSplitFileSync(msg, sendResponse)
	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

func (s *Server) handleRegisterWorkerSync(msg *Message, sendResponse ResponseFunc) {
	var payload RegisterWorkerPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		sendResponse(ErrorPayload{Code: "INVALID_PAYLOAD", Message: err.Error()})
		return
	}

	s.mu.Lock()
	payload.Worker.Registered = time.Now()
	payload.Worker.LastSeen = time.Now()
	payload.Worker.Status = WorkerStatusOnline
	payload.Worker.Encoder = nil // Don't store encoder
	s.workers[payload.Worker.ID] = &payload.Worker
	s.mu.Unlock()

	log.Printf("Worker registered: %s (%s) with capabilities: %v", 
		payload.Worker.ID, payload.Worker.Address, payload.Worker.Capabilities)
	
	sendResponse(map[string]string{"status": "ok", "worker_id": payload.Worker.ID})
}

func (s *Server) handleWorkerHeartbeatSync(msg *Message) {
	var payload WorkerHeartbeatPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return
	}

	s.mu.Lock()
	if worker, ok := s.workers[payload.WorkerID]; ok {
		worker.LastSeen = time.Now()
		worker.Status = payload.Status
		worker.CurrentLoad = payload.CurrentLoad
	}
	s.mu.Unlock()
}

func (s *Server) handleTaskResultSync(msg *Message) {
	var payload TaskResultPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		log.Printf("Invalid task result payload: %v", err)
		return
	}

	log.Printf("Received task result: task=%s, worker=%s, error=%q", payload.TaskID, payload.WorkerID, payload.Error)

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[payload.TaskID]
	if !ok {
		log.Printf("Received result for unknown task: %s", payload.TaskID)
		return
	}

	now := time.Now()
	if payload.Error != "" {
		task.Status = TaskStatusFailed
		task.Error = payload.Error
		task.RetryCount++
		log.Printf("Task %s failed: %s (retry %d/%d)", 
			payload.TaskID, payload.Error, task.RetryCount, s.config.MaxRetries)
		
		if task.RetryCount < s.config.MaxRetries {
			task.Status = TaskStatusPending
			task.AssignedTo = ""
			go func(t *Task) {
				s.taskQueue <- t
				log.Printf("Task %s requeued for retry", t.ID)
			}(task)
		}
	} else {
		task.Status = TaskStatusCompleted
		task.CompletedAt = &now
		task.Result = payload.Result
		log.Printf("Task %s marked as completed", payload.TaskID)
	}

	s.updateJobProgress(task.JobID)

	// Update worker load
	if worker, ok := s.workers[payload.WorkerID]; ok {
		worker.CurrentLoad--
		if worker.CurrentLoad < 0 {
			worker.CurrentLoad = 0
		}
		if worker.CurrentLoad < worker.MaxTasks {
			worker.Status = WorkerStatusOnline
		}
		log.Printf("Worker %s load updated: %d/%d", payload.WorkerID, worker.CurrentLoad, worker.MaxTasks)
	}
}

func (s *Server) handleGetWorkersSync(sendResponse ResponseFunc) {
	s.mu.RLock()
	workers := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	s.mu.RUnlock()
	sendResponse(workers)
}

func (s *Server) handleSubmitJobSync(msg *Message, sendResponse ResponseFunc) {
	var payload SubmitJobPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		sendResponse(ErrorPayload{Code: "INVALID_PAYLOAD", Message: err.Error()})
		return
	}

	s.mu.Lock()
	s.jobCounter++
	payload.Job.ID = fmt.Sprintf("job_%d_%d", time.Now().Unix(), s.jobCounter)
	payload.Job.Status = "pending"
	payload.Job.CreatedAt = time.Now()
	s.jobs[payload.Job.ID] = &payload.Job
	s.mu.Unlock()

	log.Printf("Job submitted: %s (type: %s, input: %s)", 
		payload.Job.ID, payload.Job.Type, payload.Job.InputFile)

	go s.processJob(&payload.Job)
	
	sendResponse(map[string]string{"status": "ok", "job_id": payload.Job.ID})
}

func (s *Server) handleJobStatusSync(msg *Message, sendResponse ResponseFunc) {
	var payload JobStatusPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		sendResponse(ErrorPayload{Code: "INVALID_PAYLOAD", Message: err.Error()})
		return
	}

	s.mu.RLock()
	job, ok := s.jobs[payload.JobID]
	s.mu.RUnlock()

	if !ok {
		sendResponse(ErrorPayload{Code: "JOB_NOT_FOUND", Message: "Job not found"})
		return
	}
	sendResponse(job)
}

func (s *Server) handleJobListSync(sendResponse ResponseFunc) {
	s.mu.RLock()
	jobPtrs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobPtrs = append(jobPtrs, j)
	}
	s.mu.RUnlock()
	
	jobs := make([]Job, len(jobPtrs))
	for i, j := range jobPtrs {
		jobs[i] = *j
	}
	sendResponse(JobListPayload{Jobs: jobs})
}

func (s *Server) handleCancelJobSync(msg *Message, sendResponse ResponseFunc) {
	var payload CancelJobPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		sendResponse(ErrorPayload{Code: "INVALID_PAYLOAD", Message: err.Error()})
		return
	}

	s.mu.Lock()
	job, ok := s.jobs[payload.JobID]
	if ok {
		job.Status = "cancelled"
		log.Printf("Job %s cancelled", payload.JobID)
	}
	s.mu.Unlock()
	sendResponse(map[string]string{"status": "ok"})
}

func (s *Server) handleSplitFileSync(msg *Message, sendResponse ResponseFunc) {
	var payload SplitFilePayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		sendResponse(ErrorPayload{Code: "INVALID_PAYLOAD", Message: err.Error()})
		return
	}

	splitFiles, err := s.SplitInputFile(payload.InputFile, payload.SplitSize, payload.OutputDir)
	result := FileSplitResultPayload{
		JobID:      payload.JobID,
		SplitFiles: splitFiles,
		Error:      "",
	}
	if err != nil {
		result.Error = err.Error()
	}
	sendResponse(result)
}

type tcpEncoder struct {
	encoder *json.Encoder
	mu      sync.Mutex
}

func (e *tcpEncoder) Encode(v interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.encoder.Encode(v)
}

type wsEncoder struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (e *wsEncoder) Encode(v interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn.WriteJSON(v)
}

func (s *Server) handleMessage(msg *Message, encoder Encoder) {
	switch msg.Type {
	case MsgTypeRegisterWorker:
		s.handleRegisterWorker(msg, encoder)
	case MsgTypeWorkerHeartbeat:
		s.handleWorkerHeartbeat(msg, encoder)
	case MsgTypeTaskResult:
		s.handleTaskResult(msg)
	case MsgTypeGetWorkers:
		s.handleGetWorkers(encoder)
	case MsgTypeSubmitJob:
		s.handleSubmitJob(msg, encoder)
	case MsgTypeJobStatus:
		s.handleJobStatus(msg, encoder)
	case MsgTypeJobList:
		s.handleJobList(encoder)
	case MsgTypeCancelJob:
		s.handleCancelJob(msg, encoder)
	case MsgTypeSplitFile:
		s.handleSplitFile(msg, encoder)
	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

func (s *Server) handleRegisterWorker(msg *Message, encoder Encoder) {
	var payload RegisterWorkerPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		s.sendError(encoder, "INVALID_PAYLOAD", err.Error())
		return
	}

	s.mu.Lock()
	payload.Worker.Registered = time.Now()
	payload.Worker.LastSeen = time.Now()
	payload.Worker.Status = WorkerStatusOnline
	payload.Worker.Encoder = encoder
	s.workers[payload.Worker.ID] = &payload.Worker
	s.mu.Unlock()

	log.Printf("Worker registered: %s (%s) with capabilities: %v", 
		payload.Worker.ID, payload.Worker.Address, payload.Worker.Capabilities)
	
	response := map[string]string{"status": "ok", "worker_id": payload.Worker.ID}
	if err := encoder.Encode(response); err != nil {
		log.Printf("Failed to send registration response: %v", err)
	}
}

func (s *Server) handleWorkerHeartbeat(msg *Message, encoder Encoder) {
	var payload WorkerHeartbeatPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return
	}

	s.mu.Lock()
	if worker, ok := s.workers[payload.WorkerID]; ok {
		worker.LastSeen = time.Now()
		worker.Status = payload.Status
		worker.CurrentLoad = payload.CurrentLoad
		worker.Encoder = encoder
	}
	s.mu.Unlock()
}

func (s *Server) handleTaskResult(msg *Message) {
	var payload TaskResultPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		log.Printf("Invalid task result payload: %v", err)
		return
	}

	log.Printf("Received task result: task=%s, worker=%s, error=%q", payload.TaskID, payload.WorkerID, payload.Error)

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[payload.TaskID]
	if !ok {
		log.Printf("Received result for unknown task: %s", payload.TaskID)
		return
	}

	now := time.Now()
	if payload.Error != "" {
		task.Status = TaskStatusFailed
		task.Error = payload.Error
		task.RetryCount++
		log.Printf("Task %s failed: %s (retry %d/%d)", 
			payload.TaskID, payload.Error, task.RetryCount, s.config.MaxRetries)
		
		if task.RetryCount < s.config.MaxRetries {
			task.Status = TaskStatusPending
			task.AssignedTo = ""
			select {
			case s.taskQueue <- task:
				log.Printf("Task %s requeued for retry", payload.TaskID)
			default:
				log.Printf("Task queue full, cannot requeue task %s", payload.TaskID)
			}
		}
	} else {
		task.Status = TaskStatusCompleted
		task.CompletedAt = &now
		task.Result = payload.Result
		log.Printf("Task %s marked as completed", payload.TaskID)
	}

	s.updateJobProgress(task.JobID)

	// Update worker load
	if worker, ok := s.workers[payload.WorkerID]; ok {
		worker.CurrentLoad--
		if worker.CurrentLoad < 0 {
			worker.CurrentLoad = 0
		}
		if worker.CurrentLoad < worker.MaxTasks {
			worker.Status = WorkerStatusOnline
		}
		log.Printf("Worker %s load updated: %d/%d", payload.WorkerID, worker.CurrentLoad, worker.MaxTasks)
	}
}

func (s *Server) updateJobProgress(jobID string) {
	job, ok := s.jobs[jobID]
	if !ok {
		return
	}

	taskIDs, ok := s.jobTasks[jobID]
	if !ok {
		return
	}

	completed := 0
	failed := 0
	for _, taskID := range taskIDs {
		task, ok := s.tasks[taskID]
		if !ok {
			continue
		}
		if task.Status == TaskStatusCompleted {
			completed++
		}
		if task.Status == TaskStatusFailed && task.RetryCount >= s.config.MaxRetries {
			failed++
		}
	}

	job.Completed = completed
	job.Failed = failed

	if completed+failed >= job.TotalTasks {
		job.Status = "completed"
		now := time.Now()
		job.CompletedAt = &now
		log.Printf("Job %s completed: %d succeeded, %d failed", jobID, completed, failed)
	}
}

func (s *Server) handleGetWorkers(encoder Encoder) {
	s.mu.RLock()
	workers := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	s.mu.RUnlock()
	encoder.Encode(workers)
}

func (s *Server) handleSubmitJob(msg *Message, encoder Encoder) {
	var payload SubmitJobPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		s.sendError(encoder, "INVALID_PAYLOAD", err.Error())
		return
	}

	s.mu.Lock()
	s.jobCounter++
	payload.Job.ID = fmt.Sprintf("job_%d_%d", time.Now().Unix(), s.jobCounter)
	payload.Job.Status = "pending"
	payload.Job.CreatedAt = time.Now()
	s.jobs[payload.Job.ID] = &payload.Job
	s.mu.Unlock()

	log.Printf("Job submitted: %s (type: %s, input: %s)", 
		payload.Job.ID, payload.Job.Type, payload.Job.InputFile)

	go s.processJob(&payload.Job)
	
	response := map[string]string{"status": "ok", "job_id": payload.Job.ID}
	if err := encoder.Encode(response); err != nil {
		log.Printf("Failed to send job submission response: %v", err)
	}
}

func (s *Server) processJob(job *Job) {
	s.mu.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	s.mu.Unlock()

	log.Printf("Processing job %s, splitting file %s", job.ID, job.InputFile)

	splitFiles, err := s.SplitInputFile(job.InputFile, job.SplitSize, job.OutputDir)
	if err != nil {
		log.Printf("Job %s split error: %v", job.ID, err)
		s.mu.Lock()
		job.Status = "failed"
		s.mu.Unlock()
		return
	}

	log.Printf("Job %s split into %d files", job.ID, len(splitFiles))

	s.mu.Lock()
	job.TotalTasks = len(splitFiles)
	taskIDs := make([]string, 0, len(splitFiles))
	
	for i, sf := range splitFiles {
		s.taskCounter++
		taskID := fmt.Sprintf("task_%s_%d", job.ID, i)
		task := &Task{
			ID:         taskID,
			JobID:      job.ID,
			Type:       job.Type,
			Payload:    map[string]interface{}{
				"input_file":  sf,
				"output_dir":  job.OutputDir,
				"task_index":  i,
				"total_tasks": len(splitFiles),
			},
			Status:     TaskStatusPending,
			CreatedAt:  time.Now(),
			Priority:   0,
			RetryCount: 0,
		}
		s.tasks[taskID] = task
		taskIDs = append(taskIDs, taskID)
		
		select {
		case s.taskQueue <- task:
		default:
			log.Printf("Warning: task queue full, task %s may be delayed", taskID)
			go func(t *Task) {
				s.taskQueue <- t
			}(task)
		}
	}
	s.jobTasks[job.ID] = taskIDs
	s.mu.Unlock()

	log.Printf("Job %s: created %d tasks", job.ID, len(splitFiles))
}

func (s *Server) SplitInputFile(inputFile string, splitSize int64, outputDir string) ([]string, error) {
	f, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer f.Close()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	var splitFiles []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, FileReadBufferSize), 10*FileReadBufferSize)
	
	partNum := 0
	var currentPart *os.File
	var currentSize int64

	for scanner.Scan() {
		line := scanner.Text()
		lineSize := int64(len(line)) + 1 // +1 for newline

		if currentPart == nil || currentSize+lineSize > splitSize {
			if currentPart != nil {
				currentPart.Close()
			}
			partNum++
			splitFile := filepath.Join(outputDir, fmt.Sprintf("part_%04d.txt", partNum))
			splitFiles = append(splitFiles, splitFile)
			currentPart, err = os.Create(splitFile)
			if err != nil {
				return nil, fmt.Errorf("failed to create part file: %w", err)
			}
			currentSize = 0
		}

		if _, err := fmt.Fprintln(currentPart, line); err != nil {
			currentPart.Close()
			return nil, fmt.Errorf("failed to write to part file: %w", err)
		}
		currentSize += lineSize
	}

	if currentPart != nil {
		currentPart.Close()
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading input file: %w", err)
	}

	return splitFiles, nil
}

func (s *Server) handleJobStatus(msg *Message, encoder Encoder) {
	var payload JobStatusPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		s.sendError(encoder, "INVALID_PAYLOAD", err.Error())
		return
	}

	s.mu.RLock()
	job, ok := s.jobs[payload.JobID]
	s.mu.RUnlock()

	if !ok {
		s.sendError(encoder, "JOB_NOT_FOUND", "Job not found")
		return
	}
	encoder.Encode(job)
}

func (s *Server) handleJobList(encoder Encoder) {
	s.mu.RLock()
	jobPtrs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobPtrs = append(jobPtrs, j)
	}
	s.mu.RUnlock()
	
	jobs := make([]Job, len(jobPtrs))
	for i, j := range jobPtrs {
		jobs[i] = *j
	}
	encoder.Encode(JobListPayload{Jobs: jobs})
}

func (s *Server) handleCancelJob(msg *Message, encoder Encoder) {
	var payload CancelJobPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		s.sendError(encoder, "INVALID_PAYLOAD", err.Error())
		return
	}

	s.mu.Lock()
	job, ok := s.jobs[payload.JobID]
	if ok {
		job.Status = "cancelled"
		log.Printf("Job %s cancelled", payload.JobID)
	}
	s.mu.Unlock()
	encoder.Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleSplitFile(msg *Message, encoder Encoder) {
	var payload SplitFilePayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		s.sendError(encoder, "INVALID_PAYLOAD", err.Error())
		return
	}

	splitFiles, err := s.SplitInputFile(payload.InputFile, payload.SplitSize, payload.OutputDir)
	result := FileSplitResultPayload{
		JobID:      payload.JobID,
		SplitFiles: splitFiles,
		Error:      "",
	}
	if err != nil {
		result.Error = err.Error()
	}
	encoder.Encode(result)
}

func (s *Server) handleWorkersAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	workers := make([]*Worker, 0, len(s.workers))
	for _, worker := range s.workers {
		workers = append(workers, worker)
	}
	s.mu.RUnlock()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workers)
}

func (s *Server) handleJobsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Submit a new job
		var payload SubmitJobPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		s.jobCounter++
		payload.Job.ID = fmt.Sprintf("job_%d_%d", time.Now().Unix(), s.jobCounter)
		payload.Job.Status = "pending"
		payload.Job.CreatedAt = time.Now()
		s.jobs[payload.Job.ID] = &payload.Job
		s.mu.Unlock()

		log.Printf("Job submitted: %s (type: %s, input: %s)", 
			payload.Job.ID, payload.Job.Type, payload.Job.InputFile)

		go s.processJob(&payload.Job)
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "job_id": payload.Job.ID})
		return
	}

	// GET - List all jobs
	s.mu.RLock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.RUnlock()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

func (s *Server) handleTasksAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (s *Server) handleStatsAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	stats := map[string]interface{}{
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
	
	for _, w := range s.workers {
		if w.Status == WorkerStatusOnline {
			stats["workers_online"] = stats["workers_online"].(int) + 1
		}
	}
	
	for _, j := range s.jobs {
		if j.Status == "running" {
			stats["jobs_running"] = stats["jobs_running"].(int) + 1
		}
	}
	
	for _, t := range s.tasks {
		switch t.Status {
		case TaskStatusPending:
			stats["tasks_pending"] = stats["tasks_pending"].(int) + 1
		case TaskStatusAssigned, TaskStatusRunning:
			stats["tasks_running"] = stats["tasks_running"].(int) + 1
		case TaskStatusCompleted:
			stats["tasks_completed"] = stats["tasks_completed"].(int) + 1
		case TaskStatusFailed:
			stats["tasks_failed"] = stats["tasks_failed"].(int) + 1
		}
	}
	s.mu.RUnlock()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleSplitAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req SplitFilePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	splitFiles, err := s.SplitInputFile(req.InputFile, req.SplitSize, req.OutputDir)
	result := FileSplitResultPayload{
		JobID:      req.JobID,
		SplitFiles: splitFiles,
	}
	if err != nil {
		result.Error = err.Error()
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

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

	// Find available worker
	var availableWorker *Worker
	for _, w := range s.workers {
		if w.Status == WorkerStatusOnline && w.CurrentLoad < w.MaxTasks {
			availableWorker = w
			break
		}
	}

	if availableWorker == nil {
		// No worker available, requeue
		go func() {
			time.Sleep(100 * time.Millisecond)
			s.taskQueue <- task
		}()
		return
	}

	if task.Status != TaskStatusPending {
		return
	}

	task.Status = TaskStatusAssigned
	task.AssignedTo = availableWorker.ID
	now := time.Now()
	task.StartedAt = &now
	availableWorker.CurrentLoad++
	if availableWorker.CurrentLoad >= availableWorker.MaxTasks {
		availableWorker.Status = WorkerStatusBusy
	}

	s.sendTaskToWorker(availableWorker, task)
}

func (s *Server) sendTaskToWorker(worker *Worker, task *Task) {
	msg, err := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: *task})
	if err != nil {
		log.Printf("Failed to create task message: %v", err)
		task.Status = TaskStatusPending
		task.AssignedTo = ""
		worker.CurrentLoad--
		s.taskQueue <- task
		return
	}

	if worker.Encoder != nil {
		if err := worker.Encoder.Encode(msg); err != nil {
			log.Printf("Failed to send task %s to worker %s: %v", task.ID, worker.ID, err)
			task.Status = TaskStatusPending
			task.AssignedTo = ""
			worker.CurrentLoad--
			worker.Status = WorkerStatusOffline
			worker.Encoder = nil
			s.taskQueue <- task
			return
		}
		log.Printf("Assigned task %s to worker %s", task.ID, worker.ID)
	} else {
		log.Printf("No encoder for worker %s, requeueing task %s", worker.ID, task.ID)
		task.Status = TaskStatusPending
		task.AssignedTo = ""
		worker.CurrentLoad--
		s.taskQueue <- task
	}
}

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
			now := time.Now()
			for id, worker := range s.workers {
				if now.Sub(worker.LastSeen) > s.getHeartbeatTTL() {
					if worker.Status != WorkerStatusOffline {
						worker.Status = WorkerStatusOffline
						worker.Encoder = nil
						log.Printf("Worker %s marked offline (last seen: %s)", id, worker.LastSeen.Format("15:04:05"))
						s.requeueWorkerTasks(id)
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) requeueWorkerTasks(workerID string) {
	for _, task := range s.tasks {
		if task.AssignedTo == workerID && (task.Status == TaskStatusAssigned || task.Status == TaskStatusRunning) {
			task.Status = TaskStatusPending
			task.AssignedTo = ""
			task.RetryCount++
			if task.RetryCount < s.config.MaxRetries {
				go func(t *Task) {
					s.taskQueue <- t
				}(task)
				log.Printf("Requeued task %s from offline worker %s", task.ID, workerID)
			} else {
				task.Status = TaskStatusFailed
				task.Error = "max retries exceeded after worker went offline"
				s.updateJobProgress(task.JobID)
				log.Printf("Task %s failed: max retries after worker %s went offline", task.ID, workerID)
			}
		}
	}
}

func (s *Server) sendError(encoder Encoder, code, message string) {
	if err := encoder.Encode(ErrorPayload{Code: code, Message: message}); err != nil {
		log.Printf("Failed to send error response: %v", err)
	}
}
