# CNC - Command & Control Platform

A distributed command-and-control platform for executing tasks across multiple workers with built-in Google Drive integration for automatic result collection.

## Features

### Core C2 Capabilities
- **Spread Mode** - Distribute file chunks across workers for parallel processing
- **Broadcast Mode** - Execute same command on all workers simultaneously
- **Real-time Dashboard** - Monitor workers and jobs via web UI
- **Live Updates** - Server-Sent Events (SSE) for real-time status
- **Task Management** - Queue, dispatch, and track tasks automatically

### Google Drive Integration
- **Automatic File Upload** - Watch folder and upload to Google Drive
- **Deduplication** - SHA256-based duplicate detection
- **Persistent State** - Remembers uploaded files across restarts
- **Configurable Cleanup** - Optional local file deletion after upload
- **Resume Support** - Handles network interruptions gracefully

## Architecture

```
┌──────────────┐
│  CNC Server  │  ← Central command server
│              │  ← Watches folder for uploads
│              │  ← Manages job distribution
└───────┬──────┘
        │
        ├─────────────┐
        │             │
   ┌────▼─────┐  ┌───▼──────┐
   │ Worker 1 │  │ Worker 2 │  ← Execute tasks
   └──────────┘  └──────────┘
```

## Quick Start

### 1. Build

```bash
make build
```

Produces:
- `cnc-server` - Command & control server
- `cnc-worker` - Worker agent
- `cnc` - CLI tool

### 2. Start Server

```bash
./cnc-server
```

Starts:
- HTTP API on `:8080`
- TCP listener on `:9090` (for workers)
- Optional: Google Drive uploader (if enabled)

### 3. Start Workers

On each worker machine:

```bash
./cnc-worker --server <server-ip>:9090
```

### 4. Submit Jobs

**Broadcast Mode** (same command everywhere):
```bash
./cnc broadcast "hostname && uptime"
```

**Spread Mode** (distribute file):
```bash
./cnc spread targets.txt "nmap -sV -iL {input}"
```

## Installation

### Prerequisites

- Go 1.21+ (for building)
- Network connectivity between server and workers

### Build from Source

```bash
git clone https://github.com/fahrel/cnc
cd cnc/cnc-api
make build
```

### Configuration

**Server** (`server_config.json`):
```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "max_retries": 3,
  "heartbeat_ttl": "30s",
  "gdrive": {
    "enabled": false
  }
}
```

**Worker** (`worker_config.json`):
```json
{
  "server_addr": "localhost:9090",
  "worker_id": "",
  "max_tasks": 8,
  "data_dir": "./worker_data",
  "heartbeat_interval": "10s"
}
```

## Usage Examples

### Example 1: System Inventory

Collect system info from all workers:

```bash
./cnc broadcast "uname -a > /upload_queue/system_\$(hostname).txt"
```

Results automatically upload to Google Drive (if enabled).

### Example 2: Distributed Port Scanning

Scan multiple networks in parallel:

```bash
# Create target file
cat > targets.txt <<EOF
192.168.1.0/24
10.0.0.0/24
172.16.0.0/24
EOF

# Distribute across workers
./cnc spread targets.txt "nmap -sV -iL {input}"
```

Each worker scans their assigned chunk concurrently.

### Example 3: Software Updates

Update all workers simultaneously:

```bash
./cnc broadcast "apt-get update && apt-get upgrade -y"
```

### Example 4: Log Collection

Collect logs from all sites:

```bash
./cnc broadcast "journalctl --since='1 hour ago' > /upload_queue/logs_\$(hostname).txt"
```

## Google Drive Setup

To enable automatic file uploads to Google Drive:

1. **Create Google Cloud Project**
   - Enable Google Drive API
   - Create OAuth credentials (Desktop app)
   - Download `credentials.json`

2. **Configure Server**
   ```json
   "gdrive": {
     "enabled": true,
     "watch_dir": "./upload_queue",
     "credentials_path": "./credentials.json",
     "upload_interval": "30s",
     "delete_after_upload": false
   }
   ```

3. **First Run Authentication**
   - Server will print OAuth URL
   - Visit URL and authorize
   - Paste code back to terminal

See [GDRIVE_SETUP.md](GDRIVE_SETUP.md) for detailed instructions.

## Web Dashboard

Access at `http://server-ip:8080/dashboard`

Features:
- View online workers by IP
- Submit broadcast/spread jobs
- Live status updates
- Job history and results

## CLI Reference

### Server Management

```bash
./cnc server start              # Start server
```

### Worker Management

```bash
./cnc worker start              # Start worker
./cnc worker list               # List all workers
```

### Job Operations

```bash
./cnc spread <file> <command>   # Distribute file processing
./cnc broadcast <command>       # Run command on all workers
./cnc job list                  # List all jobs
./cnc job status <id>           # Get job details
```

### System Status

```bash
./cnc status                    # Show server statistics
```

## Configuration Options

### Server

| Option | Description | Default |
|--------|-------------|---------|
| `http_addr` | HTTP API address | `:8080` |
| `tcp_addr` | Worker TCP address | `:9090` |
| `data_dir` | Data storage directory | `./cnc_data` |
| `max_retries` | Task retry limit | `3` |
| `heartbeat_ttl` | Worker timeout | `30s` |

### Google Drive

| Option | Description | Default |
|--------|-------------|---------|
| `enabled` | Enable/disable integration | `false` |
| `watch_dir` | Directory to monitor | `./upload_queue` |
| `credentials_path` | OAuth credentials file | `./credentials.json` |
| `upload_interval` | Scan frequency | `30s` |
| `parent_folder_id` | Drive folder ID | `""` (root) |
| `delete_after_upload` | Remove after upload | `false` |

### Worker

| Option | Description | Default |
|--------|-------------|---------|
| `server_addr` | Server TCP address | `localhost:9090` |
| `worker_id` | Unique worker ID | Auto-generated |
| `max_tasks` | Concurrent task limit | `8` |
| `heartbeat_interval` | Status ping frequency | `10s` |

## API Endpoints

### HTTP API

- `GET /api/workers` - List workers
- `GET /api/jobs` - List jobs
- `POST /api/jobs` - Submit new job
- `GET /api/jobs/{id}` - Job details
- `GET /api/jobs/{id}/tasks` - Job tasks
- `GET /api/stats` - Server statistics
- `GET /api/events` - SSE stream

### Job Submission

```bash
curl -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "broadcast",
    "command": "hostname",
    "timeout_seconds": 60
  }'
```

## Architecture Details

### Spread Mode
- Splits input file into N chunks
- Distributes chunks to available workers
- Workers process independently
- Results collected automatically

### Broadcast Mode
- Creates one task per online worker
- Pre-assigns tasks to specific workers
- All execute the same command
- Runs simultaneously across all workers

### File Watching
- Scans directory at configured interval
- Computes SHA256 for each file
- Checks against upload history
- Uploads new/modified files only
- Persists state to JSON file

## Security Considerations

- **No authentication** - Run behind firewall/VPN
- **Plain TCP** - No encryption (use WireGuard/VPN)
- **Shared filesystem** - Workers need access to input files (spread mode)
- **OAuth tokens** - Keep `credentials.json` and `token.json` private
- **Command injection** - Validate commands before submission

## Performance

- **Worker capacity**: Configurable via `max_tasks` (default: 8)
- **Task queue**: 10,000 task buffer
- **File splitting**: 10MB chunks by default
- **Heartbeat**: Workers checked every 10s
- **Upload scan**: Configurable interval (default: 30s)

## Troubleshooting

### Workers Not Connecting

```bash
# Check server is listening
netstat -an | grep 9090

# Test connectivity
telnet server-ip 9090

# Check firewall
iptables -L | grep 9090
```

### Google Drive Not Uploading

```bash
# Check logs
./cnc-server 2>&1 | grep gdrive

# Verify credentials
ls -la credentials.json

# Test OAuth token
rm token.json && ./cnc-server  # Re-authenticate
```

### Jobs Stuck

```bash
# Check worker status
./cnc worker list

# View job details
./cnc job status job_<id>

# Check server logs
tail -f /var/log/cnc-server.log
```

## Development

### Project Structure

```
cnc-api/
├── cmd/
│   ├── server/main.go    # Server entrypoint
│   ├── worker/main.go    # Worker entrypoint
│   └── cnc/main.go       # CLI entrypoint
├── protocol.go           # Core types
├── server.go             # Server implementation
├── worker.go             # Worker implementation
├── gdrive.go             # Google Drive integration
├── cli.go                # CLI commands
└── Makefile              # Build configuration
```

### Building

```bash
make build          # Local OS
make build-linux    # Linux (static)
make clean          # Remove binaries
```

### Testing

```bash
# Start server
./cnc-server &

# Start worker
./cnc-worker &

# Run test job
./cnc broadcast "echo test"

# Cleanup
pkill cnc-server cnc-worker
```

## Documentation

- [TECHNICAL.md](TECHNICAL.md) - Architecture and implementation details
- [GDRIVE_SETUP.md](GDRIVE_SETUP.md) - Google Drive integration guide
- [EXAMPLE_USAGE.md](EXAMPLE_USAGE.md) - Real-world usage scenarios

## License

MIT

## Contributing

Contributions welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

## Support

For issues and questions:
- GitHub Issues: https://github.com/fahrel/cnc/issues
- Email: support@example.com

---

**Built with Go. Powered by simplicity.**
