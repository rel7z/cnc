# CNC — Distributed Shell Task Cluster

CNC is a lightweight command-and-control system for distributing shell commands across multiple worker machines. You define the command, point it at an input file, and CNC splits the work across all connected workers automatically.

---

## How It Works

1. You submit a job with a shell command and a large input file
2. The server splits the input file into chunks
3. Each chunk is dispatched as a task to an available worker
4. Workers execute the command as a subprocess (file mode or pipe mode)
5. Results are written to the output directory

---

## Quick Start

### 1. Build

```bash
make build          # builds for your current OS
make build-linux    # cross-compiles for Linux amd64
```

Produces: `cnc-server`, `cnc-worker`, `cnc` (and `-linux` variants)

### 2. Start the server

```bash
./cnc-server
# or with a custom config:
./cnc-server -config server_config.json
```

Default ports: HTTP `:8080`, TCP `:9090`

### 3. Start one or more workers

```bash
./cnc-worker
# or with a custom config:
./cnc-worker -config worker_config.json
```

Workers connect to the server over TCP and wait for tasks. You can run as many workers as you like — on the same machine or on remote servers.

### 4. Submit a job

**Via CLI (interactive):**
```bash
./cnc job submit
```

**Via HTTP API:**
```bash
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name":            "my job",
    "command":         "grep foo {input} > {output}",
    "exec_mode":       "file",
    "input_file":      "/data/biglist.txt",
    "output_dir":      "/data/results",
    "split_size":      10485760,
    "timeout_seconds": 300
  }'
```

### 5. Monitor

```bash
./cnc status            # cluster-wide stats
./cnc job list          # all jobs
./cnc job status <id>   # one job
./cnc worker list       # connected workers
```

---

## Execution Modes

### File mode (`exec_mode: "file"`)

The command uses `{input}` and `{output}` placeholders. The server replaces them with the actual chunk file path and a pre-determined output path before sending the task to a worker.

```
"command": "nmap -iL {input} -oN {output}"
"command": "wc -l {input} > {output}"
"command": "python3 /scripts/parse.py --in {input} --out {output}"
```

The worker runs `sh -c <rendered command>`. The output file is written at the path the server specified.

### Pipe mode (`exec_mode: "pipe"`)

The command receives the input chunk via **stdin** and its stdout is captured as the output. No placeholders needed.

```
"command": "sort"
"command": "grep '\\.com$'"
"command": "awk '{print $1}'"
"command": "python3 /scripts/process.py"
```

The worker pipes the chunk file into the command's stdin and writes stdout to an output file.

---

## CLI Reference

```
cnc [--server http://host:8080] <command>

Commands:
  server start              Start the CNC server (blocking)
  worker start              Start a worker agent (blocking)
  worker list               List connected workers
  job submit                Submit a job interactively
  job list                  List all jobs
  job status <job-id>       Get detailed status of a job
  status                    Show cluster-wide stats

Flags:
  --server string   Server HTTP address (default "http://localhost:8080")
```

---

## HTTP API Reference

| Method   | Path               | Description                        |
|----------|--------------------|------------------------------------|
| `POST`   | `/api/jobs`        | Submit a new job                   |
| `GET`    | `/api/jobs`        | List all jobs                      |
| `GET`    | `/api/jobs/{id}`   | Get a specific job                 |
| `DELETE` | `/api/jobs/{id}`   | Cancel a job                       |
| `GET`    | `/api/workers`     | List connected workers             |
| `GET`    | `/api/tasks`       | List all tasks                     |
| `GET`    | `/api/stats`       | Cluster statistics                 |

### POST /api/jobs

```json
{
  "name":            "job name",
  "command":         "shell command with optional {input} and {output}",
  "exec_mode":       "file",
  "input_file":      "/absolute/path/to/input.txt",
  "output_dir":      "/absolute/path/to/output",
  "split_size":      10485760,
  "timeout_seconds": 300
}
```

| Field              | Type   | Required | Default    | Description                                     |
|--------------------|--------|----------|------------|-------------------------------------------------|
| `name`             | string | yes      | —          | Human-readable job name                         |
| `command`          | string | yes      | —          | Shell command; use `{input}` / `{output}` for file mode |
| `exec_mode`        | string | no       | `"file"`   | `"file"` or `"pipe"`                            |
| `input_file`       | string | yes      | —          | Path to the large input file to be split        |
| `output_dir`       | string | yes      | —          | Directory where output files are written        |
| `split_size`       | int    | no       | 10485760   | Bytes per chunk (split is line-aware)           |
| `timeout_seconds`  | int    | no       | 300        | Subprocess timeout per task                     |

**Response:**
```json
{ "status": "ok", "job_id": "job_1234567890_1" }
```

---

## Configuration

### server_config.json

```json
{
  "http_addr":     ":8080",
  "tcp_addr":      ":9090",
  "data_dir":      "./cnc_data",
  "max_retries":   3,
  "heartbeat_ttl": "30s"
}
```

| Field           | Description                                              |
|-----------------|----------------------------------------------------------|
| `http_addr`     | HTTP API listen address                                  |
| `tcp_addr`      | TCP worker connection listen address                     |
| `data_dir`      | Directory where the server stores split chunk files      |
| `max_retries`   | How many times a failed task is retried                  |
| `heartbeat_ttl` | How long before a silent worker is marked offline        |

### worker_config.json

```json
{
  "server_addr": "192.168.1.10:9090",
  "worker_id":   "",
  "max_tasks":   8,
  "data_dir":    "./worker_data"
}
```

| Field         | Description                                                     |
|---------------|-----------------------------------------------------------------|
| `server_addr` | Server TCP address the worker connects to                       |
| `worker_id`   | Unique worker name; auto-generated from hostname+PID if empty   |
| `max_tasks`   | Maximum concurrent tasks this worker will accept                |
| `data_dir`    | Directory for temporary worker output files                     |

---

## Deploying Workers on Remote Linux Servers

Build the Linux binaries on your Mac:
```bash
make build-linux
# produces: cnc-server-linux, cnc-worker-linux, cnc-linux
```

Copy to the remote server:
```bash
scp cnc-worker-linux user@remote:/opt/cnc/cnc-worker
scp worker_config.json user@remote:/opt/cnc/worker_config.json
```

On the remote server, edit `worker_config.json` to point `server_addr` at your server machine's IP, then run:
```bash
chmod +x /opt/cnc/cnc-worker
/opt/cnc/cnc-worker -config /opt/cnc/worker_config.json
```

To run as a systemd service, create `/etc/systemd/system/cnc-worker.service`:
```ini
[Unit]
Description=CNC Worker
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/cnc
ExecStart=/opt/cnc/cnc-worker -config /opt/cnc/worker_config.json
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now cnc-worker
```

---

## Job and Task Lifecycle

```
Job status:   pending → running → completed | failed | cancelled
Task status:  pending → assigned → running → completed | failed
```

Failed tasks are automatically retried up to `max_retries` times. If a worker goes offline mid-task, its tasks are requeued for another worker.

---

## Examples

**Resolve domains and extract IP prefixes:**
```json
{
  "name": "resolve-domains",
  "command": "python3 /scripts/resolve.py {input} {output}",
  "exec_mode": "file",
  "input_file": "/data/domains.txt",
  "output_dir": "/data/ips"
}
```

**Port scan a list of IPs using nmap:**
```json
{
  "name": "nmap-scan",
  "command": "nmap -iL {input} -p 80,443,8080 -oN {output} --open",
  "exec_mode": "file",
  "input_file": "/data/ips.txt",
  "output_dir": "/data/scan_results",
  "timeout_seconds": 600
}
```

**Filter lines from a large file:**
```json
{
  "name": "filter-coms",
  "command": "grep '\\.com$'",
  "exec_mode": "pipe",
  "input_file": "/data/domains.txt",
  "output_dir": "/data/filtered"
}
```

**Run a custom Python script on each chunk:**
```json
{
  "name": "custom-parse",
  "command": "python3 /opt/scripts/parse.py",
  "exec_mode": "pipe",
  "input_file": "/data/raw.txt",
  "output_dir": "/data/parsed",
  "split_size": 5242880
}
```
