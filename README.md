# CNC - Distributed Cluster Controller

Command & Control system untuk mendistribusikan workload besar ke multiple servers/workers secara paralel.

## 🎯 Fitur Utama

- **Distributed Processing**: Split file besar dan process secara paralel di multiple workers
- **Auto File Splitting**: Otomatis split file berdasarkan ukuran dengan line-aware splitting
- **Worker Auto-Discovery**: Workers otomatis register ke server
- **Heartbeat Monitoring**: Deteksi worker offline otomatis
- **Task Retry**: Automatic retry untuk failed tasks
- **Load Balancing**: Task didistribusikan ke worker yang paling tidak sibuk
- **HTTP API**: RESTful API untuk monitoring dan control
- **Real-time Progress**: Track job progress secara real-time

## 📋 Prerequisites

- Go 1.20 atau lebih baru
- Linux atau macOS
- Network connectivity antar servers (untuk distributed setup)

## 🚀 Quick Start

### 1. Build Binaries

```bash
# Clone repository
cd cnc

# Build all binaries
make build

# Atau build untuk Linux
make linux-all
```

Ini akan menghasilkan 3 binaries:
- `cnc-server` - Server koordinator
- `cnc-worker` - Worker agent
- `cnc` - CLI tool

### 2. Start Server

```bash
# Dengan default config
./cnc-server

# Atau dengan custom config
./cnc-server -config server_config.json
```

Server akan listen di:
- HTTP API: `localhost:8080`
- TCP (untuk workers): `localhost:9090`

### 3. Start Workers

Di setiap server worker:

```bash
# Dengan default config
./cnc-worker

# Atau dengan custom config
./cnc-worker -config worker_config.json
```

Worker akan otomatis connect dan register ke server.

### 4. Submit Job via CLI

```bash
# Submit job untuk domain resolution
./cnc job submit

# Lihat semua jobs
./cnc job list

# Check job status
./cnc job status <job-id>

# Lihat workers
./cnc workers

# Show cluster status
./cnc status
```

## 📝 Configuration

### Server Configuration (`server_config.json`)

```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "max_retries": 3,
  "heartbeat_ttl": "120s"
}
```

**Parameters:**
- `http_addr`: HTTP API listen address
- `tcp_addr`: TCP server untuk workers
- `data_dir`: Directory untuk data storage
- `max_retries`: Maximum retry attempts untuk failed tasks
- `heartbeat_ttl`: Worker timeout duration

### Worker Configuration (`worker_config.json`)

```json
{
  "server_addr": "localhost:9090",
  "worker_id": "worker_001",
  "max_tasks": 4,
  "capabilities": ["domain_resolve", "ip_scan"],
  "data_dir": "./worker_data",
  "use_websocket": false
}
```

**Parameters:**
- `server_addr`: Server address untuk connect
- `worker_id`: Unique worker identifier (auto-generated jika kosong)
- `max_tasks`: Maximum concurrent tasks
- `capabilities`: Task types yang bisa di-handle
- `data_dir`: Directory untuk temporary data
- `use_websocket`: Use WebSocket instead of TCP (default: false)

## 🔧 Job Types

### 1. Domain Resolution (`domain_resolve`)

Convert list domain menjadi IP ranges untuk scanning.

**Input:** File dengan domain (one per line)
```
google.com
github.com
stackoverflow.com
```

**Output:** IP ranges dalam /24 CIDR
```
142.250.185.1
142.250.185.2
...
142.250.185.255
```

**Usage:**
```bash
# Via CLI
./cnc job submit
# Name: domain_resolver
# Type: domain_resolve
# Input: domains.txt
# Output: ./results
# Split size: 10485760 (10MB)

# Via HTTP API
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "job": {
      "name": "resolve_domains",
      "type": "domain_resolve",
      "input_file": "domains.txt",
      "output_dir": "./results",
      "split_size": 10485760,
      "workers": []
    }
  }'
```

### 2. IP Scanning (`ip_scan`)

Scan list IP untuk find live hosts dan open ports.

**Input:** File dengan IP addresses (one per line)
```
8.8.8.8
1.1.1.1
192.168.1.1
```

**Output:** Dua files:
- `ping_*.txt`: IPs yang respond ke TCP ping
- `port80_*.txt`: IPs dengan port 80 terbuka

**Usage:**
```bash
# Via CLI
./cnc job submit
# Name: scan_ips
# Type: ip_scan  
# Input: ips.txt
# Output: ./scan_results
# Split size: 10485760
```

## 🌐 HTTP API Reference

### Workers

**List Workers**
```bash
GET /api/workers

Response:
[
  {
    "id": "worker_001",
    "address": "localhost:9090",
    "status": "online",
    "capabilities": ["domain_resolve", "ip_scan"],
    "max_tasks": 4,
    "current_load": 0,
    "last_seen": "2026-09-01T11:30:00Z",
    "registered": "2026-09-01T11:00:00Z"
  }
]
```

### Jobs

**List Jobs**
```bash
GET /api/jobs

Response:
[
  {
    "id": "job_1234_1",
    "name": "test_job",
    "type": "domain_resolve",
    "status": "running",
    "total_tasks": 10,
    "completed": 5,
    "failed": 0,
    "created_at": "2026-09-01T11:00:00Z"
  }
]
```

**Submit Job**
```bash
POST /api/jobs
Content-Type: application/json

{
  "job": {
    "name": "my_job",
    "type": "domain_resolve",
    "input_file": "/path/to/input.txt",
    "output_dir": "/path/to/output",
    "split_size": 10485760,
    "workers": []
  }
}

Response:
{
  "status": "ok",
  "job_id": "job_1234_1"
}
```

**Get Job Status**
```bash
GET /api/jobs/{job_id}

Response:
{
  "id": "job_1234_1",
  "name": "my_job",
  "status": "completed",
  "total_tasks": 10,
  "completed": 10,
  "failed": 0,
  ...
}
```

### Tasks

**List Tasks**
```bash
GET /api/tasks

Response:
[
  {
    "id": "task_job_1234_1_0",
    "job_id": "job_1234_1",
    "type": "domain_resolve",
    "status": "completed",
    "assigned_to": "worker_001",
    "created_at": "2026-09-01T11:00:00Z",
    "completed_at": "2026-09-01T11:01:00Z"
  }
]
```

### Statistics

**Get Cluster Statistics**
```bash
GET /api/stats

Response:
{
  "workers_total": 3,
  "workers_online": 3,
  "jobs_total": 5,
  "jobs_running": 1,
  "tasks_total": 50,
  "tasks_pending": 10,
  "tasks_running": 20,
  "tasks_completed": 15,
  "tasks_failed": 5
}
```

## 🏗️ Architecture

```
┌─────────────┐     ┌─────────────────┐     ┌─────────────┐
│   Client    │────▶│   CNC Server    │◀───▶│  Worker 1   │
│   (CLI)     │     │  (Coordinator)  │     │  (Executor) │
└─────────────┘     └─────────────────┘     └─────────────┘
                           │ ▲                      │
                           │ │                      │
                           ▼ │              ┌───────┴───────┐
                    ┌─────────────┐         │               │
                    │  Worker 2   │   ┌─────▼─────┐ ┌──────▼──────┐
                    │  (Executor) │   │ Worker 3  │ │  Worker N   │
                    └─────────────┘   └───────────┘ └─────────────┘
```

### Components

1. **CNC Server**: Koordinator pusat
   - Menerima job submissions
   - Split input files
   - Manage worker registrations
   - Distribute tasks ke workers
   - Track progress
   - Provide HTTP API

2. **Worker Agent**: Task executor
   - Register ke server
   - Kirim heartbeats
   - Execute tasks
   - Report hasil
   - Auto-reconnect jika disconnect

3. **CLI**: Command-line interface
   - Submit jobs
   - Monitor progress
   - Manage cluster

## 🔄 Workflow

1. **Job Submission**
   - User submit job via CLI atau API
   - Server receive dan validate job

2. **File Splitting**
   - Server split input file menjadi chunks
   - Each chunk = 1 task
   - Line-aware splitting (tidak potong di tengah line)

3. **Task Distribution**
   - Server queue semua tasks
   - Dispatcher assign tasks ke available workers
   - Load balancing otomatis

4. **Task Execution**
   - Worker execute task
   - Generate output file
   - Report result ke server

5. **Job Completion**
   - Server track semua task completions
   - Update job status
   - Retry failed tasks (up to max_retries)

## 🚀 Deployment

### Single Server (Local Testing)

```bash
# Terminal 1: Start server
./cnc-server

# Terminal 2: Start worker
./cnc-worker

# Terminal 3: Submit job
./cnc job submit
```

### Multi-Server (Production)

**Server Node:**
```bash
# On coordinator machine
./cnc-server -config server_config.json
```

**Worker Nodes:**
```bash
# On each worker machine
# Edit worker_config.json:
# "server_addr": "<server-ip>:9090"

./cnc-worker -config worker_config.json
```

### Using Systemd (Linux)

**Server Service** (`/etc/systemd/system/cnc-server.service`):
```ini
[Unit]
Description=CNC Server
After=network.target

[Service]
Type=simple
User=cnc
WorkingDirectory=/opt/cnc
ExecStart=/opt/cnc/cnc-server -config /opt/cnc/server_config.json
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

**Worker Service** (`/etc/systemd/system/cnc-worker.service`):
```ini
[Unit]
Description=CNC Worker
After=network.target

[Service]
Type=simple
User=cnc
WorkingDirectory=/opt/cnc
ExecStart=/opt/cnc/cnc-worker -config /opt/cnc/worker_config.json
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

**Enable and Start:**
```bash
sudo systemctl daemon-reload
sudo systemctl enable cnc-server
sudo systemctl start cnc-server

sudo systemctl enable cnc-worker
sudo systemctl start cnc-worker

# Check status
sudo systemctl status cnc-server
sudo systemctl status cnc-worker

# View logs
sudo journalctl -u cnc-server -f
sudo journalctl -u cnc-worker -f
```

## 📊 Monitoring

### CLI Monitoring

```bash
# Watch cluster stats
watch -n 2 './cnc status'

# Monitor specific job
watch -n 2 './cnc job status job_1234_1'

# Watch worker list
watch -n 2 './cnc workers'
```

### Log Monitoring

```bash
# Server logs
tail -f /var/log/cnc-server.log

# Worker logs
tail -f /var/log/cnc-worker.log
```

## 🐛 Troubleshooting

### Worker Tidak Connect

**Check:**
1. Server running? `curl http://localhost:8080/api/stats`
2. Network connectivity: `telnet <server-ip> 9090`
3. Firewall: pastikan port 9090 terbuka
4. Config: check `server_addr` di worker_config.json

**Logs:**
```bash
# Check worker logs
./cnc-worker -config worker_config.json 2>&1 | tee worker.log
```

### Task Tidak Execute

**Check:**
1. Workers registered? `curl http://localhost:8080/api/workers`
2. Worker capabilities match task type?
3. Worker has available capacity? (current_load < max_tasks)

**Debug:**
```bash
# Check task queue
curl http://localhost:8080/api/tasks | jq '.[] | {id, status, assigned_to}'
```

### Job Stuck

**Possible causes:**
1. Worker offline (check heartbeat)
2. All workers busy
3. Task failing repeatedly (check max_retries)

**Fix:**
```bash
# Add more workers
./cnc-worker &

# Or cancel and resubmit job
./cnc job cancel <job-id>
```

### High Memory Usage

**Worker side:**
- Reduce `max_tasks` in worker_config.json
- Increase file `split_size` to create fewer, larger tasks

**Server side:**
- Reduce task queue size in code (DefaultTaskQueueSize)
- Increase split_size to reduce total tasks

## 📦 Building from Source

```bash
# Install dependencies
go mod download

# Build all
make build

# Build for Linux (cross-compile)
make linux-all

# Clean build artifacts
make clean

# Run tests
make test
```

## 🤝 Contributing

Contributions welcome! Please:
1. Fork repository
2. Create feature branch
3. Make changes
4. Test thoroughly
5. Submit pull request

## 📄 License

MIT License - see LICENSE file

## 🔗 Related Projects

- Similar systems: Apache Spark, Dask, Ray
- Job queues: RabbitMQ, Redis Queue, Bull
- Distributed computing: Kubernetes Jobs, AWS Batch

## 📞 Support

- Issues: GitHub Issues
- Documentation: README.md (this file)
- Technical docs: TECHNICAL.md

---

**Version:** 1.0.0  
**Last Updated:** September 2026  
**Author:** fahrel
