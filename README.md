# CNC - Distributed Cluster Controller

A Command & Control system for distributing large workloads across multiple servers.

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────▶│  CNC Server │◀───▶│  Worker 1   │
│   (CLI)     │     │  (Coordinator)   │  (Server)   │
└─────────────┘     └─────────────┘     └─────────────┘
                           │                    │
                           ▼                    ▼
                    ┌─────────────┐     ┌─────────────┐
                    │  Worker 2   │     │  Worker N   │
                    │  (Server)   │     │  (Server)   │
                    └─────────────┘     └─────────────┘
```

## Components

1. **CNC Server** - Central coordinator that:
   - Splits large input files into chunks
   - Manages worker connections
   - Distributes tasks to available workers
   - Tracks job progress
   - Provides HTTP API and TCP interface

2. **Worker Agent** - Runs on each server:
   - Connects to CNC server
   - Executes assigned tasks (domain resolution, IP scanning)
   - Reports results back
   - Sends heartbeats

3. **CLI** - Control interface:
   - Start/stop server and workers
   - Submit and monitor jobs
   - List workers and cluster status
   - Split files locally

## Quick Start

### 1. Start the CNC Server

```bash
cd cnc
go run main.go -mode server
# Or using CLI
go run main.go -mode cli server start
```

Server listens on:
- HTTP API: `:8080`
- TCP for workers: `:9090`

### 2. Start Workers (on each server)

```bash
# On each worker machine
go run main.go -mode worker
# Or using CLI
go run main.go -mode cli worker start
```

### 3. Submit Jobs via CLI

```bash
# Submit a domain resolution job
cnc job submit

# Submit an IP scanning job
cnc job submit

# List all jobs
cnc job list

# Check job status
cnc job status <job-id>

# List workers
cnc workers

# Show cluster status
cnc status
```

## Configuration Files

### Server Config (`server_config.json`)
```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "max_retries": 3,
  "heartbeat_ttl": "30s"
}
```

### Worker Config (`worker_config.json`)
```json
{
  "server_addr": "localhost:9090",
  "worker_id": "",
  "max_tasks": 0,
  "capabilities": ["domain_resolve", "ip_scan", "file_split"],
  "data_dir": "./worker_data",
  "use_websocket": false
}
```

### CLI Config (`cnc_config.json`)
```json
{
  "server_http": "http://localhost:8080",
  "server_tcp": "localhost:9090",
  "config_file": "./cnc_config.json"
}
```

## Job Types

### Domain Resolution (`domain_resolve`)
- Input: File with domains (one per line)
- Output: IP ranges (CIDR /24) for resolved domains
- Use case: Convert domain lists to IP ranges for scanning

### IP Scanning (`ip_scan`)
- Input: File with IP addresses (one per line)
- Output: Two files - ping results and port 80 open
- Use case: Scan large IP lists for live hosts

## Distributed Processing Flow

1. Client submits job with large input file
2. CNC Server splits file into chunks (default 10MB each)
3. Creates tasks for each chunk
4. Assigns tasks to available workers
5. Workers process chunks in parallel
6. Results aggregated back to output directory
7. Client monitors progress via CLI

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /api/workers | List all workers |
| GET | /api/jobs | List all jobs |
| GET | /api/tasks | List all tasks |
| GET | /api/stats | Cluster statistics |
| POST | /api/jobs | Submit new job |
| GET | /api/jobs/:id | Get job status |
| DELETE | /api/jobs/:id | Cancel job |
| POST | /api/split | Split file locally |

## Example Workflow

```bash
# 1. Start server
cnc server start

# 2. Start 3 workers on different machines
# Machine 1: cnc worker start
# Machine 2: cnc worker start
# Machine 3: cnc worker start

# 3. Verify workers connected
cnc workers

# 4. Submit large domain list job
cnc job submit
# Enter: name=big_domains, type=domain_resolve, input=domains.txt, output=./results

# 5. Monitor progress
cnc status
cnc job status <job-id>

# 6. When done, results in ./results/
```

## Building

```bash
cd cnc
go mod tidy
go build -o cnc main.go
```

## Multi-Server Deployment

1. Copy `cnc` binary to each server
2. Copy config files to each server
3. Update `worker_config.json` on each worker with correct `server_addr`
4. Start server on coordinator machine
5. Start workers on all machines
6. Use CLI from any machine to submit jobs

## Features

- **Auto file splitting** - Large files automatically split into manageable chunks
- **Worker auto-discovery** - Workers register automatically
- **Heartbeat monitoring** - Dead workers detected automatically
- **Task retry** - Failed tasks retried up to max_retries
- **Load balancing** - Tasks distributed to least busy workers
- **Progress tracking** - Real-time job progress via CLI/API
- **Multiple transport** - TCP and WebSocket support
- **Horizontal scaling** - Add workers dynamically