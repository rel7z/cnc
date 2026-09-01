# CNC - Technical Documentation

Dokumentasi teknis untuk developer dan AI yang akan maintain/extend system ini.

## 📐 Architecture Overview

### System Design

CNC menggunakan **Master-Worker pattern** dengan components:

1. **Server (Master)**
   - Centralized coordinator
   - HTTP API untuk external clients
   - TCP server untuk worker connections
   - Task queue dan dispatcher
   - Job dan task state management

2. **Worker (Executor)**
   - Task execution engine
   - TCP client ke server
   - Heartbeat mechanism
   - Autonomous reconnection

3. **CLI (Client)**
   - User interface
   - HTTP client untuk API calls
   - Job submission dan monitoring

### Communication Protocols

**Worker ↔ Server:** TCP dengan JSON messages
- Persistent connection
- Bidirectional communication
- Message-based protocol (see `protocol.go`)

**Client ↔ Server:** HTTP REST API
- Stateless
- JSON request/response
- Standard HTTP methods

## 🗂️ Project Structure

```
cnc/
├── cmd/
│   ├── cnc/main.go          # CLI entry point
│   ├── server/main.go       # Server entry point
│   └── worker/main.go       # Worker entry point
├── protocol.go              # Message types dan structs
├── server.go                # Server implementation
├── worker.go                # Worker implementation
├── cli.go                   # CLI commands
├── go.mod                   # Go dependencies
├── Makefile                 # Build automation
├── README.md                # User documentation
├── TECHNICAL.md             # This file
├── test_full.sh             # Integration test script
├── server_config.json       # Server config
└── worker_config.json       # Worker config
```

## 🔧 Core Components

### 1. Protocol (`protocol.go`)

Defines all message types dan data structures.

**Key Types:**
```go
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

type Message struct {
    Type      string          `json:"type"`
    Payload   json.RawMessage `json:"payload"`
    Timestamp time.Time       `json:"timestamp"`
}
```

**Message Types:**
- `register_worker`: Worker registration
- `worker_heartbeat`: Keep-alive dari worker
- `assign_task`: Server assign task ke worker
- `task_result`: Worker report hasil
- `submit_job`: Client submit job baru
- `job_status`: Request job status
- Dan lain-lain (lihat constants di protocol.go)

**Important Structs:**
- `Task`: Single unit of work dengan JobID, Type, Payload, Status
- `Job`: Collection of tasks dengan metadata
- `Worker`: Worker registration info
- `TaskResult`: Output dari task execution

### 2. Server (`server.go`)

Server adalah koordinator pusat.

**Main Struct:**
```go
type Server struct {
    mu           sync.RWMutex
    workers      map[string]*Worker      // Registered workers
    jobs         map[string]*Job         // All jobs
    tasks        map[string]*Task        // All tasks
    jobTasks     map[string][]string     // JobID -> TaskIDs mapping
    taskQueue    chan *Task              // Pending tasks queue
    config       *ServerConfig
    ctx          context.Context
    cancel       context.CancelFunc
}
```

**Key Functions:**

**`Start()`**
- Initialize HTTP dan TCP servers
- Start background goroutines:
  - `taskDispatcher()`: Assign tasks ke workers
  - `heartbeatChecker()`: Monitor worker health
  - `tcpServer()`: Accept worker connections

**`processJob(job *Job)`**
- Split input file dengan `SplitInputFile()`
- Create tasks untuk each chunk
- Queue tasks untuk execution

**`SplitInputFile(inputFile, splitSize, outputDir)`**
- **Line-aware splitting** - tidak potong di tengah line
- Baca file line-by-line menggunakan `bufio.Scanner`
- Create part files ketika size threshold tercapai
- Return list of split file paths

**`taskDispatcher()`**
- Continuous loop yang monitor task queue
- Find available workers (online + capacity)
- Assign tasks menggunakan `dispatchTask()`
- Requeue jika tidak ada worker available

**`heartbeatChecker()`**
- Check worker `LastSeen` timestamps
- Mark offline jika > `HeartbeatTTL`
- Requeue tasks dari offline workers

**`handleTCPConnection(conn)`**
- Handle single worker connection
- Decode JSON messages
- Call appropriate handlers
- **CURRENT ISSUE**: Message handling dapat block
  - Synchronous processing
  - Race condition dengan concurrent messages
  - Need refactor untuk async handling

**HTTP Handlers:**
- `handleWorkersAPI`: GET /api/workers
- `handleJobsAPI`: GET/POST /api/jobs  
- `handleTasksAPI`: GET /api/tasks
- `handleStatsAPI`: GET /api/stats

### 3. Worker (`worker.go`)

Worker execute tasks dan report results.

**Main Struct:**
```go
type WorkerAgent struct {
    mu              sync.RWMutex
    config          *WorkerConfig
    worker          *Worker
    tasks           map[string]*Task     // Active tasks
    conn            net.Conn             // TCP connection
    encoder         *json.Encoder
    decoder         *json.Decoder
    stats           WorkerStats          // Performance metrics
    ctx             context.Context
    cancel          context.CancelFunc
}
```

**Key Functions:**

**`Start()`**
- Connect ke server (TCP atau WebSocket)
- Register worker
- Start background goroutines:
  - `heartbeatLoop()`: Send periodic heartbeats
  - `messageLoop()`: Receive messages dari server
  - `statsReporter()`: Log statistics

**`executeTask(task)`**
- Route ke appropriate executor based on task type
- Handle success/failure
- Send result ke server
- Update statistics

**`executeDomainResolve(task)`**
- Baca domains dari input file
- Parallel DNS lookups (100 goroutines)
- Extract /24 IP prefixes
- Generate all IPs in each /24 range
- Write ke output file
- Return `TaskResult` dengan metadata

**`executeIPScan(task)`**
- Baca IPs dari input file
- Spawn worker pool (DefaultScanWorkers = 1000)
- TCP ping ke multiple ports
- Check port 80 availability
- Write hasil ke two files: ping_*.txt, port80_*.txt

**`sendTaskResult(taskID, result, errMsg)`**
- Create TaskResultPayload
- Marshal to JSON message
- Send via TCP connection
- **CURRENT ISSUE**: 
  - Blocking for ~58 seconds on 244-byte message
  - Root cause: TCP encoder.Encode blocking
  - Likely race condition atau buffer issue
  - Need investigation atau switch protocol

**`reconnect()`**
- Attempt reconnection 10 times
- 5-second delay between attempts
- Re-register after successful connect
- Restart message loop

### 4. CLI (`cli.go`)

Command-line interface untuk user interaction.

**Commands:**
- `server start/stop`: Manage server
- `worker start/stop`: Manage worker
- `job submit/list/status/cancel/logs`: Job management
- `workers`: List all workers
- `status`: Show cluster stats
- `split`: Manually split file
- `config`: Manage CLI configuration

**HTTP Client:**
- `client *http.Client` dengan 30s timeout
- JSON encoding/decoding
- Fallback ke TCP untuk beberapa operations

## 🔄 Data Flow

### Job Submission Flow

```
1. Client -> Server: POST /api/jobs
   {
     job: {
       name, type, input_file, output_dir, split_size
     }
   }

2. Server: Create Job
   - Generate job_id
   - Set status = "pending"
   - Store dalam jobs map

3. Server: processJob() goroutine
   a. SplitInputFile()
      - Baca input file line-by-line
      - Create part files (part_0001.txt, part_0002.txt, ...)
      - Return paths
   
   b. Create Tasks
      - 1 task per part file
      - task.Payload = {input_file: part_path, output_dir, ...}
      - Store dalam tasks map
      - Add task_id ke jobTasks[job_id]
      - Queue ke taskQueue

4. Server: taskDispatcher()
   - Read from taskQueue
   - Find available worker
   - sendTaskToWorker()
   - Update task.Status = "assigned"

5. Worker: Receive AssignTask message
   - handleAssignTask()
   - Launch executeTask() goroutine

6. Worker: Execute task
   - executeDomainResolve() or executeIPScan()
   - Process input file
   - Generate output file
   - Create TaskResult

7. Worker -> Server: TaskResult message
   - sendTaskResult()
   - **BLOCKS HERE** (current issue)

8. Server: Receive TaskResult
   - handleTaskResult()
   - Update task.Status = "completed"
   - Store task.Result
   - updateJobProgress()
   - Update worker.CurrentLoad--

9. Server: Job Completion
   - When all tasks completed/failed
   - job.Status = "completed"
   - job.CompletedAt = now
```

### Heartbeat Flow

```
Worker:
- Every 5 seconds
- Send WorkerHeartbeatPayload {WorkerID, Status, CurrentLoad}

Server:
- Receive heartbeat
- Update worker.LastSeen = now
- Update worker.Status and worker.CurrentLoad

heartbeatChecker():
- Every 10 seconds
- Check all workers
- If now - worker.LastSeen > HeartbeatTTL:
  - Mark worker offline
  - Requeue assigned tasks
```

## 🐛 Known Issues

### Issue #1: Task Result Message Blocking

**Symptom:**
- Worker completes task successfully
- Output file generated correctly
- `sendTaskResult()` blocks for ~58 seconds
- Server never receives message
- Worker marked offline
- Job never completes

**Timeline:**
```
11:30:31 - Task starts executing
11:30:31 - Task completes, sending result... (244 bytes)
11:31:29 - (58 seconds later) Connection lost
11:31:29 - Task result sent successfully
```

**Root Cause Hypothesis:**
1. **TCP Write Blocking**: `encoder.Encode()` blocks karena TCP buffer full atau connection issue
2. **Race Condition**: Heartbeat loop dan executeTask goroutine both writing ke same connection
3. **Server Read Blocking**: Server's decoder.Decode() blocking message queue
4. **Timeout Cascade**: Server's 60s read deadline menyebabkan connection close

**Evidence:**
- Message size: only 244 bytes (not a size issue)
- Consistent 58-second block time
- Worker has `connMu` mutex protecting writes
- Server marks worker offline at ~30s (heartbeat TTL)
- Connection breaks at ~60s (server read deadline)

**Attempted Fixes:**
1. ✅ Removed server read deadline - still blocks
2. ✅ Made message handling async - created new issues
3. ✅ Added thread-safe encoder wrapper - still blocks
4. ❌ Increased heartbeat TTL to 120s - same issue

**Possible Solutions:**
1. **Switch to WebSocket**: Built-in ping/pong, better async support
2. **Use gRPC**: Streaming support, better concurrency
3. **Buffered Channels**: Queue messages instead of direct send
4. **Separate Connections**: One for heartbeat, one for task results
5. **HTTP for Results**: Workers POST results via HTTP API instead of TCP

### Issue #2: Worker Encoder Storage

**Problem:**
Server stores `worker.Encoder` untuk send tasks, tapi encoder tied ke specific connection. Jika connection changes, encoder invalid.

**Current Workaround:**
Update encoder pada setiap heartbeat, tapi masih fragile.

**Better Solution:**
- Don't store encoder
- Use connection pool or message queue
- Workers poll for tasks instead of push

### Issue #3: Large Output in Task Results

**Not Currently Used:**
TaskResult originally designed untuk include output data dalam message. Ini akan cause huge messages.

**Current Solution:**
TaskResult hanya include metadata:
- OutputFile path
- LinesOut count
- BytesOut size
- Stats map

Output files written directly ke shared filesystem atau uploaded separately.

## 🔮 Future Improvements

### High Priority

1. **Fix TCP Blocking Issue**
   - Implement WebSocket support
   - Or use HTTP POST untuk task results
   - Add proper connection pooling

2. **Result Aggregation**
   - Combine split files into single output
   - Option untuk merge atau keep separate
   - Progress tracking untuk aggregation

3. **Authentication**
   - Worker authentication tokens
   - API key untuk HTTP API
   - TLS/SSL support

### Medium Priority

4. **Persistence**
   - Save job/task state ke database
   - Resume interrupted jobs
   - Job history dan auditing

5. **Better Error Handling**
   - Detailed error messages
   - Error categorization
   - Retry strategies per error type

6. **Resource Limits**
   - CPU/memory limits per task
   - Disk space monitoring
   - Network bandwidth throttling

7. **Metrics & Monitoring**
   - Prometheus metrics export
   - Grafana dashboards
   - Alert integration

### Low Priority

8. **Web UI**
   - React-based dashboard
   - Real-time job monitoring
   - Worker management

9. **Task Priorities**
   - Priority queue implementation
   - SLA-based scheduling
   - Cost optimization

10. **Advanced Features**
    - Task dependencies (DAG)
    - Conditional execution
    - Custom task types via plugins

## 🧪 Testing

### Unit Tests

**To Add:**
```go
// server_test.go
func TestSplitInputFile(t *testing.T)
func TestTaskDispatcher(t *testing.T)
func TestHeartbeatChecker(t *testing.T)

// worker_test.go  
func TestExecuteDomainResolve(t *testing.T)
func TestExecuteIPScan(t *testing.T)
func TestReconnection(t *testing.T)

// protocol_test.go
func TestMessageEncoding(t *testing.T)
func TestPayloadUnmarshaling(t *testing.T)
```

### Integration Tests

**Current:** `test_full.sh`
- Start server
- Start worker
- Submit job
- Monitor completion
- Verify output

**To Add:**
- Multi-worker test
- Failure scenarios
- Performance benchmarks
- Load testing

### Manual Testing

```bash
# Test split functionality
./cnc split test_domains.txt ./output 1024

# Test server API
curl http://localhost:8080/api/stats
curl http://localhost:8080/api/workers

# Test job submission
curl -X POST http://localhost:8080/api/jobs \
  -d '{"job":{...}}'

# Monitor logs
tail -f server.log worker.log
```

## 📊 Performance Considerations

### Bottlenecks

1. **File I/O**
   - Reading large input files
   - Writing output files
   - Use buffered I/O (`bufio`)

2. **Network**
   - Message serialization
   - TCP connection overhead
   - Consider compression

3. **Memory**
   - Task queue size
   - In-memory state maps
   - Large file processing

### Optimizations

1. **Concurrency**
   - Worker pool size tuning
   - Parallel file processing
   - Async message handling

2. **Resource Usage**
   - Adjust split_size based on memory
   - Limit concurrent tasks
   - Implement backpressure

3. **Network Efficiency**
   - Keep-alive connections
   - Message batching
   - Protocol buffers instead of JSON

## 🔐 Security Considerations

### Current State
- ❌ No authentication
- ❌ No encryption
- ❌ No input validation
- ❌ No rate limiting
- ❌ No access control

### Required for Production

1. **Authentication**
   - Worker registration tokens
   - API keys untuk CLI
   - JWT untuk web clients

2. **Encryption**
   - TLS untuk all connections
   - Encrypt sensitive job data
   - Secure configuration storage

3. **Input Validation**
   - Sanitize file paths
   - Validate job parameters
   - Limit file sizes

4. **Access Control**
   - Role-based permissions
   - Job ownership
   - Resource quotas

5. **Audit Logging**
   - All API calls
   - Job submissions
   - Worker registrations
   - Security events

## 📚 Code Style & Conventions

### Go Best Practices

1. **Error Handling**
   ```go
   // Good
   if err := doSomething(); err != nil {
       return fmt.Errorf("failed to do something: %w", err)
   }
   
   // Avoid
   doSomething()  // Ignoring errors
   ```

2. **Mutex Usage**
   ```go
   // Always defer unlock
   s.mu.Lock()
   defer s.mu.Unlock()
   
   // RLock untuk read-only
   s.mu.RLock()
   defer s.mu.RUnlock()
   ```

3. **Context Usage**
   ```go
   // Pass context untuk cancellation
   func (w *WorkerAgent) executeTask(ctx context.Context, task *Task)
   
   // Check cancellation
   select {
   case <-ctx.Done():
       return ctx.Err()
   default:
       // Continue
   }
   ```

4. **Logging**
   ```go
   // Structured logging
   log.Printf("Task %s completed: lines=%d, bytes=%d", 
       task.ID, result.LinesOut, result.BytesOut)
   
   // Not just
   log.Println("Task completed")
   ```

### Naming Conventions

- Types: PascalCase (`TaskType`, `WorkerStatus`)
- Functions: camelCase (`executeTask`, `sendMessage`)
- Constants: PascalCase with prefix (`TaskStatusPending`, `MsgTypeRegisterWorker`)
- Private fields: camelCase with receiver prefix (`s.mu`, `w.conn`)

## 🛠️ Development Setup

### Prerequisites

```bash
# Install Go
brew install go  # macOS
# or download from golang.org

# Install tools
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

### Build & Run

```bash
# Get dependencies
go mod download

# Build
make build

# Run server
./cnc-server

# Run worker
./cnc-worker

# Run tests
go test -v ./...

# Lint
golangci-lint run
```

### Debugging

```bash
# Enable verbose logging
export DEBUG=1
./cnc-server 2>&1 | tee server.log

# Use delve debugger
dlv debug ./cmd/server/main.go

# Print stack traces
export GOTRACEBACK=all
```

## 📖 Additional Resources

### Go Documentation
- [Effective Go](https://golang.org/doc/effective_go)
- [Go Concurrency Patterns](https://go.dev/blog/pipelines)
- [Context Package](https://pkg.go.dev/context)

### Similar Projects
- [Dask Distributed](https://distributed.dask.org/)
- [Apache Spark](https://spark.apache.org/)
- [Celery](https://docs.celeryproject.org/)

### Network Programming
- [TCP/IP Illustrated](https://www.amazon.com/TCP-Illustrated-Vol-Addison-Wesley-Professional/dp/0201633469)
- [Go Network Programming](https://ipfs.io/ipfs/QmfYeDhGH9bZzihBUDEQbCbTc5k5FZKURMUoUvfmc27BwL/socket/tcp_sockets.html)

---

**Document Version:** 1.0  
**Last Updated:** September 2026  
**For:** AI Assistants & Developers  
**Project:** CNC Distributed Cluster Controller
