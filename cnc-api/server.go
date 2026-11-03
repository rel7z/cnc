package cnc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Server is the CNC command-and-control server. It accepts worker connections
// over TCP, exposes an HTTP API for job management, splits input files into
// chunks, and dispatches tasks to available workers.
type Server struct {
	mu              sync.RWMutex
	workers         map[string]*Worker
	jobs            map[string]*Job
	tasks           map[string]*Task
	jobTasks        map[string][]string // jobID -> []taskID
	taskQueue       chan *Task
	taskBuffers     map[string]string // taskID -> buffer for partial lines
	httpServer      *http.Server
	tcpListener     net.Listener
	config          *ServerConfig
	jobCounter      int
	enumRecoveryErr error
	taskCounter     int
	wg              sync.WaitGroup
	ctx             context.Context
	cancel          context.CancelFunc
	reverseIPLookup reverseIPLookup

	// Google Drive uploader
	gdriveUploader *GDriveUploader
	gdriveMu       sync.RWMutex

	// Telegram Bot
	telegramBot *TelegramBot

	// SSE broker — guards sseClients only; never held at the same time as s.mu.
	sseMu      sync.Mutex
	sseClients map[chan SSEEvent]struct{}

	// Active worker TCP connections
	conns        map[net.Conn]struct{}
	isRestarting bool
}

type ServerConfig struct {
	configPath       string
	LocalAdminOnly   bool            `json:"local_admin_only,omitempty"`
	HTTPAddr         string          `json:"http_addr"`
	TCPAddr          string          `json:"tcp_addr"`
	DataDir          string          `json:"data_dir"`
	MaxRetries       int             `json:"max_retries"`
	HeartbeatTTL     string          `json:"heartbeat_ttl"`
	WPPluginsDir     string          `json:"wp_plugins_dir,omitempty"`
	JoomlaPluginsDir string          `json:"joomla_plugins_dir,omitempty"`
	GDrive           *GDriveWrapper  `json:"gdrive,omitempty"`
	Telegram         *TelegramConfig `json:"telegram,omitempty"`
}

type GDriveWrapper struct {
	RedirectURL       string `json:"redirect_url"`
	Enabled           bool   `json:"enabled"`
	WatchDir          string `json:"watch_dir"`
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	TokenPath         string `json:"token_path"`
	UploadInterval    string `json:"upload_interval"`
	ParentFolderID    string `json:"parent_folder_id"`
	StateFilePath     string `json:"state_file_path"`
	DeleteAfterUpload bool   `json:"delete_after_upload"`
}

// ExpandPath expands leading ~ or ~/ to the user's home directory.
func ExpandPath(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		HTTPAddr:         DefaultHTTPPort,
		TCPAddr:          DefaultTCPPort,
		DataDir:          "./cnc_data",
		MaxRetries:       DefaultMaxRetries,
		HeartbeatTTL:     "30s",
		WPPluginsDir:     "~/plugins/wp",
		JoomlaPluginsDir: "~/plugins/joomla",
		GDrive: &GDriveWrapper{
			Enabled:        false,
			WatchDir:       "~/merged",
			UploadInterval: "1s",
		},
		Telegram: &TelegramConfig{
			Enabled:            true,
			BotToken:           "8763217188:AAHQCttBHBskdaCqLiUkqqYAWnSg4c3SiRw",
			WebAppURL:          "http://localhost:3000",
			NotifyJobStart:     true,
			NotifyJobComplete:  true,
			NotifyJobFail:      true,
			NotifyWorkerEvents: false,
		},
	}
}

func SaveServerConfig(path string, config *ServerConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func LoadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		cfg := DefaultServerConfig()
		cfg.configPath = path
		_ = SaveServerConfig(path, cfg)
		return cfg, err
	}
	var config ServerConfig
	config.configPath = path
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
	if config.WPPluginsDir == "" {
		config.WPPluginsDir = "~/plugins/wp"
	}
	if config.JoomlaPluginsDir == "" {
		config.JoomlaPluginsDir = "~/plugins/joomla"
	}
	if config.GDrive == nil {
		config.GDrive = &GDriveWrapper{
			Enabled:        false,
			WatchDir:       "~/merged",
			UploadInterval: "1s",
		}
	} else if config.GDrive.WatchDir == "" {
		config.GDrive.WatchDir = "~/merged"
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
	server := &Server{
		workers:    make(map[string]*Worker),
		jobs:       make(map[string]*Job),
		tasks:      make(map[string]*Task),
		jobTasks:   make(map[string][]string),
		taskQueue:  make(chan *Task, DefaultTaskQueueSize),
		config:     config,
		ctx:        ctx,
		cancel:     cancel,
		sseClients: make(map[chan SSEEvent]struct{}),
		conns:      make(map[net.Conn]struct{}),
	}
	server.enumRecoveryErr = server.recoverEnumJobs()
	return server
}

// Start runs all server components. It blocks on the HTTP listener.
func (s *Server) Start() error {
	if s.enumRecoveryErr != nil {
		return fmt.Errorf("recover enum jobs: %w", s.enumRecoveryErr)
	}
	if err := os.MkdirAll(s.config.DataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// Ensure WP and Joomla plugins directories exist
	if s.config.WPPluginsDir != "" {
		if err := os.MkdirAll(ExpandPath(s.config.WPPluginsDir), 0755); err != nil {
			log.Printf("[server] Warning: could not create WP plugins dir: %v", err)
		}
	}
	if s.config.JoomlaPluginsDir != "" {
		if err := os.MkdirAll(ExpandPath(s.config.JoomlaPluginsDir), 0755); err != nil {
			log.Printf("[server] Warning: could not create Joomla plugins dir: %v", err)
		}
	}

	// Initialize authorization even when automatic uploads are disabled.
	if cfg := s.driveConfig(); cfg != nil && cfg.ClientID != "" && cfg.ClientSecret != "" {
		if uploader, err := buildDriveUploader(cfg); err != nil {
			log.Printf("[gdrive] Warning: failed to initialize: %v", err)
		} else {
			if cfg.Enabled {
				err = uploader.Start()
			}
			if err != nil {
				uploader.Stop()
				log.Printf("[gdrive] Warning: failed to start: %v", err)
			} else {
				s.gdriveUploader = uploader
			}
		}
	}

	// Start Telegram bot if enabled
	if s.config.Telegram != nil && s.config.Telegram.Enabled {
		bot, err := NewTelegramBot(s.config.Telegram, s)
		if err != nil {
			log.Printf("[telegram] Warning: failed to initialize: %v", err)
		} else {
			s.telegramBot = bot
			if err := s.telegramBot.Start(); err != nil {
				log.Printf("[telegram] Warning: failed to start: %v", err)
			}
		}
	}

	s.wg.Add(1)
	go s.taskDispatcher()

	s.wg.Add(1)
	go s.heartbeatChecker()

	s.wg.Add(1)
	go s.tcpServer()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/processor/", s.handleProcessorAPI)
	linode := newLinodeIntegration(s.config.DataDir)
	linode.tcpAddr, linode.httpAddr = s.config.TCPAddr, s.config.HTTPAddr
	linode.prepareTools = refreshWorkerToolsPackage
	mux.HandleFunc("/api/linode/", func(w http.ResponseWriter, r *http.Request) {
		remote, _, _ := net.SplitHostPort(r.RemoteAddr)
		local, _, _ := net.SplitHostPort(s.config.HTTPAddr)
		if !linodeLocalCaller(remote, local) {
			http.Error(w, "cloud management requires the local dashboard proxy", http.StatusForbidden)
			return
		}
		linode.handle(w, r)
	})
	awsCloud := newAWSIntegration("/opt/cnc/aws-config.json")
	awsCloud.prepareTools = refreshWorkerToolsPackage
	awsCloud.dataDir = s.config.DataDir
	mux.HandleFunc("/api/aws/", func(w http.ResponseWriter, r *http.Request) {
		remote, _, _ := net.SplitHostPort(r.RemoteAddr)
		local, _, _ := net.SplitHostPort(s.config.HTTPAddr)
		if !linodeLocalCaller(remote, local) {
			http.Error(w, "cloud management requires the local dashboard proxy", http.StatusForbidden)
			return
		}
		awsCloud.handle(w, r)
	})
	mux.HandleFunc("/download/cnc-release", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "/opt/cnc/RELEASE") })
	mux.HandleFunc("/download/cnc-worker-tools", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "/opt/cnc/worker-tools.tar.gz") })
	mux.HandleFunc("/api/workers", s.handleWorkersAPI)
	mux.HandleFunc("/api/workers/deploy", s.handleDeployWorkerAPI)
	mux.HandleFunc("/api/workers/login", s.handleWorkerLoginAPI)
	mux.HandleFunc("/download/cnc-worker-linux", s.handleDownloadWorker)
	mux.HandleFunc("/api/jobs", s.handleJobsAPI)
	mux.HandleFunc("/api/jobs/", s.handleJobsSubAPI)
	mux.HandleFunc("/api/tasks", s.handleTasksAPI)
	mux.HandleFunc("/api/stats", s.handleStatsAPI)
	mux.HandleFunc("/api/config", s.handleServerConfigAPI)
	mux.HandleFunc("/api/config/gdrive", s.handleGDriveConfigAPI)
	mux.HandleFunc("/api/config/gdrive/test", s.handleTestGDriveAPI)
	mux.HandleFunc("/api/config/gdrive/auth-url", s.handleGDriveAuthURL)
	mux.HandleFunc("/api/config/gdrive/auth-status", s.handleGDriveAuthStatus)
	mux.HandleFunc("/api/config/gdrive/sync", s.handleGDriveSync)
	mux.HandleFunc("/api/config/gdrive/restore", s.handleGDriveRestore)
	mux.HandleFunc("/api/config/gdrive/auth-disconnect", s.handleGDriveDisconnect)
	mux.HandleFunc("/api/config/gdrive/callback", s.handleGDriveCallback)
	mux.HandleFunc("/api/config/telegram", s.handleTelegramConfigAPI)
	mux.HandleFunc("/api/config/telegram/test", s.handleTestTelegramAPI)
	mux.HandleFunc("/api/server/restart", s.handleServerRestartAPI)
	mux.HandleFunc("/api/server/update", s.handleServerUpdateAPI)
	mux.HandleFunc("/api/status", s.handleStatusAPI)
	mux.HandleFunc("/api/events", s.handleEventsAPI)
	mux.HandleFunc("/api/files/", s.handleFilesAPI)
	mux.HandleFunc("/api/tools", s.handleToolsAPI)
	mux.HandleFunc("/api/tools/launch", s.handleToolsLaunchAPI)
	mux.HandleFunc("/api/enum/", s.handleEnumAPI)
	mux.HandleFunc("/api/tools/wordlists", s.handleToolsWordlistAPI)

	// File Manager & Server Terminal APIs
	mux.HandleFunc("/api/fs/list", s.handleFSListAPI)
	mux.HandleFunc("/api/fs/upload", s.handleFSUploadAPI)
	mux.HandleFunc("/api/fs/read", s.handleFSReadAPI)
	mux.HandleFunc("/api/fs/write", s.handleFSWriteAPI)
	mux.HandleFunc("/api/fs/delete", s.handleFSDeleteAPI)
	mux.HandleFunc("/api/server/exec", s.handleServerExecAPI)

	s.httpServer = &http.Server{
		Addr:    s.config.HTTPAddr,
		Handler: corsMiddleware(s.localAdminMiddleware(mux)),
	}

	log.Printf("CNC Server starting — HTTP %s  TCP %s", s.config.HTTPAddr, s.config.TCPAddr)
	err := s.httpServer.ListenAndServe()
	if s.IsRestarting() {
		log.Println("[restart] Server stopped for restart. Re-executing...")
		if rErr := restartProcess(); rErr != nil {
			log.Printf("[restart] Process re-exec failed (%v), falling back to in-process restart", rErr)
		}
		return ErrRestartRequested
	}
	return err
}

func (s *Server) IsRestarting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRestarting
}

func (s *Server) TriggerRestart() {
	s.mu.Lock()
	if s.isRestarting {
		s.mu.Unlock()
		return
	}
	s.isRestarting = true
	s.mu.Unlock()

	log.Println("[restart] Restart requested, stopping server...")
	s.Stop()
}

func (s *Server) Stop() {
	log.Println("Stopping CNC Server...")
	s.cancel()

	// Stop Google Drive uploader if running
	s.gdriveMu.Lock()
	if s.gdriveUploader != nil {
		s.gdriveUploader.Stop()
	}
	s.gdriveMu.Unlock()

	// Stop Telegram bot if running
	if s.telegramBot != nil {
		s.telegramBot.Stop()
	}

	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx) //nolint:errcheck
	}
	if s.tcpListener != nil {
		s.tcpListener.Close()
	}

	// Close all active worker TCP connections so s.wg.Wait doesn't hang
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	log.Println("CNC Server stopped")
}

// ── CORS middleware ───────────────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
	// Send to Telegram if bot is active
	if s.telegramBot != nil {
		switch event.Type {
		case SSEEventJob:
			if j, ok := event.Payload.(Job); ok {
				s.telegramBot.HandleJobEvent(&j)
			} else if jp, ok := event.Payload.(*Job); ok && jp != nil {
				s.telegramBot.HandleJobEvent(jp)
			}
		case SSEEventWorker:
			if w, ok := event.Payload.(Worker); ok {
				s.telegramBot.HandleWorkerEvent(&w)
			} else if wp, ok := event.Payload.(*Worker); ok && wp != nil {
				s.telegramBot.HandleWorkerEvent(wp)
			}
		}
	}

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
	tasks := s.dashboardTasksLocked(dashboardTaskLimit)
	stats := s.computeStats()
	s.mu.RUnlock()

	return SSEEvent{
		Type: SSEEventSnapshot,
		Payload: SSESnapshot{
			Stats:   stats,
			Workers: workers,
			Jobs:    jobs,
			Tasks:   tasks,
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
		case TaskStatusCompleted, TaskStatusCompletedWithErrors:
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
func (s *Server) handleTCPConn(conn net.Conn) {
	s.mu.Lock()
	if s.conns == nil {
		s.conns = make(map[net.Conn]struct{})
	}
	s.conns[conn] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()

	defer s.wg.Done()
	defer conn.Close()
	log.Printf("New TCP connection from %s", conn.RemoteAddr())

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	sendCh := make(chan *Message, 256)

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

	var registeredWorkerID string
	for {
		select {
		case <-writeCtx.Done():
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
			registeredWorkerID = s.handleRegisterWorker(&msg, sendCh, conn.RemoteAddr().String())

		case MsgTypeWorkerHeartbeat:
			s.handleWorkerHeartbeat(&msg)

		case MsgTypeTaskResult:
			s.handleTaskResult(&msg)

		case MsgTypeTaskStream:
			s.handleTaskStream(&msg)

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

func (s *Server) handleRegisterWorker(msg *Message, sendCh chan *Message, remoteAddr string) string {
	var p RegisterWorkerPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		log.Printf("Invalid register payload: %v", err)
		return ""
	}

	s.mu.Lock()
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	p.Worker.Address = host

	// Safeguard: Ensure worker ID is unique per host so workers never overwrite each other
	cleanHost := strings.ReplaceAll(host, ".", "_")
	cleanHost = strings.ReplaceAll(cleanHost, ":", "_")
	if p.Worker.ID == "" {
		p.Worker.ID = fmt.Sprintf("worker_%s", cleanHost)
	} else if existing, exists := s.workers[p.Worker.ID]; exists && existing.Address != host && existing.Status == WorkerStatusOnline {
		p.Worker.ID = fmt.Sprintf("%s_%s", p.Worker.ID, cleanHost)
	}

	p.Worker.Registered = time.Now()
	p.Worker.LastSeen = time.Now()
	p.Worker.Status = WorkerStatusOnline
	p.Worker.SendCh = sendCh
	s.workers[p.Worker.ID] = &p.Worker
	workerCopy := p.Worker // copy for broadcast (SendCh is json:"-")
	s.mu.Unlock()

	log.Printf("Worker registered: %s (max_tasks=%d)", p.Worker.ID, p.Worker.MaxTasks)

	// Acknowledge registration — include http_addr so the worker knows the HTTP server address.
	ack, _ := NewMessage("ack", map[string]string{
		"status":    "ok",
		"worker_id": p.Worker.ID,
		"http_addr": s.config.HTTPAddr,
	})
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
		w.Resources = p.Resources
		w.EnumRequests = p.EnumRequests
		w.ProcessorProtocol = p.ProcessorProtocol
		w.ProcessorVersions = p.ProcessorVersions
		w.ProcessorAttempts = p.ProcessorAttempts
		w.ProcessorWorkspaces = p.ProcessorWorkspaces
		active := map[string]bool{}
		for _, id := range p.ActiveTasks {
			active[id] = true
		}
		for _, task := range s.tasks {
			if task.AssignedTo == w.ID && (task.Type == "processor" || task.Type == "processor_install") && (task.Status == TaskStatusAssigned || task.Status == TaskStatusRunning) && task.StartedAt != nil && time.Since(*task.StartedAt) > 10*time.Second && !active[task.ID] {
				task.Status = TaskStatusFailed
				task.Error = "worker no longer reports this transport task"
			}
		}
		if reserved := s.reservedLoad(w.ID); reserved > w.CurrentLoad {
			w.CurrentLoad = reserved
		}
		if w.CurrentLoad >= w.MaxTasks {
			w.Status = WorkerStatusBusy
		}
		cp := *w
		workerCopy = &cp
	}
	s.mu.Unlock()

	if workerCopy != nil {
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: *workerCopy})
	}
}

func (s *Server) handleTaskStream(msg *Message) {
	var p TaskStreamPayload
	if err := msg.UnmarshalPayload(&p); err != nil {
		log.Printf("Invalid task stream payload: %v", err)
		return
	}

	s.mu.RLock()
	task, ok := s.tasks[p.TaskID]
	if !ok {
		s.mu.RUnlock()
		return
	}
	jobCopy := s.jobs[task.JobID]
	s.mu.RUnlock()

	driveConfig := s.driveConfig()
	if jobCopy == nil || driveConfig == nil || driveConfig.WatchDir == "" {
		return
	}

	if p.Chunk != "" {
		watchDir := ExpandPath(driveConfig.WatchDir)
		bufferKey := p.TaskID
		chunk := p.Chunk
		if p.IsStderr {
			bufferKey += "\x00stderr"
			// Progress redraws use carriage returns, not newlines. Do not
			// retain an entire long scan's progress as one partial line.
			chunk = strings.ReplaceAll(chunk, "\r", "\n")
		}

		s.mu.Lock()
		if s.taskBuffers == nil {
			s.taskBuffers = make(map[string]string)
		}
		s.taskBuffers[bufferKey] += chunk
		data := s.taskBuffers[bufferKey]

		var remaining string
		lines := strings.Split(data, "\n")
		if !strings.HasSuffix(data, "\n") {
			remaining = lines[len(lines)-1]
			lines = lines[:len(lines)-1]
		}

		if remaining == "" {
			delete(s.taskBuffers, bufferKey)
		} else {
			s.taskBuffers[bufferKey] = remaining
		}
		s.mu.Unlock()

		fileChunks := make(map[string]*strings.Builder)

		for _, line := range lines {
			if line == "" {
				continue
			}
			// Enum emits explicit file records. Keep progress/log messages out
			// of results, while accepting records from older >&2 launchers.
			if (p.IsStderr || jobCopy.Name == "Tool: enum") && !strings.HasPrefix(line, "[FILE:") {
				continue
			}
			targetFile := jobCopy.OutputFile
			content := line + "\n"

			if strings.HasPrefix(line, "[FILE:") {
				if endIdx := strings.Index(line, "]"); endIdx > 6 {
					targetFile = line[6:endIdx]
					content = line[endIdx+1:] + "\n"
					content = strings.TrimPrefix(content, " ")
				}
			}

			if targetFile == "" {
				continue
			}
			if jobCopy.Enum != nil && (filepath.Base(targetFile) != targetFile || strings.HasSuffix(targetFile, ".jsonl")) {
				continue
			}

			log.Printf("[DEBUG] Routing line to %s: %s", targetFile, content)
			b := fileChunks[targetFile]
			if b == nil {
				b = &strings.Builder{}
				fileChunks[targetFile] = b
			}
			b.WriteString(content)
		}

		for filename, b := range fileChunks {
			if b.Len() == 0 {
				continue
			}
			watcherFileMu.Lock()
			outPath := filepath.Join(watchDir, filename)
			f, err := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Printf("Failed to open watcher output file %s: %v", outPath, err)
			} else {
				if _, err := f.WriteString(b.String()); err != nil {
					log.Printf("Failed to write stream to watcher output file %s: %v", outPath, err)
				}
				f.Close()
			}
			watcherFileMu.Unlock()
			if uploader := s.driveUploader(); uploader != nil {
				uploader.NotifyResults()
			}
		}
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

	if task.Status == TaskStatusCancelled || task.Status == TaskStatusCompleted || task.Status == TaskStatusCompletedWithErrors || task.Status == TaskStatusFailed || task.AssignedTo != p.WorkerID {
		log.Printf("Ignoring result for cancelled task %s", p.TaskID)
		s.mu.Unlock()
		return
	}

	now := time.Now()

	// Infrastructure error (worker couldn't run the command at all) → retry.
	if p.Error != "" && (task.Type == "processor" || task.Type == "processor_install" || task.Type == "enum") {
		task.Status = TaskStatusFailed
		task.CompletedAt = &now
		task.Error = p.Error
	} else if p.Error != "" {
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
	} else if (task.Type == "enum" || s.jobs[task.JobID] != nil && s.jobs[task.JobID].Name == "Tool: cms-scan") && p.Result != nil && p.Result.ExitCode == 2 {
		task.Status = TaskStatusCompletedWithErrors
		task.CompletedAt = &now
		task.Result = p.Result
	} else if p.Result != nil && p.Result.ExitCode != 0 {
		// Command ran but exited non-zero — mark failed without retry.
		task.Status = TaskStatusFailed
		task.CompletedAt = &now
		task.Result = p.Result
		task.Error = taskFailureSummary(p.Result)
		log.Printf("Task %s failed with exit code %d", task.ID, p.Result.ExitCode)
	} else {
		task.Status = TaskStatusCompleted
		task.CompletedAt = &now
		task.Result = p.Result
	}
	if task.Type == "enum" && p.Result != nil && task.EnumPublication != "accepted" && task.EnumPublication != "conflicted" {
		task.EnumPublication = p.Result.PublicationStatus
	}

	s.updateJobProgress(task.JobID)
	if err := s.persistEnumJobLocked(task.JobID); err != nil {
		log.Printf("Cannot persist enum result state: %v", err)
	}

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

	// (Output is now routed to the watcher directory in real-time via handleTaskStream)

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
	// New jobs have no overall deadline unless a timeout is explicitly chosen.
	if job.TimeoutSeconds == 0 {
		job.TimeoutSeconds = NoTimeout
	}

	s.mu.Lock()
	s.jobCounter++
	if job.ID == "" {
		job.ID = fmt.Sprintf("job_%d_%d", time.Now().Unix(), s.jobCounter)
	}
	job.Status = "pending"
	job.CreatedAt = time.Now()
	s.jobs[job.ID] = job
	if err := s.persistEnumJobLocked(job.ID); err != nil {
		job.Status = "failed"
		s.mu.Unlock()
		log.Printf("Cannot persist enum job: %v", err)
		return
	}
	jobCopy := *job
	s.mu.Unlock()

	log.Printf("Job submitted: %s  mode=%s  command=%q  input=%s", job.ID, job.Mode, job.Command, job.InputFile)

	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})
	s.broadcastStats()

	go s.processJob(job)
}

// processJob routes to the correct processing strategy based on mode.
func (s *Server) processJob(job *Job) {
	switch job.Mode {
	case JobModeSpread:
		s.processSpreadJob(job)
	case JobModeBroadcast:
		s.processBroadcastJob(job)
	case JobModeServer:
		s.processServerJob(job)
	}
}

// processServerJob executes a command directly on the server host machine.
func (s *Server) processServerJob(job *Job) {
	s.mu.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	job.TotalTasks = 1
	job.Workers = 1

	taskID := fmt.Sprintf("task_%s_0", job.ID)
	task := &Task{
		ID:    taskID,
		JobID: job.ID,
		Type:  "shell",
		Payload: map[string]interface{}{
			"command":         job.Command,
			"timeout_seconds": float64(job.TimeoutSeconds),
			"output_file":     job.OutputFile,
		},
		Status:     TaskStatusRunning,
		AssignedTo: "server",
		CreatedAt:  now,
		StartedAt:  &now,
	}
	s.tasks[taskID] = task
	s.jobTasks[job.ID] = []string{taskID}
	jobCopy := *job
	taskCopy := *task
	s.mu.Unlock()

	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})
	s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
	s.broadcastStats()

	go func() {
		var ctx context.Context
		var cancel context.CancelFunc
		if job.TimeoutSeconds == NoTimeout {
			ctx, cancel = context.WithCancel(s.ctx)
		} else {
			timeout := time.Duration(job.TimeoutSeconds) * time.Second
			if timeout <= 0 {
				timeout = time.Duration(DefaultTimeout) * time.Second
			}
			ctx, cancel = context.WithTimeout(s.ctx, timeout)
		}
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", job.Command)

		// Ensure PATH includes ./tools directory and current directory
		currPath := os.Getenv("PATH")
		exeDir, _ := os.Getwd()
		toolsDir := filepath.Join(exeDir, "tools")
		newPath := fmt.Sprintf("%s:%s:%s", toolsDir, exeDir, currPath)
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

		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		err := cmd.Run()
		completedAt := time.Now()
		exitCode := 0
		errMsg := ""
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
			errMsg = err.Error()
		}

		duration := completedAt.Sub(now).Seconds()
		res := &TaskResult{
			ExitCode: exitCode,
			Stdout:   stdoutBuf.String(),
			Stderr:   stderrBuf.String(),
		}

		s.mu.Lock()
		task.CompletedAt = &completedAt
		task.Result = res
		if exitCode == 0 {
			task.Status = TaskStatusCompleted
		} else {
			task.Status = TaskStatusFailed
			task.Error = errMsg
		}
		s.updateJobProgress(job.ID)
		finalJobCopy := *job
		finalTaskCopy := *task
		s.mu.Unlock()

		// Route output to watcher directory if this job specifies an OutputFile.
		if driveConfig := s.driveConfig(); finalJobCopy.OutputFile != "" && driveConfig != nil && driveConfig.WatchDir != "" {
			watchDir := ExpandPath(driveConfig.WatchDir)
			outPath := filepath.Join(watchDir, finalJobCopy.OutputFile)
			watcherFileMu.Lock()

			// 1. If output file was generated in local working directory, copy it to watchDir
			if localBytes, readErr := os.ReadFile(finalJobCopy.OutputFile); readErr == nil && len(localBytes) > 0 {
				_ = os.MkdirAll(watchDir, 0755)
				captureErr := error(nil)
				if _, statErr := os.Stat(outPath); statErr == nil {
					captureErr = processorCaptureBeforeDelete(outPath)
				}
				if captureErr != nil {
					log.Printf("[processor] Retained existing %s before replacement: %v; new output remains at %s", outPath, captureErr, finalJobCopy.OutputFile)
				} else {
					_ = os.WriteFile(outPath, localBytes, 0644)
				}
			} else if res.Stdout != "" {
				// 2. Otherwise write captured stdout
				_ = os.MkdirAll(watchDir, 0755)
				f, openErr := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if openErr == nil {
					_, _ = f.WriteString(res.Stdout)
					f.Close()
				}
			}
			watcherFileMu.Unlock()
		}

		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: finalTaskCopy})
		s.broadcast(SSEEvent{Type: SSEEventJob, Payload: finalJobCopy})
		s.broadcastStats()
		log.Printf("Server job %s finished with exit=%d in %.2fs", job.ID, exitCode, duration)
	}()
}

// processSpreadJob handles spread mode: split file into N parts, one task per worker.
func (s *Server) processSpreadJob(job *Job) {
	s.mu.Lock()
	if job.Status == "cancelled" {
		s.mu.Unlock()
		return
	}
	// Determine worker count under the same lock as the job state.
	if job.Workers <= 0 {
		for _, w := range s.workers {
			if w.Status == WorkerStatusOnline || w.Status == WorkerStatusBusy {
				job.Workers++
			}
		}
		if job.Workers <= 0 {
			job.Workers = 1
		}
	}

	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	jobCopy := *job
	s.mu.Unlock()

	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	chunkDir := filepath.Join(home, "split")
	if job.Enum != nil {
		chunkDir = filepath.Join(s.config.DataDir, "enum-jobs", job.ID, "chunks")
	}
	originalName := filepath.Base(job.InputFile)
	inputFile := job.InputFile
	err = nil
	if isReverseIPTool(strings.TrimPrefix(job.Name, "Tool: ")) {
		// Resolve and deduplicate once on the controller, before splitting. A
		// unique chunk basename also prevents simultaneous jobs overwriting one
		// another in the existing worker download endpoint.
		originalName = job.ID + "-ips.txt"
		inputFile, err = s.prepareReverseIPJob(job)
	}

	var splitFiles []string
	if err != nil {
		// Handled by the same failure path as a split error, with no dispatch.
	} else if job.Enum != nil && job.Workers > 1 {
		splitFiles, err = s.splitInputFileByCount(job.InputFile, job.Workers, chunkDir, originalName)
	} else if job.Enum != nil {
		splitFiles, err = splitEnumInput(job.InputFile, chunkDir, job.Workers)
	} else {
		splitFiles, err = s.splitInputFileByCount(inputFile, job.Workers, chunkDir, originalName)
	}
	if err != nil {
		log.Printf("Job %s split error: %v", job.ID, err)
		s.mu.Lock()
		if job.Status == "cancelled" {
			s.mu.Unlock()
			return
		}
		job.Status = "failed"
		job.Error = err.Error()
		completed := time.Now()
		job.CompletedAt = &completed
		if job.Enum != nil {
			s.persistEnumJobLocked(job.ID)
		}
		failedCopy := *job
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventJob, Payload: failedCopy})
		s.broadcastStats()
		return
	}
	log.Printf("Job %s: split %q into %d parts in %s", job.ID, originalName, len(splitFiles), chunkDir)

	// Create all tasks and set TotalTasks under one lock to avoid the total_tasks=0 window.
	s.mu.Lock()
	if job.Status == "cancelled" {
		s.mu.Unlock()
		return
	}
	job.TotalTasks = len(splitFiles)
	taskIDs := make([]string, 0, len(splitFiles))
	createdTasks := make([]Task, 0, len(splitFiles))

	for i, chunkPath := range splitFiles {
		taskID := fmt.Sprintf("task_%s_%d", job.ID, i)

		task := &Task{
			ID:    taskID,
			JobID: job.ID,
			Type:  "shell",
			Payload: map[string]interface{}{
				"command":         job.Command,
				"download_url":    fmt.Sprintf("/api/files/%s?part=%d", originalName, i),
				"dest_name":       originalName,
				"chunk_path":      chunkPath,
				"timeout_seconds": float64(job.TimeoutSeconds),
				"output_file":     job.OutputFile,
			},
			Status:    TaskStatusPending,
			CreatedAt: time.Now(),
		}
		if job.Name == "Tool: cms-scan" {
			task.Payload["file_records"] = true
			task.Payload["cms_scanner"] = true
		}
		if job.Enum != nil {
			task.Type = "enum"
			task.Payload = map[string]interface{}{"enum_artifact_url": "/api/enum/jobs/" + job.ID + "/artifact", "enum_artifact_sha256": job.Enum.ArtifactSHA256, "enum_token": job.Enum.Token, "enum_input_url": "/api/enum/jobs/" + job.ID + "/chunks/" + taskID, "chunk_path": chunkPath, "auto_concurrency": job.Enum.Automatic, "threads": float64(job.Enum.Threads), "request_timeout_ms": float64(job.Enum.RequestTimeoutMS), "timeout_seconds": float64(job.TimeoutSeconds)}
			for _, name := range job.Enum.Wordlists {
				task.Payload["enum_"+strings.TrimSuffix(name, ".txt")+"_url"] = "/api/enum/jobs/" + job.ID + "/" + name
			}
		}
		s.tasks[taskID] = task
		taskIDs = append(taskIDs, taskID)
		createdTasks = append(createdTasks, *task)
	}
	s.jobTasks[job.ID] = taskIDs
	if err := s.persistEnumJobLocked(job.ID); err != nil {
		job.Status = "failed"
		for _, id := range taskIDs {
			s.tasks[id].Status = TaskStatusFailed
			s.tasks[id].Error = "cannot persist enum task"
		}
		s.mu.Unlock()
		log.Printf("Cannot persist enum shards: %v", err)
		return
	}
	jobCopy = *job
	s.mu.Unlock()

	// Broadcast the updated job (with total_tasks) and all newly created tasks.
	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})
	for _, t := range createdTasks {
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: t})
	}
	s.broadcastStats()

	// Enqueue tasks after releasing the lock.
	for _, taskID := range taskIDs {
		s.mu.RLock()
		task := s.tasks[taskID]
		s.mu.RUnlock()
		select {
		case s.taskQueue <- task:
		default:
			go func(t *Task) { s.taskQueue <- t }(task)
		}
	}
}

// processBroadcastJob sends the same command to every online worker (no file splitting).
func (s *Server) processBroadcastJob(job *Job) {
	// Snapshot online workers before acquiring the main lock.
	s.mu.RLock()
	var onlineWorkerIDs []string
	for id, w := range s.workers {
		if w.Status == WorkerStatusOnline || w.Status == WorkerStatusBusy {
			onlineWorkerIDs = append(onlineWorkerIDs, id)
		}
	}
	s.mu.RUnlock()

	if len(onlineWorkerIDs) == 0 {
		s.mu.Lock()
		job.Status = "failed"
		job.TotalTasks = 0
		failedCopy := *job
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventJob, Payload: failedCopy})
		s.broadcastStats()
		log.Printf("Job %s failed: no online workers for broadcast", job.ID)
		return
	}

	// Set running + create all tasks under one lock.
	s.mu.Lock()
	job.Status = "running"
	now := time.Now()
	job.StartedAt = &now
	job.TotalTasks = len(onlineWorkerIDs)
	job.Workers = len(onlineWorkerIDs)

	taskIDs := make([]string, 0, len(onlineWorkerIDs))
	createdTasks := make([]Task, 0, len(onlineWorkerIDs))
	for i, workerID := range onlineWorkerIDs {
		taskID := fmt.Sprintf("task_%s_%d", job.ID, i)
		task := &Task{
			ID:    taskID,
			JobID: job.ID,
			Type:  "shell",
			Payload: map[string]interface{}{
				"command":         job.Command,
				"timeout_seconds": float64(job.TimeoutSeconds),
				"output_file":     job.OutputFile,
			},
			Status:     TaskStatusPending,
			AssignedTo: workerID,
			CreatedAt:  time.Now(),
		}
		if job.Name == "Tool: cms-scan" {
			task.Payload["file_records"] = true
			task.Payload["cms_scanner"] = true
		}
		s.tasks[taskID] = task
		taskIDs = append(taskIDs, taskID)
		createdTasks = append(createdTasks, *task)
	}
	s.jobTasks[job.ID] = taskIDs
	jobCopy := *job
	s.mu.Unlock()

	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})
	for _, t := range createdTasks {
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: t})
	}
	s.broadcastStats()

	// Enqueue all tasks.
	for _, taskID := range taskIDs {
		s.mu.RLock()
		task := s.tasks[taskID]
		s.mu.RUnlock()
		select {
		case s.taskQueue <- task:
		default:
			go func(t *Task) { s.taskQueue <- t }(task)
		}
	}
}

func (s *Server) updateJobProgress(jobID string) {
	job, ok := s.jobs[jobID]
	if !ok {
		return
	}
	taskIDs := s.jobTasks[jobID]

	completed, failed, partial := 0, 0, 0
	for _, tid := range taskIDs {
		t, ok := s.tasks[tid]
		if !ok {
			continue
		}
		switch t.Status {
		case TaskStatusCompleted:
			completed++
		case TaskStatusCompletedWithErrors:
			completed++
			partial++
		case TaskStatusFailed:
			failed++
		}
	}
	job.Completed = completed
	job.Failed = failed

	if completed+failed >= job.TotalTasks && job.TotalTasks > 0 {
		if failed == job.TotalTasks {
			job.Status = "failed"
		} else if failed > 0 || partial > 0 {
			job.Status = "completed_with_errors"
		} else {
			job.Status = "completed"
		}
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

	if task.Status != TaskStatusPending {
		s.mu.Unlock()
		return
	}
	if reason := s.dorkCapacityReason(task); reason != "" {
		task.QueueReason = reason
		s.mu.Unlock()
		s.retryCapacityTask(task)
		return
	}

	var chosen *Worker

	if task.AssignedTo != "" {
		// Broadcast task: pre-assigned to a specific worker.
		w, ok := s.workers[task.AssignedTo]
		if !ok || w.Status == WorkerStatusOffline || w.SendCh == nil || s.workerLoad(w) >= w.MaxTasks {
			// Worker unavailable — requeue after a short delay.
			s.mu.Unlock()
			go func() {
				time.Sleep(200 * time.Millisecond)
				s.taskQueue <- task
			}()
			return
		}
		chosen = w
		if _, reason := s.ordinaryResources(w); reason != "" {
			task.QueueReason = reason
			s.mu.Unlock()
			go func() { time.Sleep(200 * time.Millisecond); s.taskQueue <- task }()
			return
		}
	} else {
		// Spread task: find least-loaded available worker.
		for _, w := range s.workers {
			if w.Status != WorkerStatusOnline || s.workerLoad(w) >= w.MaxTasks || w.SendCh == nil {
				continue
			}
			if _, reason := s.ordinaryResources(w); reason != "" {
				task.QueueReason = reason
				continue
			}
			if chosen == nil || s.workerLoad(w)*chosen.MaxTasks < s.workerLoad(chosen)*w.MaxTasks ||
				(s.workerLoad(w)*chosen.MaxTasks == s.workerLoad(chosen)*w.MaxTasks && w.ID < chosen.ID) {
				chosen = w
			}
		}

		if chosen == nil {
			s.mu.Unlock()
			go func() {
				time.Sleep(200 * time.Millisecond)
				s.taskQueue <- task
			}()
			return
		}
	}

	reservedLoad := s.workerLoad(chosen)
	resources, _ := s.ordinaryResources(chosen)
	task.ExecutionResources = &resources
	task.QueueReason = ""
	task.Status = TaskStatusAssigned
	task.AssignedTo = chosen.ID
	now := time.Now()
	task.StartedAt = &now
	chosen.CurrentLoad = reservedLoad + 1
	if chosen.CurrentLoad >= chosen.MaxTasks {
		chosen.Status = WorkerStatusBusy
	}
	if err := s.persistEnumJobLocked(task.JobID); err != nil {
		task.Status = TaskStatusFailed
		task.Error = "cannot persist enum dispatch"
		chosen.CurrentLoad--
		s.updateJobProgress(task.JobID)
		s.mu.Unlock()
		return
	}
	taskCopy := *task
	workerCopy := *chosen

	msg, err := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: *task})
	if err != nil {
		log.Printf("Failed to build assign-task message: %v", err)
		s.requeueTask(task, chosen)
		taskCopy = *task
		workerCopy = *chosen
		s.mu.Unlock()

		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
		s.broadcastStats()
		go func() { s.taskQueue <- task }()
		return
	}

	select {
	case chosen.SendCh <- msg:
		log.Printf("Dispatched task %s → worker %s", task.ID, chosen.ID)
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
		s.broadcastStats()
	default:
		log.Printf("Worker %s send buffer full, requeueing task %s", chosen.ID, task.ID)
		s.requeueTask(task, chosen)
		taskCopy = *task
		workerCopy = *chosen
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
		s.broadcastStats()
		go func() { s.taskQueue <- task }()
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
		if w.CurrentLoad < w.MaxTasks {
			w.Status = WorkerStatusOnline
		}
	}
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
		if task.Type == "processor" || task.Type == "processor_install" {
			task.Status = TaskStatusFailed
			task.Error = "worker disconnected; processor lease owns recovery"
			continue
		}
		if task.Type == "enum" {
			task.Status = TaskStatusFailed
			task.Error = "worker disconnected; shard interrupted and published output retained"
			now := time.Now()
			task.CompletedAt = &now
			s.updateJobProgress(task.JobID)
			s.persistEnumJobLocked(task.JobID)
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

func (s *Server) splitInputFileByCount(inputFile string, n int, outputDir string, baseName string) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("split count must be > 0")
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Pass 1: count total lines.
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

	if int64(n) > totalLines {
		n = int(totalLines)
	}

	linesPerPart := totalLines / int64(n)
	remainder := int(totalLines % int64(n))

	// Pass 2: write parts.
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

		partPath := filepath.Join(outputDir, fmt.Sprintf("%s.%d", baseName, part))
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

func (s *Server) handleDownloadWorker(w http.ResponseWriter, r *http.Request) {
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "cnc-worker-linux"),
		filepath.Join(cwd, "cnc-api", "cnc-worker-linux"),
		filepath.Join(cwd, "..", "cnc-worker-linux"),
		filepath.Join(cwd, "..", "cnc-api", "cnc-worker-linux"),
		"/root/cnc/cnc-worker-linux",
		"/root/cnc/cnc-api/cnc-worker-linux",
		"/root/cnc-worker-node/cnc-worker-linux",
	}

	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		candidates = append([]string{
			filepath.Join(dir, "cnc-worker-linux"),
			filepath.Join(dir, "cnc-api", "cnc-worker-linux"),
			filepath.Join(dir, "..", "cnc-worker-linux"),
			filepath.Join(dir, "..", "cnc-api", "cnc-worker-linux"),
		}, candidates...)
	}

	var workerPath string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Size() > 0 {
			workerPath = c
			break
		}
	}

	if workerPath == "" {
		http.Error(w, "worker binary not found on server", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=cnc-worker-linux")
	http.ServeFile(w, r, workerPath)
}

type DeployWorkerRequest struct {
	IPs        []string `json:"ips"`
	Username   string   `json:"username"`
	Password   string   `json:"password"`
	ServerAddr string   `json:"server_addr"`
}

func (s *Server) handleDeployWorkerAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DeployWorkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	serverAddr, serverHTTPAddr := s.resolveServerAddresses(req.ServerAddr, r.Host, r)
	log.Printf("[Deploy] Target server address: %s (HTTP: %s)", serverAddr, serverHTTPAddr)

	logChan := make(chan string, 200)
	var wg sync.WaitGroup

	for _, ip := range req.IPs {
		ip := strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		wg.Add(1)
		go func(targetIP string) {
			defer wg.Done()
			s.deployWorkerViaSSH(targetIP, req.Username, req.Password, serverAddr, serverHTTPAddr, logChan)
		}(ip)
	}

	go func() {
		wg.Wait()
		close(logChan)
	}()

	for msg := range logChan {
		fmt.Fprintf(w, "%s\n", msg)
		flusher.Flush()
	}
}

func (s *Server) resolveServerAddresses(reqAddr, reqHost string, r *http.Request) (string, string) {
	tcpPort := "9090"
	if strings.Contains(s.config.TCPAddr, ":") {
		_, p, _ := net.SplitHostPort(s.config.TCPAddr)
		if p != "" {
			tcpPort = p
		}
	}
	httpPort := "8080"
	if strings.Contains(s.config.HTTPAddr, ":") {
		_, p, _ := net.SplitHostPort(s.config.HTTPAddr)
		if p != "" {
			httpPort = p
		}
	}

	host := ""
	if reqAddr != "" {
		host = reqAddr
		port := tcpPort
		if strings.Contains(reqAddr, ":") {
			h, p, err := net.SplitHostPort(reqAddr)
			if err == nil {
				host = h
				port = p
			}
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "" {
			return net.JoinHostPort(host, port), fmt.Sprintf("http://%s:%s", host, httpPort)
		}
	}

	// Try X-Forwarded-Host from reverse proxies
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		h := strings.Split(xfh, ",")[0]
		if strings.Contains(h, ":") {
			h, _, _ = net.SplitHostPort(h)
		}
		if h != "localhost" && h != "127.0.0.1" && h != "" {
			host = h
		}
	}

	if host == "" {
		h := reqHost
		if strings.Contains(h, ":") {
			h, _, _ = net.SplitHostPort(h)
		}
		if h != "localhost" && h != "127.0.0.1" && h != "" {
			host = h
		}
	}

	// If still local/blank, detect Linode server external public IP
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		pubIP := getPublicIP()
		if pubIP != "" {
			host = pubIP
		} else {
			host = "127.0.0.1"
		}
	}

	return net.JoinHostPort(host, tcpPort), fmt.Sprintf("http://%s:%s", host, httpPort)
}

func getPublicIP() string {
	client := http.Client{Timeout: 3 * time.Second}
	endpoints := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}
	for _, ep := range endpoints {
		resp, err := client.Get(ep)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			ip := strings.TrimSpace(string(body))
			if ip != "" && !strings.Contains(ip, "<") && len(ip) <= 45 {
				return ip
			}
		}
	}
	return ""
}

func (s *Server) deployWorkerViaSSH(ip, username, password, serverAddr, serverHTTPAddr string, logChan chan<- string) {
	sendLog := func(format string, args ...interface{}) {
		msg := fmt.Sprintf("[%s] %s", ip, fmt.Sprintf(format, args...))
		log.Print(msg)
		select {
		case logChan <- msg:
		default:
		}
	}

	sendLog("Starting deployment to %s (Target CNC Server: %s)...", ip, serverAddr)

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         12 * time.Second,
	}

	target := ip
	if !strings.Contains(target, ":") {
		target = target + ":22"
	}

	client, err := ssh.Dial("tcp", target, config)
	if err != nil {
		sendLog("Failed to dial SSH: %v", err)
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		sendLog("Failed to create SSH session: %v", err)
		return
	}
	defer session.Close()

	cleanIP := strings.ReplaceAll(ip, ".", "_")
	cleanIP = strings.ReplaceAll(cleanIP, ":", "_")
	workerID := fmt.Sprintf("worker_%s", cleanIP)

	// Robust bash script with dependency auto-install, mirror fallback, unique worker ID, and health check
	script := fmt.Sprintf(`
set -e

# 1. Ensure basic tools
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
	echo "[1/4] Installing system dependencies (curl, wget, git)..."
	if command -v apt-get >/dev/null 2>&1; then
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq && apt-get install -y -qq curl wget git
	elif command -v yum >/dev/null 2>&1; then
		yum install -y -q curl wget git
	fi
fi

mkdir -p /root/cnc-worker-node
cd /root/cnc-worker-node

echo "[1/4] Downloading CNC Worker Binary..."
rm -f cnc-worker-linux
DL_OK=0
if command -v curl >/dev/null 2>&1; then
	curl -fsSL -o cnc-worker-linux "%s/download/cnc-worker-linux" 2>/dev/null && DL_OK=1 || true
elif command -v wget >/dev/null 2>&1; then
	wget -q -O cnc-worker-linux "%s/download/cnc-worker-linux" 2>/dev/null && DL_OK=1 || true
fi

if [ $DL_OK -eq 0 ] || [ ! -s cnc-worker-linux ]; then
	echo "Direct download from server failed, using GitHub mirror..."
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o cnc-worker-linux "https://github.com/rel7z/cnc-deploy/raw/refs/heads/main/cnc-worker-linux" || true
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O cnc-worker-linux "https://github.com/rel7z/cnc-deploy/raw/refs/heads/main/cnc-worker-linux" || true
	fi
fi

if [ ! -s cnc-worker-linux ]; then
	echo "ERROR: Failed to download worker binary from both server and mirror!"
	exit 1
fi
chmod +x cnc-worker-linux

echo "[2/4] Setting up Tools..."
if ! command -v git >/dev/null 2>&1; then
	if command -v apt-get >/dev/null 2>&1; then
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq && apt-get install -y -qq git || true
	elif command -v yum >/dev/null 2>&1; then
		yum install -y -q git || true
	fi
fi

if [ -d "tools" ] && [ -d "tools/.git" ]; then
	cd tools
	git fetch origin main && git reset --hard origin/main || echo "Warning: Failed to update tools repo"
	cd ..
else
	rm -rf tools
	git clone --depth 1 https://github.com/rel7z/worker-tools.git tools || echo "Warning: Failed to clone worker-tools"
fi
chmod +x tools/* 2>/dev/null || true

echo "[3/4] Creating Configuration..."
TARGET_SERVER="%s"
if [ -z "$TARGET_SERVER" ] || [ "$TARGET_SERVER" = "localhost:9090" ] || [ "$TARGET_SERVER" = "127.0.0.1:9090" ]; then
	SSH_IP=$(echo $SSH_CLIENT | awk '{print $1}')
	[ -z "$SSH_IP" ] && SSH_IP=$(echo $SSH_CONNECTION | awk '{print $1}')
	if [ -n "$SSH_IP" ] && [ "$SSH_IP" != "127.0.0.1" ]; then
		TARGET_SERVER="${SSH_IP}:9090"
	fi
fi

echo "Connecting worker (%s) to CNC Server at: $TARGET_SERVER"

cat << EOF > worker_config.json
{
  "server_addr": "$TARGET_SERVER",
  "worker_id": "%s",
  "max_tasks": 0,
  "data_dir": "./worker_data",
  "processor_workspace_root": "/root/processor"
}
EOF
cp -f worker_config.json server_config.json

./cnc-worker-linux -drain-support >/dev/null
cp cnc-worker-linux worker-stop-guard.new
chmod 700 worker-stop-guard.new
mv worker-stop-guard.new worker-stop-guard

echo "[4/4] Starting systemd service..."
cat << EOF > /etc/systemd/system/cnc-worker.service
[Unit]
Description=CNC Worker Node
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/cnc-worker-node
ExecStart=/root/cnc-worker-node/cnc-worker-linux -config=/root/cnc-worker-node/worker_config.json
ExecStop=/root/cnc-worker-node/worker-stop-guard -config=/root/cnc-worker-node/worker_config.json -drain-stop \$MAINPID
TimeoutStopSec=infinity
KillMode=mixed
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable cnc-worker
systemctl restart cnc-worker

sleep 2
if systemctl is-active --quiet cnc-worker; then
	echo "✓ Deployment successful: CNC Worker is active on $(hostname -I | awk '{print $1}') (ID: %s, Target: $TARGET_SERVER)"
else
	echo "✗ ERROR: cnc-worker service failed to start! Recent logs:"
	journalctl -u cnc-worker -n 25 --no-pager
	exit 1
fi
`, serverHTTPAddr, serverHTTPAddr, serverAddr, workerID, workerID, workerID)
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		sendLog("Failed to get stdout pipe: %v", err)
		return
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		sendLog("Failed to get stderr pipe: %v", err)
		return
	}

	var outputWg sync.WaitGroup
	outputWg.Add(2)

	go func() {
		defer outputWg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			sendLog("%s", scanner.Text())
		}
	}()

	go func() {
		defer outputWg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			sendLog("ERR: %s", scanner.Text())
		}
	}()

	if err := session.Start(script); err != nil {
		sendLog("Deployment script failed to start: %v", err)
		return
	}

	outputWg.Wait()

	if err := session.Wait(); err != nil {
		sendLog("Deployment script finished with error: %v", err)
		return
	}
	sendLog("Deployment script finished successfully.")
}

func (s *Server) handleJobCancelAPI(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	if job.Status != "running" && job.Status != "pending" {
		s.mu.Unlock()
		http.Error(w, "job is not in a cancellable state", http.StatusBadRequest)
		return
	}

	job.Status = "cancelled"
	now := time.Now()
	job.CompletedAt = &now

	// Collect tasks to cancel
	var tasksToCancel []*Task
	var workersToNotify []string

	for _, task := range s.tasks {
		if task.JobID == jobID {
			if task.Status == TaskStatusPending || task.Status == TaskStatusAssigned || task.Status == TaskStatusRunning {
				task.Status = TaskStatusCancelled
				task.CompletedAt = &now

				if task.AssignedTo != "" {
					tasksToCancel = append(tasksToCancel, task)
					workersToNotify = append(workersToNotify, task.AssignedTo)

					// Free up worker load
					if worker, ok := s.workers[task.AssignedTo]; ok {
						worker.CurrentLoad--
						if worker.CurrentLoad < 0 {
							worker.CurrentLoad = 0
						}
						if worker.CurrentLoad < worker.MaxTasks {
							worker.Status = WorkerStatusOnline
						}
					}
				}
			}
		}
	}
	if err := s.persistEnumJobLocked(jobID); err != nil {
		s.mu.Unlock()
		http.Error(w, "cannot persist cancellation", 500)
		return
	}

	jobCopy := *job
	s.mu.Unlock()

	// Notify workers to kill their running processes
	for i, task := range tasksToCancel {
		workerID := workersToNotify[i]
		msg, _ := NewMessage(MsgTypeCancelTask, CancelTaskPayload{TaskID: task.ID})

		s.mu.RLock()
		worker, ok := s.workers[workerID]
		s.mu.RUnlock()

		if ok && worker.SendCh != nil {
			select {
			case worker.SendCh <- msg:
			default:
			}
		}
	}

	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: jobCopy})
	s.broadcastStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
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
		if job.Enum != nil {
			http.Error(w, "enum jobs must use the authorized tools launcher", 400)
			return
		}
		job.ID = ""
		if job.Command == "" {
			http.Error(w, "command is required", http.StatusBadRequest)
			return
		}
		if job.Mode != JobModeSpread && job.Mode != JobModeBroadcast && job.Mode != JobModeServer {
			http.Error(w, `mode must be "spread", "broadcast", or "server"`, http.StatusBadRequest)
			return
		}
		if job.Mode == JobModeSpread && job.InputFile == "" {
			http.Error(w, "input_file is required for spread mode", http.StatusBadRequest)
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

// handleJobsSubAPI routes /api/jobs/{id} and /api/jobs/{id}/tasks.
func (s *Server) handleJobsSubAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	if strings.HasSuffix(path, "/tasks") {
		jobID := strings.TrimSuffix(path, "/tasks")
		s.handleJobTasksAPI(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, "/cancel") {
		jobID := strings.TrimSuffix(path, "/cancel")
		s.handleJobCancelAPI(w, r, jobID)
		return
	}
	s.handleJobByIDAPI(w, r)
}

func (s *Server) handleJobByIDAPI(w http.ResponseWriter, r *http.Request) {
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
		if err := s.CancelJob(jobID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// CancelJob cancels a running or pending job and all its non-terminal tasks.
func (s *Server) CancelJob(jobID string) error {
	s.mu.Lock()
	job, ok := s.jobs[jobID]
	if ok {
		job.Status = "cancelled"
		for _, tid := range s.jobTasks[jobID] {
			if t, exists := s.tasks[tid]; exists {
				if t.Status == TaskStatusPending || t.Status == TaskStatusAssigned || t.Status == TaskStatusRunning {
					t.Status = TaskStatusFailed
					t.Error = "job cancelled"
				}
			}
		}
	}
	var jobCopy *Job
	if ok {
		cp := *job
		jobCopy = &cp
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job not found")
	}
	s.broadcast(SSEEvent{Type: SSEEventJob, Payload: *jobCopy})
	s.broadcastStats()
	return nil
}

// handleJobTasksAPI serves GET /api/jobs/{id}/tasks
func (s *Server) handleJobTasksAPI(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	taskIDs, ok := s.jobTasks[jobID]
	tasks := make([]*Task, 0, len(taskIDs))
	if ok {
		for _, tid := range taskIDs {
			if t, exists := s.tasks[tid]; exists {
				tasks = append(tasks, t)
			}
		}
	}
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleTasksAPI(w http.ResponseWriter, r *http.Request) {
	limit := 0 // Preserve the full-list API for existing callers.
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			http.Error(w, "limit must be between 1 and 1000", http.StatusBadRequest)
			return
		}
	}
	s.mu.RLock()
	tasks := s.dashboardTasksLocked(limit)
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, tasks)
}

// GetFullStats returns the complete cluster stats including Google Drive metrics.
func (s *Server) GetFullStats() map[string]interface{} {
	s.mu.RLock()
	stats := s.computeStats()
	s.mu.RUnlock()

	result := make(map[string]interface{})
	for k, v := range stats {
		result[k] = v
	}

	if uploader := s.driveUploader(); uploader != nil {
		gdriveStats := uploader.GetStats()
		result["gdrive_enabled"] = gdriveStats.Enabled
		result["gdrive_uploaded"] = gdriveStats.Uploaded
		result["gdrive_pending"] = gdriveStats.Pending
		if !gdriveStats.LastUpload.IsZero() {
			result["gdrive_last_upload"] = gdriveStats.LastUpload.Format(time.RFC3339)
		}
	}
	return result
}

func (s *Server) handleStatsAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.GetFullStats())
}

// handleFilesAPI serves a split chunk file to a worker.
// GET /api/files/<filename>?part=N  (split chunks)
// GET /api/files/wordlist/<name>     (tool wordlist files)
func (s *Server) handleFilesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/files/")

	// ── WordPress Plugins sub-route ──────────────────────────────────────
	if strings.HasPrefix(path, "plugins/wp/") {
		name := strings.TrimPrefix(path, "plugins/wp/")
		if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
			http.Error(w, "invalid plugin filename", http.StatusBadRequest)
			return
		}
		filePath := filepath.Join(ExpandPath(s.config.WPPluginsDir), name)
		http.ServeFile(w, r, filePath)
		return
	}

	// ── Joomla Plugins sub-route ─────────────────────────────────────────
	if strings.HasPrefix(path, "plugins/joomla/") {
		name := strings.TrimPrefix(path, "plugins/joomla/")
		if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
			http.Error(w, "invalid plugin filename", http.StatusBadRequest)
			return
		}
		filePath := filepath.Join(ExpandPath(s.config.JoomlaPluginsDir), name)
		http.ServeFile(w, r, filePath)
		return
	}

	// ── Wordlist sub-route ────────────────────────────────────────────────
	if strings.HasPrefix(path, "wordlist/") {
		name := strings.TrimPrefix(path, "wordlist/")
		if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
			http.Error(w, "invalid wordlist name", http.StatusBadRequest)
			return
		}
		wlPath := filepath.Join(s.config.DataDir, "wordlists", name)
		if _, err := os.Stat(wlPath); os.IsNotExist(err) {
			// Check WP plugins dir or Joomla plugins dir if not found in DataDir/wordlists
			wpCandidate := filepath.Join(ExpandPath(s.config.WPPluginsDir), name)
			if _, err := os.Stat(wpCandidate); err == nil {
				wlPath = wpCandidate
			} else {
				jmCandidate := filepath.Join(ExpandPath(s.config.JoomlaPluginsDir), name)
				if _, err := os.Stat(jmCandidate); err == nil {
					wlPath = jmCandidate
				}
			}
		}
		http.ServeFile(w, r, wlPath)
		return
	}

	// ── Split chunk sub-route ─────────────────────────────────────────────
	filename := path
	partStr := r.URL.Query().Get("part")

	if filename == "" || strings.ContainsAny(filename, "/\\") || strings.Contains(filename, "..") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	if partStr == "" {
		http.Error(w, "missing part parameter", http.StatusBadRequest)
		return
	}

	if len(partStr) > 9 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}
	for _, digit := range partStr {
		if digit < '0' || digit > '9' {
			http.Error(w, "invalid part number", http.StatusBadRequest)
			return
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "cannot resolve home dir", http.StatusInternalServerError)
		return
	}

	chunkPath := filepath.Join(home, "split", fmt.Sprintf("%s.%s", filename, partStr))
	http.ServeFile(w, r, chunkPath)
}

// handleServerConfigAPI handles GET and POST requests for full server configuration.
// GET /api/config - Returns full configuration
// POST /api/config - Updates configuration
func (s *Server) handleServerConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := LoadServerConfig(s.configFile())
		if err != nil {
			cfg = s.config
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPost:
		var input ServerConfig
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
			return
		}
		if err := SaveServerConfig(s.configFile(), &input); err != nil {
			http.Error(w, fmt.Sprintf("failed to save config: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"message": "Server configuration saved successfully. Restart the server for changes to take effect.",
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGDriveConfigAPI handles GET and POST requests for Google Drive configuration.
// GET /api/config/gdrive - Returns current configuration
// POST /api/config/gdrive - Updates configuration
func (s *Server) handleGDriveConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetGDriveConfig(w, r)
	case http.MethodPost:
		s.handleUpdateGDriveConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetGDriveConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.driveConfig()
	if cfg == nil {
		cfg = &GDriveWrapper{WatchDir: "~/merged", UploadInterval: "30s"}
	}
	copy := *cfg
	copy.ClientSecret = ""
	writeJSON(w, http.StatusOK, map[string]interface{}{"gdrive": copy, "client_secret_set": cfg.ClientSecret != ""})
}

func (s *Server) handleUpdateGDriveConfig(w http.ResponseWriter, r *http.Request) {
	var input GDriveWrapper
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.gdriveMu.Lock()
	defer s.gdriveMu.Unlock()
	current := s.driveConfig()
	if input.ClientSecret == "" && current != nil && input.ClientID == current.ClientID {
		input.ClientSecret = current.ClientSecret
	}
	if err := validateDriveConfig(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Keep tokens outside the watched directory, separated by OAuth client.
	if current != nil && current.ClientID != input.ClientID {
		input.TokenPath = ""
	}
	if input.TokenPath == "" && current != nil && current.ClientID == input.ClientID && s.gdriveUploader != nil {
		input.TokenPath = s.gdriveUploader.config.TokenPath
	}
	if input.TokenPath == "" && (current == nil || current.ClientID != input.ClientID || current.TokenPath == "") {
		input.TokenPath = filepath.Join(s.config.DataDir, "gdrive", fmt.Sprintf("%x", sha256.Sum256([]byte(input.ClientID)))+".json")
	} else if input.TokenPath == "" {
		input.TokenPath = current.TokenPath
	}
	if current == nil || current.ParentFolderID != input.ParentFolderID || current.WatchDir != input.WatchDir || current.ClientID != input.ClientID {
		input.StateFilePath = ""
	}
	if input.StateFilePath == "" {
		input.StateFilePath = filepath.Join(s.config.DataDir, "gdrive", fmt.Sprintf("%x", sha256.Sum256([]byte(input.ClientID+"\x00"+input.ParentFolderID+"\x00"+input.WatchDir)))+"-state.json")
	}
	var next *GDriveUploader
	var err error
	if input.ClientID != "" && input.ClientSecret != "" {
		next, err = buildDriveUploader(&input)
		if err != nil {
			http.Error(w, "Could not initialize Google Drive: "+err.Error(), http.StatusBadRequest)
			return
		}
		if input.Enabled {
			if err = os.MkdirAll(next.config.WatchDir, 0755); err != nil {
				next.cancelFunc()
				http.Error(w, "Could not create watch directory", http.StatusBadRequest)
				return
			}
		}
	}
	cfg, err := LoadServerConfig(s.configFile())
	if err != nil {
		if next != nil {
			next.cancelFunc()
		}
		http.Error(w, "Could not load server configuration", http.StatusInternalServerError)
		return
	}
	cfg.GDrive = &input
	if err = SaveServerConfig(s.configFile(), cfg); err != nil {
		if next != nil {
			next.cancelFunc()
		}
		http.Error(w, "Could not save configuration", http.StatusInternalServerError)
		return
	}
	if s.gdriveUploader != nil {
		s.gdriveUploader.Stop()
	}
	s.mu.Lock()
	s.config.GDrive = &input
	s.mu.Unlock()
	s.gdriveUploader = next
	if next != nil {
		// The old watcher may have finished an upload since the new instance loaded state.
		if err = next.loadState(); err != nil {
			log.Printf("[gdrive] Could not reload upload state: %v", err)
		}
		if input.Enabled {
			if err = next.Start(); err != nil {
				next.cancelFunc()
				s.gdriveUploader = nil
				http.Error(w, "Settings saved, but watcher could not start: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "Google Drive settings applied. No restart required."})
}

// handleTestGDriveAPI tests a Google Drive API key by attempting to create a Drive service.
// POST /api/config/gdrive/test
// Now just checks if the uploader is authorized.
func (s *Server) handleTestGDriveAPI(w http.ResponseWriter, r *http.Request) {
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.gdriveUploader == nil || !s.gdriveUploader.IsAuthorized() {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid": false,
			"error": "Not connected. Use 'Connect Google Account' first.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":   true,
		"message": fmt.Sprintf("Connected as %s", s.gdriveUploader.ConnectedEmail()),
	})
}

// handleGDriveAuthURL returns the OAuth2 URL for the user to authorize.
// GET /api/config/gdrive/auth-url
func (s *Server) handleGDriveAuthURL(w http.ResponseWriter, r *http.Request) {
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.gdriveUploader == nil {
		http.Error(w, "Google Drive not configured. Save config first.", http.StatusBadRequest)
		return
	}
	authURL, state, err := s.gdriveUploader.BeginAuthorization()
	if err != nil {
		http.Error(w, "Could not start authorization", http.StatusInternalServerError)
		return
	}
	bridgeURL, err := driveAuthorizationBridgeURL(s.gdriveUploader.oauthCfg.RedirectURL, authURL, state)
	if err != nil {
		http.Error(w, "Could not prepare authorization callback", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "cnc_drive_state", Value: state, Path: "/api/config/gdrive", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.gdriveUploader.oauthCfg.RedirectURL, "https://"), MaxAge: 600})
	writeJSON(w, http.StatusOK, map[string]string{"url": bridgeURL})
}

// handleGDriveAuthStatus returns the current auth status.
// GET /api/config/gdrive/auth-status
func (s *Server) handleGDriveAuthStatus(w http.ResponseWriter, r *http.Request) {
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.gdriveUploader == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"authorized": false,
			"configured": false,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authorized":      s.gdriveUploader.IsAuthorized(),
		"configured":      true,
		"connected_email": s.gdriveUploader.ConnectedEmail(),
	})
}

// handleGDriveSync wakes the existing uploader to rescan its watch directory.
func (s *Server) handleGDriveSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	uploader := s.gdriveUploader
	cfg := s.driveConfig()
	if uploader == nil || cfg == nil || !cfg.Enabled || !uploader.IsAuthorized() {
		http.Error(w, "Google Drive is not enabled and connected", http.StatusConflict)
		return
	}
	if err := uploader.RequestFullSync(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

// handleGDriveDisconnect removes the saved token.
// POST /api/config/gdrive/auth-disconnect
func (s *Server) handleGDriveDisconnect(w http.ResponseWriter, r *http.Request) {
	s.gdriveMu.Lock()
	defer s.gdriveMu.Unlock()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.gdriveUploader == nil {
		http.Error(w, "Google Drive not configured", http.StatusBadRequest)
		return
	}
	if err := s.gdriveUploader.Disconnect(); err != nil {
		http.Error(w, fmt.Sprintf("failed to disconnect: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// handleGDriveCallback handles the OAuth2 redirect from Google.
// GET /api/config/gdrive/callback
func (s *Server) handleGDriveCallback(w http.ResponseWriter, r *http.Request) {
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("cnc_drive_state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 || s.gdriveUploader == nil || !s.gdriveUploader.ConsumeState(state) {
		http.Error(w, "Authorization expired or invalid. Connect again from the dashboard.", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "cnc_drive_state", Value: "", Path: "/api/config/gdrive", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode})
	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error")
		if errMsg == "" {
			errMsg = "no code returned"
		}
		http.Error(w, fmt.Sprintf("OAuth error: %s", errMsg), http.StatusBadRequest)
		return
	}

	if s.gdriveUploader == nil {
		http.Error(w, "Google Drive not configured", http.StatusBadRequest)
		return
	}

	if err := s.gdriveUploader.ExchangeCode(code); err != nil {
		// Show error page
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;padding:40px;background:#111;color:#f87171">
<h2>❌ Authorization Failed</h2><p>%s</p>
<script>setTimeout(()=>window.close(),3000)</script>
</body></html>`, html.EscapeString(err.Error()))
		return
	}

	// Success — show page and close window
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;padding:40px;background:#111;color:#34d399;text-align:center">
<h2>✅ Connected!</h2>
<p style="color:#9ca3af">Authorized as <strong style="color:#fff">%s</strong></p>
<p style="color:#6b7280;font-size:14px">You can close this window.</p>
<script>setTimeout(()=>window.close(),2000)</script>
</body></html>`, html.EscapeString(s.gdriveUploader.ConnectedEmail()))
}

// handleTelegramConfigAPI handles GET and POST requests for Telegram configuration.
// GET /api/config/telegram - Returns current configuration & bot status
// POST /api/config/telegram - Updates configuration & hot-reloads bot
func (s *Server) handleTelegramConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetTelegramConfig(w, r)
	case http.MethodPost:
		s.handleUpdateTelegramConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetTelegramConfig(w http.ResponseWriter, _ *http.Request) {
	cfg, err := LoadServerConfig(s.configFile())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"telegram": cfg.Telegram,
	}

	if s.telegramBot != nil {
		response["status"] = s.telegramBot.GetInfo()
	} else {
		response["status"] = map[string]interface{}{
			"enabled":    false,
			"authorized": false,
		}
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleUpdateTelegramConfig(w http.ResponseWriter, r *http.Request) {
	var input TelegramConfig

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	cfg, err := LoadServerConfig(s.configFile())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	cfg.Telegram = &input

	if err := SaveServerConfig(s.configFile(), cfg); err != nil {
		http.Error(w, fmt.Sprintf("failed to write config: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot-reload or start in-memory bot if present
	if s.telegramBot != nil {
		_ = s.telegramBot.UpdateConfig(&input)
		if input.Enabled && s.telegramBot.botUser == nil {
			_ = s.telegramBot.Start()
		}
	} else if input.Enabled {
		bot, err := NewTelegramBot(&input, s)
		if err == nil {
			s.telegramBot = bot
			_ = s.telegramBot.Start()
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "Telegram configuration updated successfully.",
	})
}

// handleTestTelegramAPI sends a test message to verify Telegram bot connectivity.
// POST /api/config/telegram/test
func (s *Server) handleTestTelegramAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ChatID string `json:"chat_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if s.telegramBot == nil {
		cfg, err := LoadServerConfig(s.configFile())
		if err == nil && cfg.Telegram != nil && cfg.Telegram.BotToken != "" {
			bot, err := NewTelegramBot(cfg.Telegram, s)
			if err == nil {
				s.telegramBot = bot
				_ = s.telegramBot.Start()
			}
		}
	}

	if s.telegramBot == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "Telegram bot is not initialized. Please verify your bot token.",
		})
		return
	}

	targetChat := strings.TrimSpace(req.ChatID)
	if targetChat == "" {
		s.telegramBot.mu.RLock()
		targetChat = s.telegramBot.config.ChatID
		s.telegramBot.mu.RUnlock()
	}

	if targetChat == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "No Chat ID configured. Please send /start to your bot in Telegram first, or enter a Chat ID manually.",
		})
		return
	}

	err := s.telegramBot.SendTestMessage(targetChat)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Test alert sent successfully to Chat ID %s!", targetChat),
	})
}

// handleServerRestartAPI handles server restart requests.
// POST /api/server/restart
func (s *Server) handleServerRestartAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Send success response first
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "restarting",
		"message": "Server is restarting...",
	})

	// Flush response to ensure client receives it
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Schedule shutdown and restart in a goroutine to allow response to complete
	go func() {
		time.Sleep(200 * time.Millisecond) // Give time for response to be sent
		s.TriggerRestart()
	}()
}

// handleStatusAPI returns simple status check for health check and restart polling.
func (s *Server) handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// ── Tools API ────────────────────────────────────────────────────────────────

// GET /api/tools
func (s *Server) handleToolsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Convert map to slice for easier JSON consumption
	toolsList := make([]ToolDefinition, 0, len(ToolsRegistry))
	for _, t := range ToolsRegistry {
		toolsList = append(toolsList, t)
	}

	writeJSON(w, http.StatusOK, toolsList)
}

// POST /api/tools/launch
func (s *Server) handleToolsLaunchAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ToolID         string                 `json:"tool_id"`
		TimeoutSeconds *int                   `json:"timeout_seconds,omitempty"`
		InputFile      string                 `json:"input_file"`
		OutputFile     string                 `json:"output_file"`
		Mode           string                 `json:"mode,omitempty"`
		Options        map[string]interface{} `json:"options"`
		// Optional custom wordlists — plain text, one entry per line.
		WPPlugins  string `json:"wp_plugins,omitempty"`
		JoomlaExts string `json:"joomla_exts,omitempty"`
	}

	body, bodyErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*1024*1024))
	if bodyErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "launch payload exceeds size limit"})
		return
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json payload"})
		return
	}
	if req.ToolID == "enum" {
		s.launchEnum(w, r, body)
		return
	}

	// Persist custom wordlists so workers can download them via /api/files/wordlist/
	wlFlags := ""
	if req.WPPlugins != "" {
		if err := s.saveWordlist("wp_plugins.txt", req.WPPlugins); err != nil {
			log.Printf("[tools] Failed to save wp_plugins wordlist: %v", err)
		} else {
			wlFlags += " -wp-plugins {wp_plugins_path}"
		}
	}
	if req.JoomlaExts != "" {
		if err := s.saveWordlist("joomla_exts.txt", req.JoomlaExts); err != nil {
			log.Printf("[tools] Failed to save joomla_exts wordlist: %v", err)
		} else {
			wlFlags += " -joomla-exts {joomla_exts_path}"
		}
	}

	if req.Options == nil {
		req.Options = make(map[string]interface{})
	}
	if req.OutputFile != "" && req.Options["output_file"] == nil {
		req.Options["output_file"] = req.OutputFile
	}

	mode := JobModeBroadcast
	tool, exists := ToolsRegistry[req.ToolID]
	if exists {
		if tool.Scope == "server" {
			mode = JobModeServer
		} else if tool.DefaultMode != "" {
			mode = tool.DefaultMode
		}
	}
	if req.Mode != "" {
		mode = JobMode(req.Mode)
	}
	if isReverseIPTool(req.ToolID) && mode != JobModeSpread {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reverse IP requires spread mode so input is resolved and deduplicated before distribution"})
		return
	}
	if mode == JobModeSpread {
		req.InputFile = ExpandPath(req.InputFile)
		f, err := os.Open(req.InputFile)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("cannot read source file %q: %v", req.InputFile, err)})
			return
		}
		info, err := f.Stat()
		f.Close()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source must be a non-empty regular file"})
			return
		}
	}

	toolInput := req.InputFile
	if mode == JobModeSpread {
		toolInput = "{input}"
	}

	cmd, err := GenerateToolCommand(req.ToolID, toolInput, req.Options)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Enum streams [FILE:...] records on stdout. Do not redirect them away
	// from the worker's result stream; diagnostics already use stderr.

	// When wordlists are provided, wrap the tool command so the worker:
	// 1. Downloads each wordlist file from the server using curl
	// 2. Passes the local file path to cnc-enum via the appropriate flag
	if wlFlags != "" {
		var dlCmds []string
		if req.WPPlugins != "" {
			dlCmds = append(dlCmds,
				"curl -sfo /tmp/cnc_wp_plugins.txt {http_addr}/api/files/wordlist/wp_plugins.txt",
			)
		}
		if req.JoomlaExts != "" {
			dlCmds = append(dlCmds,
				"curl -sfo /tmp/cnc_joomla_exts.txt {http_addr}/api/files/wordlist/joomla_exts.txt",
			)
		}
		cmd += wlFlags
		cmd = strings.ReplaceAll(cmd, "{wp_plugins_path}", "/tmp/cnc_wp_plugins.txt")
		cmd = strings.ReplaceAll(cmd, "{joomla_exts_path}", "/tmp/cnc_joomla_exts.txt")
		cmd = strings.Join(append(dlCmds, cmd), " && ")
	}

	job := &Job{
		Name:       fmt.Sprintf("Tool: %s", req.ToolID),
		Command:    cmd,
		Mode:       mode,
		InputFile:  req.InputFile,
		OutputFile: req.OutputFile,
	}
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds != NoTimeout && *req.TimeoutSeconds <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "timeout_seconds must be positive or -1 for no overall timeout"})
			return
		}
		job.TimeoutSeconds = *req.TimeoutSeconds
	}

	s.submitJob(job)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":  "launched",
		"job_id":  job.ID,
		"command": cmd,
		"mode":    mode,
	})
}

func (s *Server) saveWordlist(name, content string) error {
	wlDir := filepath.Join(s.config.DataDir, "wordlists")
	if err := os.MkdirAll(wlDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(wlDir, name), []byte(content), 0644); err != nil {
		return err
	}

	// Mirror to WP plugins directory if applicable
	if (strings.HasPrefix(name, "wp") || strings.Contains(name, "wordpress")) && s.config.WPPluginsDir != "" {
		wpDir := ExpandPath(s.config.WPPluginsDir)
		_ = os.MkdirAll(wpDir, 0755)
		_ = os.WriteFile(filepath.Join(wpDir, name), []byte(content), 0644)
	}

	// Mirror to Joomla plugins directory if applicable
	if (strings.HasPrefix(name, "joomla") || strings.HasPrefix(name, "jm")) && s.config.JoomlaPluginsDir != "" {
		jmDir := ExpandPath(s.config.JoomlaPluginsDir)
		_ = os.MkdirAll(jmDir, 0755)
		_ = os.WriteFile(filepath.Join(jmDir, name), []byte(content), 0644)
	}

	return nil
}

// handleToolsWordlistAPI handles GET/POST for persisted tool wordlists.
// GET  /api/tools/wordlists          — list saved wordlists (from wordlists dir, WP plugins dir, and Joomla plugins dir)
// POST /api/tools/wordlists          — save/overwrite a wordlist
func (s *Server) handleToolsWordlistAPI(w http.ResponseWriter, r *http.Request) {
	wlDir := filepath.Join(s.config.DataDir, "wordlists")

	switch r.Method {
	case http.MethodGet:
		type wlInfo struct {
			Name   string `json:"name"`
			Lines  int    `json:"lines"`
			Source string `json:"source,omitempty"`
		}
		seen := make(map[string]bool)
		var list []wlInfo

		scanDir := func(dir string, source string) {
			if dir == "" {
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.IsDir() || seen[e.Name()] {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					continue
				}
				seen[e.Name()] = true
				lines := 0
				for _, l := range strings.Split(string(data), "\n") {
					if strings.TrimSpace(l) != "" {
						lines++
					}
				}
				list = append(list, wlInfo{Name: e.Name(), Lines: lines, Source: source})
			}
		}

		scanDir(wlDir, "wordlists")
		scanDir(ExpandPath(s.config.WPPluginsDir), "wp_plugins")
		scanDir(ExpandPath(s.config.JoomlaPluginsDir), "joomla_plugins")

		writeJSON(w, http.StatusOK, list)

	case http.MethodPost:
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and content are required"})
			return
		}
		if strings.ContainsAny(req.Name, "/\\") || strings.Contains(req.Name, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		if err := s.saveWordlist(req.Name, req.Content); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved", "name": req.Name})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
