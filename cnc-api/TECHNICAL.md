# CNC — Technical Reference for AI Assistants

This document explains the architecture, data flow, and design decisions of the CNC codebase. Read this before making any changes.

---

## Overview

CNC is a Go distributed task system. A single **server** accepts HTTP job submissions, splits input files into line-aware chunks, and distributes one task per chunk to connected **workers** over persistent TCP connections. Workers execute each task as a shell subprocess and report results back. A **CLI** (`cnc`) wraps the HTTP API for human use.

There are no message brokers, databases, or external dependencies beyond `github.com/spf13/cobra` (CLI) and the Go standard library.

---

## File Map

```
cnc/
├── protocol.go          Core types: Job, Task, Worker, Message, all payload structs
├── server.go            Server: TCP listener, HTTP API, job processing, task dispatch
├── worker.go            Worker agent: TCP client, subprocess execution (file + pipe)
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
- `Command string` — shell command template; may contain `{input}` and `{output}` placeholders (file mode) or be a plain command (pipe mode)
- `ExecMode ExecMode` — `"file"` or `"pipe"` (see execution modes below)
- `TimeoutSeconds int` — per-task subprocess timeout; defaults to `DefaultTimeout` (300s) if zero
- `SplitSize int64` — byte threshold for line-aware file splitting; defaults to `DefaultSplitSize` (10MB) if zero

**`Task`** — one chunk of a job. Always has `Type: "shell"`. The `Payload` map carries:
```
"command"          string   // rendered command (file mode) or raw command (pipe mode)
"exec_mode"        string   // "file" or "pipe"
"input_file"       string   // path to the chunk file
"output_file"      string   // predetermined output path
"timeout_seconds"  float64  // JSON numbers decode as float64 in map[string]interface{}
```

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

### TCP Connection Lifecycle (`handleTCPConn`)

Each accepted TCP connection gets:
1. A `json.Decoder` (read loop, current goroutine)
2. A `json.Encoder` (write goroutine only — never written from anywhere else)
3. A `sendCh chan *Message` with a buffer of 256

The **write goroutine** drains `sendCh` and encodes to the socket. This is the fix for the old TCP blocking bug — the mutex is never held during a socket write.

The **read loop** decodes messages and calls the appropriate handler synchronously. On disconnect it calls `markWorkerOffline(workerID)`.

### Task Dispatch (`dispatchTask`)

Called by `taskDispatcher` for every task dequeued from `taskQueue`.

1. Acquires `s.mu.Lock()`
2. Finds a worker where `Status == online && CurrentLoad < MaxTasks && SendCh != nil`
3. Marks the task `assigned`, increments `worker.CurrentLoad`
4. Does a **non-blocking send** into `worker.SendCh`:
   - If the channel accepts: task is dispatched
   - If the channel is full: `requeueTask()` is called (decrements load, puts task back)
5. If no worker is available: requeues after 200ms sleep (without holding the lock)

**Important:** `dispatchTask` holds `s.mu.Lock()` the entire time. Keep any work inside it minimal.

### Job Processing (`processJob`)

Runs in a goroutine. Steps:
1. Sets `job.Status = "running"`
2. Calls `splitInputFile()` — line-aware splitter, never cuts mid-line
3. For each chunk file, renders the command:
   - Replaces `{input}` with the chunk path
   - Replaces `{output}` with `<output_dir>/result_part_NNNN.txt`
4. Creates a `Task` with the rendered command and all payload fields
5. Enqueues each task to `taskQueue` (non-blocking, falls back to goroutine if full)

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

**`execFile`**: runs `sh -c <command>` (command already has `{input}` and `{output}` rendered by the server). Captures stderr. Returns error if exit code != 0.

**`execPipe`**: opens `inputFile` as stdin, creates `outputFile` for stdout, runs `sh -c <command>`. If `outputFile` is empty, falls back to `<DataDir>/result_<taskID>.txt`.

Both modes use `context.WithTimeout(w.ctx, timeout)` — if the worker is stopped mid-task, the subprocess is killed.

### Payload Extraction

`task.Payload` is `map[string]interface{}`. JSON numbers decode as `float64`. Always extract `timeout_seconds` as `float64` then multiply:
```go
timeoutSec, _ := task.Payload["timeout_seconds"].(float64)
timeout := time.Duration(timeoutSec) * time.Second
```

---

## CLI (`cli.go`)

Thin wrapper over the HTTP API using `github.com/spf13/cobra`. No TCP connections. All commands accept a `--server` flag to override the server URL.

Commands:
- `server start` — calls `NewServer(config).Start()` in-process (blocking)
- `worker start` — calls `NewWorkerAgent(config).Start()` in-process (blocking)
- `worker list` — GET `/api/workers`
- `job submit` — interactive prompt → POST `/api/jobs`
- `job list` — GET `/api/jobs`
- `job status <id>` — GET `/api/jobs/{id}`
- `status` — GET `/api/stats`

---

## Data Flow (End to End)

```
User
  │  POST /api/jobs {name, command, exec_mode, input_file, output_dir, ...}
  ▼
Server.handleJobsAPI()
  │  validates required fields, calls submitJob()
  ▼
Server.submitJob()
  │  assigns job ID, stores in s.jobs, calls go processJob()
  ▼
Server.processJob()
  │  splitInputFile() → [part_0001.txt, part_0002.txt, ...]
  │  for each chunk:
  │    render command: {input}→chunk path, {output}→result_part_NNNN.txt
  │    create Task{Type:"shell", Payload:{command, exec_mode, input_file, output_file, timeout}}
  │    push to s.taskQueue
  ▼
Server.taskDispatcher() / dispatchTask()
  │  find available worker, send Task via worker.SendCh
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
  ▼
WorkerAgent.sendResult()
  │  sends Message{type:"task_result"} back to server
  ▼
Server.handleTaskResult()
  │  updates Task.Status, calls updateJobProgress()
  │  decrements worker.CurrentLoad
  ▼
Job.Status = "completed" when all tasks done
```

---

## Known Constraints and Edge Cases

**File paths must be accessible from workers.** In file mode the server renders absolute paths into the command. Workers must have the same filesystem mounted (NFS, shared volume, or the same machine). In pipe mode, the server sends the chunk file path — same constraint applies.

**Split files live in `output_dir`.** `splitInputFile` writes `part_NNNN.txt` files and `result_part_NNNN.txt` into the same `output_dir`. If a job is cancelled or fails, these files remain.

**No authentication.** The TCP port and HTTP API are unauthenticated. Run behind a firewall or VPN.

**Worker ID must be unique.** If two workers register with the same ID, the second registration overwrites the first in `s.workers`. Auto-generation from hostname+PID avoids this for normal use.

**`job_id` format.** Jobs are named `job_<unix_timestamp>_<counter>`. Tasks are named `task_<job_id>_<index>`. These are stored only in memory — server restart loses all state.

**No persistence.** All state (`s.workers`, `s.jobs`, `s.tasks`) is in memory. A server restart loses all job history.

---

## Adding a New Feature: Checklist

Before changing anything:
1. Re-read the relevant section of this document
2. Note which mutexes guard the data you need to touch
3. Check whether `updateJobProgress` is involved — it must be called under `s.mu.Lock()`

Common extension points:
- **New job option** → add field to `Job` in `protocol.go`, use it in `processJob` in `server.go`
- **New CLI command** → add a `cmd*` method to `CLI` in `cli.go`, register it in `Execute()`
- **New HTTP endpoint** → add `mux.HandleFunc` in `Server.Start()`, implement handler in `server.go`
- **Change how workers execute tasks** → modify `executeShellTask` in `worker.go`; the rest of the pipeline is unchanged

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
