# CNC Quick Start Guide

Get up and running with CNC in 5 minutes!

## 🚀 TL;DR

```bash
# Build
make build

# Terminal 1: Start server
./cnc-server

# Terminal 2: Start worker  
./cnc-worker

# Terminal 3: Submit job
echo -e "google.com\ngithub.com" > test.txt
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "job": {
      "name": "test",
      "type": "domain_resolve",
      "input_file": "test.txt",
      "output_dir": "./output",
      "split_size": 1024,
      "workers": []
    }
  }'

# Check results
ls -lh output/
cat output/resolved_*.txt
```

## 📦 Installation

### Option 1: Build from Source

```bash
cd cnc
make build
```

Binaries created:
- `./cnc-server`
- `./cnc-worker`
- `./cnc`

### Option 2: Pre-built Binaries

Download dari releases page (if available).

### Option 3: Build for Linux

```bash
make linux-all
```

Creates:
- `./cnc-server-linux`
- `./cnc-worker-linux`

## 🎯 Basic Usage

### 1. Start Server

```bash
./cnc-server
```

Output:
```
========================================
     CNC Server
========================================
HTTP API:    :8080
TCP Server:  :9090
Data Dir:    ./cnc_data
Max Retries: 3
========================================

2026/09/01 11:00:00 CNC Server starting on HTTP :8080, TCP :9090
2026/09/01 11:00:00 TCP server listening on :9090
```

### 2. Start Worker

```bash
./cnc-worker
```

Output:
```
========================================
     CNC Worker
========================================
Worker ID:     worker_hostname_12345
Server:        localhost:9090
Max Tasks:     4
Capabilities:  [domain_resolve ip_scan]
Data Dir:      ./worker_data
========================================

2026/09/01 11:00:05 Connected to server via TCP: localhost:9090
2026/09/01 11:00:05 Worker worker_hostname_12345 started
```

### 3. Verify Worker Registered

```bash
curl -s http://localhost:8080/api/workers | jq
```

Output:
```json
[
  {
    "id": "worker_hostname_12345",
    "status": "online",
    "max_tasks": 4,
    "current_load": 0
  }
]
```

### 4. Submit Job

**Create input file:**
```bash
cat > domains.txt << EOF
google.com
github.com
stackoverflow.com
reddit.com
amazon.com
EOF
```

**Submit via API:**
```bash
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "job": {
      "name": "resolve_domains",
      "type": "domain_resolve",
      "input_file": "domains.txt",
      "output_dir": "./results",
      "split_size": 1024,
      "workers": []
    }
  }'
```

Response:
```json
{
  "status": "ok",
  "job_id": "job_1234567890_1"
}
```

### 5. Monitor Progress

```bash
# Watch status
watch -n 1 'curl -s http://localhost:8080/api/stats | jq'

# Or check specific job
curl -s http://localhost:8080/api/jobs | jq
```

### 6. Check Results

```bash
# List output files
ls -lh results/

# View results
cat results/resolved_*.txt | head -20
```

## 📋 Common Tasks

### Domain Resolution

**Input:** List of domains
**Output:** IP ranges (/24 CIDR)

```bash
# Create input
echo -e "google.com\ngithub.com\nstackoverflow.com" > domains.txt

# Submit
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d @- << EOF
{
  "job": {
    "name": "resolve_domains",
    "type": "domain_resolve",
    "input_file": "domains.txt",
    "output_dir": "./domain_results",
    "split_size": 10485760,
    "workers": []
  }
}
EOF
```

### IP Scanning

**Input:** List of IP addresses
**Output:** Live hosts + port 80 status

```bash
# Create input
echo -e "8.8.8.8\n1.1.1.1\n192.168.1.1" > ips.txt

# Submit
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d @- << EOF
{
  "job": {
    "name": "scan_ips",
    "type": "ip_scan",
    "input_file": "ips.txt",
    "output_dir": "./scan_results",
    "split_size": 10485760,
    "workers": []
  }
}
EOF

# Check results
ls scan_results/
# ping_*.txt = IPs yang respond
# port80_*.txt = IPs dengan port 80 open
```

## 🔧 Configuration

### Minimal Server Config

Create `server_config.json`:
```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "max_retries": 3,
  "heartbeat_ttl": "120s"
}
```

Run:
```bash
./cnc-server -config server_config.json
```

### Minimal Worker Config

Create `worker_config.json`:
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

Run:
```bash
./cnc-worker -config worker_config.json
```

## 🌐 Multi-Server Setup

### Server Node

```bash
# server_config.json
{
  "http_addr": "0.0.0.0:8080",
  "tcp_addr": "0.0.0.0:9090",
  ...
}

./cnc-server -config server_config.json
```

### Worker Nodes (Multiple Servers)

**On each worker machine:**

```bash
# worker_config.json
{
  "server_addr": "192.168.1.100:9090",  # Server IP
  "worker_id": "worker_server1",        # Unique ID
  "max_tasks": 8,
  ...
}

./cnc-worker -config worker_config.json
```

## 📊 Monitoring

### CLI Commands

```bash
# Status
./cnc status

# Workers
./cnc workers

# Jobs
./cnc job list

# Specific job
./cnc job status job_1234567890_1
```

### HTTP API

```bash
# Cluster stats
curl http://localhost:8080/api/stats | jq

# All workers
curl http://localhost:8080/api/workers | jq

# All jobs
curl http://localhost:8080/api/jobs | jq

# All tasks
curl http://localhost:8080/api/tasks | jq
```

### Real-time Monitoring

```bash
# Watch stats
watch -n 2 'curl -s http://localhost:8080/api/stats | jq'

# Follow logs
tail -f cnc_data/*.log

# Monitor specific job
JOB_ID="job_1234567890_1"
watch -n 2 "curl -s http://localhost:8080/api/jobs | jq '.[] | select(.id==\"$JOB_ID\")'"
```

## 🐛 Troubleshooting

### Worker not connecting?

```bash
# Check server is running
curl http://localhost:8080/api/stats

# Check network
telnet localhost 9090

# Check logs
tail -f worker.log
```

### Job stuck?

```bash
# Check workers online
curl http://localhost:8080/api/workers | jq '.[].status'

# Check task status
curl http://localhost:8080/api/tasks | jq

# Restart worker
pkill cnc-worker
./cnc-worker &
```

### No output files?

```bash
# Check job status
curl http://localhost:8080/api/jobs | jq

# Check task completion
curl http://localhost:8080/api/stats | jq '.tasks_completed'

# Manually check worker output
ls -lh worker_data/
```

## 💡 Pro Tips

### 1. Optimal Split Size

```bash
# Small files (< 1MB): 
"split_size": 102400  # 100KB

# Medium files (1-100MB):
"split_size": 1048576  # 1MB

# Large files (> 100MB):
"split_size": 10485760  # 10MB
```

### 2. Worker Scaling

```bash
# More workers = faster processing
# Start multiple workers on same or different machines

# On same machine:
./cnc-worker -config worker1.json &
./cnc-worker -config worker2.json &
./cnc-worker -config worker3.json &
```

### 3. Task Concurrency

Edit worker config:
```json
{
  "max_tasks": 8  // CPU cores * 2 recommended
}
```

### 4. Background Running

```bash
# Server
nohup ./cnc-server > server.log 2>&1 &

# Worker
nohup ./cnc-worker > worker.log 2>&1 &

# Check processes
ps aux | grep cnc
```

### 5. Auto-start with Systemd

See README.md for full systemd setup.

## 📚 Next Steps

- Read [README.md](README.md) for detailed documentation
- Check [TECHNICAL.md](TECHNICAL.md) for architecture details
- Review [CHANGELOG.md](CHANGELOG.md) for version history
- Report issues on GitHub

## ❓ Quick Reference

```bash
# Build
make build

# Server
./cnc-server [-config FILE]

# Worker
./cnc-worker [-config FILE]

# Submit job
curl -X POST http://localhost:8080/api/jobs \
  -d '{"job":{...}}'

# Check status
curl http://localhost:8080/api/stats

# View workers
curl http://localhost:8080/api/workers

# List jobs
curl http://localhost:8080/api/jobs
```

---

**Need help?** Check README.md atau TECHNICAL.md  
**Found a bug?** Create GitHub issue  
**Have questions?** Read the docs or ask maintainer
