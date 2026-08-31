package cnc

import (
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
	HeartbeatTTL time.Duration `json:"heartbeat_ttl"`
}

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		HTTPAddr:     ":8080",
		TCPAddr:      ":9090",
		DataDir:      "./cnc_data",
		MaxRetries:   3,
		HeartbeatTTL: 30 * time.Second,
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
		config.HTTPAddr = ":8080"
	}
	if config.TCPAddr == "" {
		config.TCPAddr = ":9090"
	}
	if config.DataDir == "" {
		config.DataDir = "./cnc_data"
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.HeartbeatTTL == 0 {
		config.HeartbeatTTL = 30 * time.Second
	}
	return &config, nil
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
		taskQueue: make(chan *Task, 10000),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		config:  config,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (s *Server) Start() error {
	os.MkdirAll(s.config.DataDir, 0755)

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
	s.cancel()
	if s.httpServer != nil {
		s.httpServer.Shutdown(context.Background())
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

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var msg Message
			if err := decoder.Decode(&msg); err != nil {
				if err != io.EOF {
					log.Printf("TCP decode error: %v", err)
				}
				return
			}
			s.handleMessage(&msg, encoder)
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

type wsEncoder struct {
	conn *websocket.Conn
}

func (e *wsEncoder) Encode(v interface{}) error {
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

	log.Printf("Worker registered: %s (%s)", payload.Worker.ID, payload.Worker.Address)
	encoder.Encode(map[string]string{"status": "ok", "worker_id": payload.Worker.ID})
}

func (s *Server) handleWorkerHeartbeat(msg *Message, encoder Encoder) {
	var payload WorkerHeartbeatPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if worker, ok := s.workers[payload.WorkerID]; ok {
		worker.LastSeen = time.Now()
		worker.Status = payload.Status
		worker.CurrentLoad = payload.CurrentLoad
		worker.Encoder = encoder
	}
}

func (s *Server) handleTaskResult(msg *Message) {
	var payload TaskResultPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		return
	}

	s.mu.Lock()
	task, ok := s.tasks[payload.TaskID]
	if ok {
		now := time.Now()
		if payload.Error != "" {
			task.Status = TaskStatusFailed
			task.Error = payload.Error
			task.RetryCount++
			if task.RetryCount < s.config.MaxRetries {
				task.Status = TaskStatusPending
				task.AssignedTo = ""
				s.taskQueue <- task
			}
		} else {
			task.Status = TaskStatusCompleted
			task.CompletedAt = &now
			task.Result = payload.Result
		}
		s.updateJobProgress(task)
	}
	s.mu.Unlock()

	if worker, ok := s.workers[payload.WorkerID]; ok {
		s.mu.Lock()
		worker.CurrentLoad--
		if worker.CurrentLoad < worker.MaxTasks {
			worker.Status = WorkerStatusOnline
		}
		s.mu.Unlock()
	}
}

func (s *Server) updateJobProgress(task *Task) {
	for _, job := range s.jobs {
		for _, t := range s.tasks {
			if t.AssignedTo == task.AssignedTo && t.Status == TaskStatusCompleted {
				job.Completed++
			}
			if t.Status == TaskStatusFailed && t.RetryCount >= s.config.MaxRetries {
				job.Failed++
			}
		}
		if job.Completed+job.Failed >= job.TotalTasks {
			job.Status = "completed"
			now := time.Now()
			job.CompletedAt = &now
		}
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

	go s.processJob(&payload.Job)
	encoder.Encode(map[string]string{"status": "ok", "job_id": payload.Job.ID})
}

func (s *Server) processJob(job *Job) {
	s.mu.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	s.mu.Unlock()

	splitFiles, err := s.SplitInputFile(job.InputFile, job.SplitSize, job.OutputDir)
	if err != nil {
		log.Printf("Job %s split error: %v", job.ID, err)
		s.mu.Lock()
		job.Status = "failed"
		s.mu.Unlock()
		return
	}

	job.TotalTasks = len(splitFiles)
	s.mu.Lock()
	for i, sf := range splitFiles {
		s.taskCounter++
		task := &Task{
			ID:         fmt.Sprintf("task_%s_%d", job.ID, i),
			Type:       job.Type,
			Payload:    map[string]interface{}{"input_file": sf, "output_dir": job.OutputDir, "task_index": i, "total_tasks": len(splitFiles)},
			Status:     TaskStatusPending,
			CreatedAt:  time.Now(),
			Priority:   0,
			RetryCount: 0,
		}
		s.tasks[task.ID] = task
		s.taskQueue <- task
	}
	s.mu.Unlock()
}

func (s *Server) SplitInputFile(inputFile string, splitSize int64, outputDir string) ([]string, error) {
	f, err := os.Open(inputFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	os.MkdirAll(outputDir, 0755)

	var splitFiles []string
	buf := make([]byte, 64*1024)
	partNum := 0
	var currentPart *os.File
	var currentSize int64

	for {
		n, err := f.Read(buf)
		if n > 0 {
			if currentPart == nil || currentSize >= splitSize {
				if currentPart != nil {
					currentPart.Close()
				}
				partNum++
				splitFile := filepath.Join(outputDir, fmt.Sprintf("part_%04d.txt", partNum))
				splitFiles = append(splitFiles, splitFile)
				currentPart, err = os.Create(splitFile)
				if err != nil {
					return nil, err
				}
				currentSize = 0
			}
			currentPart.Write(buf[:n])
			currentSize += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if currentPart != nil {
		currentPart.Close()
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
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	s.mu.RUnlock()
	json.NewEncoder(w).Encode(workers)
}

func (s *Server) handleJobsAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.RUnlock()
	json.NewEncoder(w).Encode(jobs)
}

func (s *Server) handleTasksAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()
	json.NewEncoder(w).Encode(tasks)
}

func (s *Server) handleStatsAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	stats := map[string]interface{}{
		"workers_total": len(s.workers),
		"workers_online": 0,
		"jobs_total": len(s.jobs),
		"jobs_running": 0,
		"tasks_total": len(s.tasks),
		"tasks_pending": 0,
		"tasks_running": 0,
		"tasks_completed": 0,
		"tasks_failed": 0,
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
	json.NewEncoder(w).Encode(result)
}

func (s *Server) taskDispatcher() {
	defer s.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.dispatchTasks()
		}
	}
}

func (s *Server) dispatchTasks() {
	s.mu.Lock()
	defer s.mu.Unlock()

	var availableWorkers []*Worker
	for _, w := range s.workers {
		if w.Status == WorkerStatusOnline && w.CurrentLoad < w.MaxTasks {
			availableWorkers = append(availableWorkers, w)
		}
	}

	if len(availableWorkers) == 0 {
		return
	}

	for _, worker := range availableWorkers {
		select {
		case task := <-s.taskQueue:
			if task.Status != TaskStatusPending {
				s.taskQueue <- task
				continue
			}
			task.Status = TaskStatusAssigned
			task.AssignedTo = worker.ID
			now := time.Now()
			task.StartedAt = &now
			worker.CurrentLoad++
			if worker.CurrentLoad >= worker.MaxTasks {
				worker.Status = WorkerStatusBusy
			}
			s.sendTaskToWorker(worker, task)
		default:
			return
		}
	}
}

func (s *Server) sendTaskToWorker(worker *Worker, task *Task) {
	msg, err := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: *task})
	if err != nil {
		log.Printf("Failed to create task message: %v", err)
		return
	}
	if worker.Encoder != nil {
		if err := worker.Encoder.Encode(msg); err != nil {
			log.Printf("Failed to send task %s to worker %s: %v", task.ID, worker.ID, err)
			return
		}
		log.Printf("Assigned task %s to worker %s", task.ID, worker.ID)
	} else {
		log.Printf("No encoder for worker %s, task %s queued", worker.ID, task.ID)
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
				if now.Sub(worker.LastSeen) > s.config.HeartbeatTTL {
					worker.Status = WorkerStatusOffline
					worker.Encoder = nil
					log.Printf("Worker %s marked offline", id)
					s.requeueWorkerTasks(id)
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
				s.taskQueue <- task
				log.Printf("Requeued task %s from offline worker %s", task.ID, workerID)
			} else {
				task.Status = TaskStatusFailed
				task.Error = "max retries exceeded after worker went offline"
				s.updateJobProgress(task)
			}
		}
	}
}

func (s *Server) sendError(encoder Encoder, code, message string) {
	encoder.Encode(ErrorPayload{Code: code, Message: message})
}