# CNC — Technical Reference for AI Assistants

This document explains the architecture, data flow, and design decisions of the CNC codebase. Read this before making any changes.

---

## Overview

CNC is a Go distributed C2 (command and control) platform. A single **server** accepts HTTP job submissions in two modes: **spread** (file-based parallel processing) or **broadcast** (command execution across all workers). Workers connect over persistent TCP, execute tasks as shell subprocesses, and report results with full stdout/stderr capture. A **CLI** (`cnc`) provides both HTTP API wrappers and direct job submission commands.

There are no message brokers, databases, or external dependencies beyond `github.com/spf13/cobra` (CLI) and the Go standard library.

---

## File Map

```
cnc/
├── protocol.go          Core types: Job, Task, Worker, Message, all payload structs
├── server.go            Server: TCP listener, HTTP API, job processing, task dispatch
├── worker.go            Worker agent: TCP client, subprocess execution (file + pipe)
├── gdrive.go            Google Drive: two-way line sync, deduplication & in-place uploader
├── gdrive_test.go       Tests: line deduplication, file detection, and Drive simulation
├── cli.go               CLI: cobra commands wrapping the HTTP API
├── cmd/
│   ├── server/main.go   Server entrypoint (flag parsing, signal handling)
│   ├── worker/main.go   Worker entrypoint (flag parsing, signal handling)
│   └── cnc/main.go      CLI entrypoint
├── server_config.json   Server config template
├── worker_config.json   Worker config template
├── Makefile             build, build-linux, clean
├── go.mod               Module: github.com/fahrel/cnc
└── go.sum
```

---

## Protocol Layer (`protocol.go`)

### Key Types

**`Job`** — user-submitted work unit. Fields of note:
- `Mode JobMode` — `"spread"` (file-based splitting) or `"broadcast"` (single command to all workers)
- `Command string` — shell command template; may contain `{input}` and `{output}` placeholders (spread mode) or be a plain command (broadcast mode)
- `ExecMode ExecMode` — `"file"` or `"pipe"` (spread mode only; broadcast always uses pipe)
- `TimeoutSeconds int` — per-task subprocess timeout; defaults to `DefaultTimeout` (300s) if zero
- `SplitSize int64` — byte threshold for line-aware file splitting (spread mode); defaults to `DefaultSplitSize` (10MB) if zero

**`Task`** — one chunk of a job. Always has `Type: "shell"`. The `Payload` map carries:
```
"command"          string   // rendered command (spread mode) or raw command (broadcast mode)
"exec_mode"        string   // "file" or "pipe"
"input_file"       string   // path to the chunk file (spread mode)
"output_file"      string   // predetermined output path
"timeout_seconds"  float64  // JSON numbers decode as float64 in map[string]interface{}
"worker_id"        string   // pre-assigned worker (broadcast mode only)
```

**`TaskResult`** — returned by workers after task execution. Fields:
```
TaskID     string   // identifies the task
Status     string   // "completed" or "failed"
Message    string   // human-readable status message
Stdout     string   // captured stdout from subprocess
Stderr     string   // captured stderr from subprocess
ExitCode   int      // subprocess exit code; 0 = success, non-zero = failure
```
Workers **always return a TaskResult**, even on failure. Failure is signaled by non-zero exit code.

**`Worker`** — represents a connected worker. `SendCh chan *Message` is the non-blocking write channel. **It is never serialised** (tagged `json:"-"`). The server's TCP write goroutine drains this channel. Set to `nil` when the worker goes offline.

**`Message`** — TCP envelope. `Type` is a string constant (e.g. `"assign_task"`). `Payload` is `json.RawMessage` — call `msg.UnmarshalPayload(&myStruct)` to decode.

### Message Types (TCP only)
```
register_worker    worker → server   RegisterWorkerPayload
worker_heartbeat   worker → server   WorkerHeartbeatPayload
assign_task        server → worker   AssignTaskPayload
task_result        worker → server   TaskResultPayload
shutdown_worker    server → worker   (empty payload)
ack                server → worker   map[string]string{"status":"ok","worker_id":"..."}
```

Workers list and job operations are **HTTP only** — no TCP message types exist for them.

---

## Server (`server.go`)

### Startup Sequence

```
Server.Start()
  ├── os.MkdirAll(config.DataDir)
  ├── go taskDispatcher()      — reads taskQueue, calls dispatchTask()
  ├── go heartbeatChecker()    — marks stale workers offline, requeues their tasks
  ├── go tcpServer()           — accepts worker connections
  └── httpServer.ListenAndServe()
```

### HTTP API

All endpoints have CORS middleware applied. Key endpoints:

- `GET /api/workers` — list all workers
- `GET /api/jobs` — list all jobs
- `POST /api/jobs` — submit a new job (requires `mode`, `command`, other fields depend on mode)
- `GET /api/jobs/{id}` — get job details
- `GET /api/jobs/{id}/tasks` — list all tasks for a job
- `GET /api/stats` — server statistics
- `GET /api/events` — SSE stream of job/worker/task updates

The SSE endpoint sends `SNAPSHOT` messages that include:
- `jobs`: map of all jobs
- `workers`: map of all workers
- `tasks`: map of task IDs to task objects for all jobs

### TCP Connection Lifecycle (`handleTCPConn`)

Each accepted TCP connection gets:
1. A `json.Decoder` (read loop, current goroutine)
2. A `json.Encoder` (write goroutine only — never written from anywhere else)
3. A `sendCh chan *Message` with a buffer of 256

The **write goroutine** drains `sendCh` and encodes to the socket. This is the fix for the old TCP blocking bug — the mutex is never held during a socket write.

The **read loop** decodes messages and calls the appropriate handler synchronously. On disconnect it calls `markWorkerOffline(workerID)`.

### Task Dispatch (`dispatchTask`)

Called by `taskDispatcher` for every task dequeued from `taskQueue`.

**Spread Mode Tasks:**
1. Acquires `s.mu.Lock()`
2. Finds a worker where `Status == online && CurrentLoad < MaxTasks && SendCh != nil`
3. Marks the task `assigned`, increments `worker.CurrentLoad`
4. Does a **non-blocking send** into `worker.SendCh`:
   - If the channel accepts: task is dispatched
   - If the channel is full: `requeueTask()` is called (decrements load, puts task back)
5. If no worker is available: requeues after 200ms sleep (without holding the lock)

**Broadcast Mode Tasks:**
1. Acquires `s.mu.Lock()`
2. Extracts `worker_id` from `task.Payload["worker_id"]`
3. Finds the specific worker; if offline or `SendCh == nil`: marks task failed immediately
4. Otherwise: same non-blocking send logic as spread mode

**Important:** `dispatchTask` holds `s.mu.Lock()` the entire time. Keep any work inside it minimal.

### Job Processing (`processJob`)

Runs in a goroutine. This is the **mode router**:

```go
func (s *Server) processJob(jobID string) {
    // ... initialize job ...
    
    switch job.Mode {
    case JobModeSpread:
        s.processSpreadJob(job)
    case JobModeBroadcast:
        s.processBroadcastJob(job)
    default:
        // error: unknown mode
    }
}
```

**Spread Mode (`processSpreadJob`):**
1. Calls `splitInputFile()` — line-aware splitter, never cuts mid-line
2. For each chunk file, renders the command:
   - Replaces `{input}` with the chunk path
   - Replaces `{output}` with `<output_dir>/result_part_NNNN.txt`
3. Creates a `Task` with the rendered command and all payload fields
4. Enqueues each task to `taskQueue` (non-blocking, falls back to goroutine if full)

**Broadcast Mode (`processBroadcastJob`):**
1. Acquires `s.mu.RLock()` and snapshots all online workers
2. For each online worker:
   - Creates a `Task` with `worker_id` pre-assigned in payload
   - Enqueues to `taskQueue`
3. Marks the job as "running" immediately (tasks may still be pending)

Both modes set `job.Status = "running"` at the start and `"completed"`/`"failed"` at the end.

### Mutex Usage

`s.mu sync.RWMutex` guards: `s.workers`, `s.jobs`, `s.tasks`, `s.jobTasks`, `s.jobCounter`, `s.taskCounter`.

- All HTTP handlers acquire `s.mu.RLock()` for reads, `s.mu.Lock()` for writes
- `handleTaskResult`, `dispatchTask`, `heartbeatChecker`, `markWorkerOffline` all use `s.mu.Lock()`
- `updateJobProgress` is always called while `s.mu` is already held — **do not acquire the lock inside it**

---

## Worker (`worker.go`)

### Connection Model

Single persistent TCP connection to the server. Protected by `w.connMu sync.Mutex` — only `connect()`, `send()`, and `reconnect()` touch `w.conn`/`w.encoder`/`w.decoder`.

`w.reconnecting atomic.Bool` prevents multiple concurrent reconnect attempts.

### Message Loop

`messageLoop()` decodes messages from `w.decoder` in a loop. On any read error it calls `go w.reconnect()` and returns (the goroutine exits). `reconnect()` restarts a new `messageLoop` goroutine on success.

### Task Execution

```
handleAssignTask(msg)
  └── go executeTask(task)
        └── executeShellTask(task)
              ├── ExecModeFile → execFile(command, outputFile, timeout)
              └── ExecModePipe → execPipe(taskID, command, inputFile, outputFile, timeout)
```

**`execFile`**: runs `sh -c <command>` (command already has `{input}` and `{output}` rendered by the server). Captures stdout and stderr. Returns `*TaskResult` with exit code.

**`execPipe`**: opens `inputFile` as stdin, creates `outputFile` for stdout, runs `sh -c <command>`. If `outputFile` is empty, falls back to `<DataDir>/result_<taskID>.txt`. Captures stderr separately.

Both modes use `context.WithTimeout(w.ctx, timeout)` — if the worker is stopped mid-task, the subprocess is killed.

**Result Contract**: Workers **always return a `*TaskResult`**, never an error. Failure is indicated by:
- `Status: "failed"`
- `ExitCode != 0`
- `Stderr` containing error output

The server marks tasks as `"failed"` if `result.Status == "failed"` or `result.ExitCode != 0`.

### Payload Extraction

`task.Payload` is `map[string]interface{}`. JSON numbers decode as `float64`. Always extract `timeout_seconds` as `float64` then multiply:
```go
timeoutSec, _ := task.Payload["timeout_seconds"].(float64)
timeout := time.Duration(timeoutSec) * time.Second
```

---

## Google Drive Synchronization (`gdrive.go`)

`GDriveUploader` provides automatic file watching, two-way line merging, and in-place updates:

### Synchronization Lifecycle (`syncFile`)

1. **Watch Loop**: Periodically scans `watch_dir` (configured by `upload_interval`). Computes the local file's SHA256.
2. **Drive Lookup (`findDriveFilesByName`)**: Searches Google Drive for files with matching `name` within `ParentFolderID`.
   - Uses `SupportsAllDrives(true)` and `IncludeItemsFromAllDrives(true)` for full compatibility with shared team drives.
3. **Multi-Duplicate Cleanup**:
   - If multiple duplicate files exist in Google Drive with the same name, it downloads content from **all** copies.
   - Deletes/trashes extra copies (`Files.Delete`) so that **strictly only 1 file remains** in Drive.
4. **Line-Level Merging & Deduplication (`mergeAndDeduplicateLines`)**:
   - Remote Drive lines come first.
   - New local lines come second.
   - Blank and whitespace-only lines are filtered out.
   - Duplicate lines are removed while preserving first-seen line order.
5. **Local File Sync**:
   - Rewrites the local file with the merged, deduplicated lines so local disk and Drive stay 1:1 identical.
6. **In-Place Update**:
   - Updates the existing Drive file via `Files.Update` with the combined content.
7. **State Tracking**:
   - Both initial and merged SHA256 hashes are added to `u.uploaded` to prevent re-processing loops.

---

## CLI (`cli.go`)

Provides both HTTP API wrappers and direct job submission commands using `github.com/spf13/cobra`.

**Management Commands** (HTTP API wrappers):
- `cnc server start` — calls `NewServer(config).Start()` in-process (blocking)
- `cnc worker start` — calls `NewWorkerAgent(config).Start()` in-process (blocking)
- `cnc worker list` — GET `/api/workers`
- `cnc job list` — GET `/api/jobs`
- `cnc job status <id>` — GET `/api/jobs/{id}`
- `cnc status` — GET `/api/stats`

**Job Submission Commands** (direct submission):
- `cnc spread <file> <command>` — submits a spread-mode job: splits `<file>`, distributes tasks to multiple workers. Command must include `{input}` placeholder.
- `cnc broadcast <command>` — submits a broadcast-mode job: sends `<command>` to all online workers simultaneously.

Both submission commands accept:
- `--server <url>` — override server URL (default: `http://localhost:8080`)
- `--exec-mode <file|pipe>` — execution mode (spread only; broadcast is always pipe)
- `--output <dir>` — output directory (spread only)
- `--timeout <seconds>` — per-task timeout
- `--split-size <bytes>` — chunk size for splitting (spread only)

No TCP connections are made by the CLI — all operations go through HTTP.

---

## Data Flow (End to End)

### Spread Mode Flow

```
User
  │  cnc spread hostnames.txt 'nmap -sV {input} > {output}'
  │  or POST /api/jobs {mode:"spread", command:"...", input_file:"...", ...}
  ▼
Server.handleJobsAPI()
  │  validates mode-specific fields, calls submitJob()
  ▼
Server.submitJob()
  │  assigns job ID, stores in s.jobs, calls go processJob()
  ▼
Server.processJob()
  │  routes to processSpreadJob() based on job.Mode
  ▼
Server.processSpreadJob()
  │  splitInputFile() → [part_0001.txt, part_0002.txt, ...]
  │  for each chunk:
  │    render command: {input}→chunk path, {output}→result_part_NNNN.txt
  │    create Task{Type:"shell", Payload:{command, exec_mode, input_file, output_file, timeout}}
  │    push to s.taskQueue
  ▼
Server.taskDispatcher() / dispatchTask()
  │  find any available worker, send Task via worker.SendCh
  ▼
TCP write goroutine (in handleTCPConn)
  │  encodes Message{type:"assign_task"} to socket
  ▼
WorkerAgent.messageLoop()
  │  decodes message, calls go executeTask()
  ▼
WorkerAgent.executeShellTask()
  │  file mode: sh -c <rendered_command>
  │  pipe mode: sh -c <command> < chunk_file > output_file
  │  captures stdout, stderr, exit code
  ▼
WorkerAgent.sendResult()
  │  sends Message{type:"task_result", Payload:TaskResult{...}} back to server
  ▼
Server.handleTaskResult()
  │  updates Task.Status, Task.Result (stores stdout/stderr/exit code)
  │  calls updateJobProgress()
  │  decrements worker.CurrentLoad
  ▼
Job.Status = "completed" when all tasks done
```

### Broadcast Mode Flow

```
User
  │  cnc broadcast 'uname -a'
  │  or POST /api/jobs {mode:"broadcast", command:"uname -a"}
  ▼
Server.processBroadcastJob()
  │  snapshot all online workers (w1, w2, w3, ...)
  │  for each worker:
  │    create Task{Payload:{worker_id:"w1", command:"uname -a", exec_mode:"pipe"}}
  │    push to s.taskQueue
  ▼
Server.dispatchTask()
  │  extracts worker_id from task.Payload
  │  sends Task only to the pre-assigned worker
  ▼
[rest of flow identical to spread mode — worker executes, returns TaskResult]
  ▼
All tasks complete simultaneously (or fail if workers offline)
```

**Key Difference**: Spread mode distributes tasks dynamically to any available worker. Broadcast mode pre-assigns tasks to specific workers at job creation time.

---

## Known Constraints and Edge Cases

**Chunk files are downloaded over HTTP (spread mode).** In spread mode, chunk files are split by the server into `~/split` and served via HTTP at `/api/files/<name>?part=N`. Workers download their assigned chunk directly to `~/<dest_name>` and interpolate `{input}` with this local path. No shared network volume (NFS) is required.

**Broadcast tasks fail if workers go offline.** In broadcast mode, tasks are pre-assigned to workers at job creation time. If a worker disconnects before the task completes, the task is marked failed. Spread mode tasks can be reassigned to other workers via `requeueTask()`.

**No authentication.** The TCP port and HTTP API are unauthenticated. Run behind a firewall or VPN.

**Worker ID must be unique.** If two workers register with the same ID, the second registration overwrites the first in `s.workers`. Auto-generation from hostname+PID avoids this for normal use.

**`job_id` format.** Jobs are named `job_<unix_timestamp>_<counter>`. Tasks are named `task_<job_id>_<index>`. These are stored only in memory — server restart loses all state.

**No persistence.** All state (`s.workers`, `s.jobs`, `s.tasks`) is in memory. A server restart loses all job history.

**stdout/stderr size limits.** Task results are JSON-serialized over TCP. Extremely large stdout/stderr (>1MB) may cause memory pressure. For long-running tasks that produce large output, redirect to a file instead of capturing stdout.

---

## Adding a New Feature: Checklist

Before changing anything:
1. Re-read the relevant section of this document
2. Note which mutexes guard the data you need to touch
3. Check whether `updateJobProgress` is involved — it must be called under `s.mu.Lock()`

Common extension points:
- **New job mode** → add constant to `JobMode` in `protocol.go`, implement `process<Mode>Job()` in `server.go`, add routing in `processJob()`
- **New job option** → add field to `Job` in `protocol.go`, use it in `process*Job` functions
- **New CLI command** → add a `cmd*` method to `CLI` in `cli.go`, register it in `Execute()`
- **New HTTP endpoint** → add `mux.HandleFunc` in `Server.Start()`, implement handler in `server.go`, remember CORS middleware
- **Change how workers execute tasks** → modify `executeShellTask` in `worker.go`; the rest of the pipeline is unchanged
- **New task result field** → add to `TaskResult` in `protocol.go`, capture in `worker.go`, consume in `server.go` handlers

---

## Dependencies

```
github.com/spf13/cobra v1.8.0   CLI framework
```

That is the only external dependency. `gorilla/websocket` and `golang.org/x/crypto` were removed in the current version.

---

## Build

```bash
make build          # local OS binaries: cnc-server, cnc-worker, cnc
make build-linux    # linux/amd64 static binaries: cnc-server-linux, cnc-worker-linux, cnc-linux
make clean          # removes all built binaries
```

Go version required: 1.21+
