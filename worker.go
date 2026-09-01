package cnc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type WorkerAgent struct {
	mu              sync.RWMutex
	config          *WorkerConfig
	worker          *Worker
	tasks           map[string]*Task
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	conn            net.Conn
	wsConn          *websocket.Conn
	encoder         *json.Encoder
	decoder         *json.Decoder
	heartbeatTicker *time.Ticker
	stats           WorkerStats
	reconnecting    atomic.Bool
	connMu          sync.Mutex
}

type WorkerConfig struct {
	ServerAddr   string   `json:"server_addr"`
	WorkerID     string   `json:"worker_id"`
	MaxTasks     int      `json:"max_tasks"`
	Capabilities []string `json:"capabilities"`
	DataDir      string   `json:"data_dir"`
	UseWebSocket bool     `json:"use_websocket"`
}

type WorkerStats struct {
	TasksCompleted uint64
	TasksFailed    uint64
	BytesProcessed uint64
	LinesProcessed uint64
	StartTime      time.Time
}

func DefaultWorkerConfig() *WorkerConfig {
	hostname, _ := os.Hostname()
	return &WorkerConfig{
		ServerAddr:   "localhost:9090",
		WorkerID:     fmt.Sprintf("worker_%s_%d", hostname, os.Getpid()),
		MaxTasks:     runtime.NumCPU() * 2,
		Capabilities: []string{"domain_resolve", "ip_scan"},
		DataDir:      "./worker_data",
		UseWebSocket: false,
	}
}

func LoadWorkerConfig(path string) (*WorkerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultWorkerConfig(), err
	}
	var config WorkerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if config.ServerAddr == "" {
		config.ServerAddr = "localhost:9090"
	}
	if config.WorkerID == "" {
		hostname, _ := os.Hostname()
		config.WorkerID = fmt.Sprintf("worker_%s_%d", hostname, os.Getpid())
	}
	if config.MaxTasks == 0 {
		config.MaxTasks = runtime.NumCPU() * 2
	}
	if len(config.Capabilities) == 0 {
		config.Capabilities = []string{"domain_resolve", "ip_scan"}
	}
	if config.DataDir == "" {
		config.DataDir = "./worker_data"
	}
	return &config, nil
}

func NewWorkerAgent(config *WorkerConfig) *WorkerAgent {
	if config == nil {
		config = DefaultWorkerConfig()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerAgent{
		config: config,
		worker: &Worker{
			ID:           config.WorkerID,
			Address:      config.ServerAddr,
			Status:       WorkerStatusOnline,
			Capabilities: config.Capabilities,
			MaxTasks:     config.MaxTasks,
			Registered:   time.Now(),
		},
		tasks: make(map[string]*Task),
		ctx:   ctx,
		cancel: cancel,
		stats: WorkerStats{StartTime: time.Now()},
	}
}

func (w *WorkerAgent) Start() error {
	if err := os.MkdirAll(w.config.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data dir: %w", err)
	}

	if err := w.connect(); err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	if err := w.register(); err != nil {
		return fmt.Errorf("failed to register with server: %w", err)
	}

	w.wg.Add(1)
	go w.heartbeatLoop()

	w.wg.Add(1)
	go w.messageLoop()

	w.wg.Add(1)
	go w.statsReporter()

	log.Printf("Worker %s started, connected to %s", w.config.WorkerID, w.config.ServerAddr)
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
	if w.wsConn != nil {
		w.wsConn.Close()
	}
	w.connMu.Unlock()
	
	if w.heartbeatTicker != nil {
		w.heartbeatTicker.Stop()
	}
	
	w.wg.Wait()
	log.Printf("Worker %s stopped", w.config.WorkerID)
}

func (w *WorkerAgent) connect() error {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	var err error
	if w.config.UseWebSocket {
		err = w.connectWebSocketLocked()
	} else {
		err = w.connectTCPLocked()
	}
	return err
}

func (w *WorkerAgent) connectTCPLocked() error {
	conn, err := net.DialTimeout("tcp", w.config.ServerAddr, 10*time.Second)
	if err != nil {
		return err
	}
	w.conn = conn
	w.encoder = json.NewEncoder(conn)
	w.decoder = json.NewDecoder(conn)
	log.Printf("Connected to server via TCP: %s", w.config.ServerAddr)
	return nil
}

func (w *WorkerAgent) connectWebSocketLocked() error {
	url := fmt.Sprintf("ws://%s/ws", w.config.ServerAddr)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return err
	}
	w.wsConn = conn
	log.Printf("Connected to server via WebSocket: %s", url)
	return nil
}

func (w *WorkerAgent) register() error {
	msg, err := NewMessage(MsgTypeRegisterWorker, RegisterWorkerPayload{Worker: *w.worker})
	if err != nil {
		return err
	}
	
	if err := w.sendMessage(msg); err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}
	
	log.Printf("Registration sent to server")
	return nil
}

func (w *WorkerAgent) heartbeatLoop() {
	defer w.wg.Done()
	w.heartbeatTicker = time.NewTicker(5 * time.Second)
	defer w.heartbeatTicker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.heartbeatTicker.C:
			w.sendHeartbeat()
		}
	}
}

func (w *WorkerAgent) sendHeartbeat() {
	w.mu.RLock()
	currentLoad := len(w.tasks)
	w.mu.RUnlock()

	payload := WorkerHeartbeatPayload{
		WorkerID:    w.config.WorkerID,
		Status:      WorkerStatusOnline,
		CurrentLoad: currentLoad,
	}
	msg, _ := NewMessage(MsgTypeWorkerHeartbeat, payload)
	
	if err := w.sendMessage(msg); err != nil {
		log.Printf("Failed to send heartbeat: %v", err)
	}
}

func (w *WorkerAgent) messageLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
			var msg Message
			var err error

			w.connMu.Lock()
			if w.config.UseWebSocket && w.wsConn != nil {
				err = w.wsConn.ReadJSON(&msg)
			} else if w.decoder != nil {
				err = w.decoder.Decode(&msg)
			} else {
				w.connMu.Unlock()
				time.Sleep(100 * time.Millisecond)
				continue
			}
			w.connMu.Unlock()

			if err != nil {
				if err != io.EOF {
					log.Printf("Message read error: %v", err)
				}
				if !w.reconnecting.Load() {
					w.reconnect()
				}
				return
			}
			w.handleMessage(&msg)
		}
	}
}

func (w *WorkerAgent) handleMessage(msg *Message) {
	switch msg.Type {
	case MsgTypeAssignTask:
		w.handleAssignTask(msg)
	case MsgTypeShutdownWorker:
		log.Println("Received shutdown command")
		w.cancel()
	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

func (w *WorkerAgent) handleAssignTask(msg *Message) {
	var payload AssignTaskPayload
	if err := msg.UnmarshalPayload(&payload); err != nil {
		log.Printf("Invalid task payload: %v", err)
		return
	}

	task := &payload.Task
	log.Printf("Received task: %s (type: %s, job: %s)", task.ID, task.Type, task.JobID)

	w.mu.Lock()
	w.tasks[task.ID] = task
	w.mu.Unlock()

	go w.executeTask(task)
}

func (w *WorkerAgent) executeTask(task *Task) {
	log.Printf("Executing task %s", task.ID)

	var result *TaskResult
	var err error

	switch task.Type {
	case TaskTypeDomainResolve:
		result, err = w.executeDomainResolve(task)
	case TaskTypeIPScan:
		result, err = w.executeIPScan(task)
	default:
		err = fmt.Errorf("unknown task type: %s", task.Type)
	}

	w.mu.Lock()
	delete(w.tasks, task.ID)
	w.mu.Unlock()

	if err != nil {
		atomic.AddUint64(&w.stats.TasksFailed, 1)
		log.Printf("Task %s failed: %v", task.ID, err)
		if sendErr := w.sendTaskResult(task.ID, nil, err.Error()); sendErr != nil {
			log.Printf("ERROR: Failed to send task failure result: %v", sendErr)
		}
	} else {
		atomic.AddUint64(&w.stats.TasksCompleted, 1)
		if result != nil {
			atomic.AddUint64(&w.stats.BytesProcessed, uint64(result.BytesOut))
			atomic.AddUint64(&w.stats.LinesProcessed, uint64(result.LinesOut))
		}
		log.Printf("Task %s completed successfully, sending result...", task.ID)
		if sendErr := w.sendTaskResult(task.ID, result, ""); sendErr != nil {
			log.Printf("ERROR: Failed to send task completion result: %v", sendErr)
		} else {
			log.Printf("Task %s result sent successfully", task.ID)
		}
	}
}

func (w *WorkerAgent) executeDomainResolve(task *Task) (*TaskResult, error) {
	inputFile, ok := task.Payload["input_file"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid input_file in task payload")
	}
	
	outputDir, ok := task.Payload["output_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid output_dir in task payload")
	}

	f, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer f.Close()

	outputFile := filepath.Join(outputDir, fmt.Sprintf("resolved_%s", filepath.Base(inputFile)))
	out, err := os.Create(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	defer out.Close()

	writer := bufio.NewWriter(out)
	defer writer.Flush()
	
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, FileReadBufferSize), 10*FileReadBufferSize)

	var domains []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		domain := strings.TrimPrefix(line, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domains = append(domains, domain)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading input: %w", err)
	}

	ipPrefixes := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 100)

	for _, domain := range domains {
		wg.Add(1)
		sem <- struct{}{}
		go func(d string) {
			defer wg.Done()
			defer func() { <-sem }()

			ips, err := net.LookupIP(d)
			if err != nil {
				return
			}

			for _, ip := range ips {
				if ip4 := ip.To4(); ip4 != nil {
					parts := strings.Split(ip4.String(), ".")
					if len(parts) == 4 {
						prefix := strings.Join(parts[:3], ".")
						mu.Lock()
						ipPrefixes[prefix] = true
						mu.Unlock()
					}
				}
			}
		}(domain)
	}

	wg.Wait()

	var linesOut int64
	for prefix := range ipPrefixes {
		for i := 1; i <= 255; i++ {
			fmt.Fprintf(writer, "%s.%d\n", prefix, i)
			linesOut++
		}
	}
	writer.Flush()

	info, _ := out.Stat()
	return &TaskResult{
		OutputFile: outputFile,
		LinesOut:   linesOut,
		BytesOut:   info.Size(),
	}, nil
}

func (w *WorkerAgent) executeIPScan(task *Task) (*TaskResult, error) {
	inputFile, ok := task.Payload["input_file"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid input_file in task payload")
	}
	
	outputDir, ok := task.Payload["output_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid output_dir in task payload")
	}

	f, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer f.Close()

	pingFile := filepath.Join(outputDir, fmt.Sprintf("ping_%s", filepath.Base(inputFile)))
	portFile := filepath.Join(outputDir, fmt.Sprintf("port80_%s", filepath.Base(inputFile)))

	pingOut, err := os.Create(pingFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create ping output: %w", err)
	}
	defer pingOut.Close()

	portOut, err := os.Create(portFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create port output: %w", err)
	}
	defer portOut.Close()

	pingWriter := bufio.NewWriter(pingOut)
	portWriter := bufio.NewWriter(portOut)
	defer pingWriter.Flush()
	defer portWriter.Flush()

	ipChan := make(chan string, DefaultScanWorkers*4)
	var wg sync.WaitGroup

	for i := 0; i < DefaultScanWorkers; i++ {
		wg.Add(1)
		go w.scanWorker(ipChan, pingWriter, portWriter, &wg)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, FileReadBufferSize), 10*FileReadBufferSize)
	
	for scanner.Scan() {
		ip := strings.TrimSpace(scanner.Text())
		if ip != "" {
			ipChan <- ip
		}
	}
	close(ipChan)

	wg.Wait()
	pingWriter.Flush()
	portWriter.Flush()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading input: %w", err)
	}

	pingInfo, _ := os.Stat(pingFile)
	portInfo, _ := os.Stat(portFile)

	return &TaskResult{
		OutputFile: pingFile,
		LinesOut:   0,
		BytesOut:   pingInfo.Size() + portInfo.Size(),
		Stats: map[string]interface{}{
			"ping_file": pingFile,
			"port_file": portFile,
		},
	}, nil
}

func (w *WorkerAgent) scanWorker(ipChan <-chan string, pingWriter, portWriter *bufio.Writer, wg *sync.WaitGroup) {
	defer wg.Done()

	pingMu := &sync.Mutex{}
	portMu := &sync.Mutex{}

	for ip := range ipChan {
		if w.tcpPing(ip) {
			pingMu.Lock()
			pingWriter.WriteString(ip + "\n")
			pingMu.Unlock()

			if w.checkPort80(ip) {
				portMu.Lock()
				portWriter.WriteString(ip + "\n")
				portMu.Unlock()
			}
		}
	}
}

func (w *WorkerAgent) tcpPing(ip string) bool {
	ports := []string{"80", "443", "8080", "22", "21", "25", "53", "110", "143", "993", "995", "3306", "3389", "5432", "5900", "8000", "8443", "8888"}
	for _, port := range ports {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, port), DefaultPingTimeout)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

func (w *WorkerAgent) checkPort80(ip string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "80"), DefaultPortTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (w *WorkerAgent) sendTaskResult(taskID string, result *TaskResult, errMsg string) error {
	payload := TaskResultPayload{
		TaskID:   taskID,
		WorkerID: w.config.WorkerID,
		Result:   result,
		Error:    errMsg,
	}
	msg, err := NewMessage(MsgTypeTaskResult, payload)
	if err != nil {
		log.Printf("Failed to create task result message: %v", err)
		return fmt.Errorf("failed to create message: %w", err)
	}
	
	// Log message size
	msgJSON, _ := json.Marshal(msg)
	log.Printf("Sending task result message: %d bytes", len(msgJSON))
	
	if err := w.sendMessage(msg); err != nil {
		log.Printf("Failed to send task result: %v", err)
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}

func (w *WorkerAgent) sendMessage(msg *Message) error {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	if w.config.UseWebSocket && w.wsConn != nil {
		return w.wsConn.WriteJSON(msg)
	}
	if w.encoder != nil {
		return w.encoder.Encode(msg)
	}
	return fmt.Errorf("no connection available")
}

func (w *WorkerAgent) reconnect() {
	if !w.reconnecting.CompareAndSwap(false, true) {
		return
	}
	defer w.reconnecting.Store(false)

	log.Println("Connection lost, attempting to reconnect...")
	
	for i := 0; i < 10; i++ {
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(5 * time.Second):
			if err := w.connect(); err != nil {
				log.Printf("Reconnect attempt %d failed: %v", i+1, err)
				continue
			}
			
			if err := w.register(); err != nil {
				log.Printf("Re-registration failed: %v", err)
				continue
			}
			
			log.Println("Reconnected successfully")
			
			// Restart message loop
			w.wg.Add(1)
			go w.messageLoop()
			return
		}
	}
	
	log.Println("Failed to reconnect after 10 attempts, shutting down")
	w.cancel()
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
			log.Printf("Worker %s stats: Completed=%d, Failed=%d, Lines=%d, Bytes=%d, Uptime=%s",
				w.config.WorkerID,
				atomic.LoadUint64(&w.stats.TasksCompleted),
				atomic.LoadUint64(&w.stats.TasksFailed),
				atomic.LoadUint64(&w.stats.LinesProcessed),
				atomic.LoadUint64(&w.stats.BytesProcessed),
				time.Since(w.stats.StartTime).Round(time.Second),
			)
		}
	}
}
