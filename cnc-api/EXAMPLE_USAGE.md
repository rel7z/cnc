# CNC Server with Google Drive - Example Usage

## Quick Start Example

This example shows how to set up workers that send scan results directly to Google Drive.

### 1. Setup Server with Google Drive

```bash
# Start server with Google Drive enabled
./cnc-server
```

The server will:
- Watch `./upload_queue` folder
- Upload new files to Google Drive every 30 seconds
- Deduplicate based on file hash
- Keep state in `./upload_queue/.gdrive_state.json`

### 2. Start Workers

On multiple machines:

```bash
# Worker 1
./cnc-worker --server 192.168.1.100:9090

# Worker 2
./cnc-worker --server 192.168.1.100:9090

# Worker 3
./cnc-worker --server 192.168.1.100:9090
```

### 3. Example: Distributed Port Scanning

**Scenario**: Scan multiple targets and automatically upload results to Google Drive

#### Option A: Using Spread Mode (File Distribution)

```bash
# Create target list
cat > targets.txt <<EOF
192.168.1.0/24
10.0.0.0/24
172.16.0.0/24
EOF

# Distribute and scan (results go to workers' local storage)
./cnc spread targets.txt "nmap -sV -iL {input}"
```

#### Option B: Using Broadcast Mode (Same Command Everywhere)

```bash
# Each worker scans from their location, saves to upload queue
./cnc broadcast "nmap -sV 8.8.8.8 -oN /path/to/server/upload_queue/scan_\$(hostname)_\$(date +%s).txt"
```

Workers will save directly to the server's `upload_queue` folder (assuming NFS mount or shared filesystem).

### 4. Watch Uploads

Monitor the server logs:

```
[gdrive] Started watching ./upload_queue (scanning every 30s)
[gdrive] ✓ Uploaded: scan_worker1_1734567890.txt (hash: a3f2c4d8b1e7...)
[gdrive] ✓ Uploaded: scan_worker2_1734567891.txt (hash: b9e1a5c3d2f6...)
[gdrive] ✓ Uploaded: scan_worker3_1734567892.txt (hash: c7d4e1f2a8b3...)
```

### 5. Deduplication Test

```bash
# Copy same file twice
cp scan_result.txt upload_queue/scan1.txt
sleep 35  # Wait for upload

cp scan_result.txt upload_queue/scan2.txt
sleep 35  # Will be skipped - same hash

# Logs will show:
# [gdrive] ✓ Uploaded: scan1.txt (hash: abc123...)
# [gdrive] Skipped duplicate: scan2.txt (hash: abc123...)
```

## Real-World Scenario: Multi-Site Vulnerability Scanning

### Setup

1. **Central Server** - Runs CNC server with Google Drive enabled
2. **Site Workers** - Worker agents at different locations
3. **Upload Queue** - Shared NFS mount at `/mnt/cnc_upload`

### Configuration

Server config (`server_config.json`):
```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "gdrive": {
    "enabled": true,
    "watch_dir": "/mnt/cnc_upload",
    "upload_interval": "60s",
    "parent_folder_id": "1A2B3C4D5E6F7G8H9I0J",
    "delete_after_upload": true
  }
}
```

### Workflow

```bash
# 1. All sites update their systems
./cnc broadcast "apt-get update && apt-get upgrade -y"

# 2. Each site runs vulnerability scan and saves to upload queue
./cnc broadcast "nessus-cli scan --target localhost --output /mnt/cnc_upload/vuln_\$(hostname).xml"

# 3. Server automatically:
#    - Detects new files in /mnt/cnc_upload
#    - Uploads to Google Drive folder
#    - Deletes local files
#    - Deduplicates if same scan runs twice

# 4. View all results in Google Drive
#    - vuln_site1.xml
#    - vuln_site2.xml
#    - vuln_site3.xml
```

## Without Shared Filesystem

If workers can't write directly to the server's upload queue:

### Option 1: Workers HTTP Upload (Future Feature)

```bash
# Workers could POST results to server
./cnc broadcast "nmap -sV target.com -oN - | curl -X POST --data-binary @- http://server:8080/api/upload"
```

### Option 2: Manual Collection

```bash
# Workers save locally
./cnc broadcast "nmap -sV target.com -oN ~/scan_result.txt"

# Manually copy to upload queue
scp worker1:~/scan_result.txt ./upload_queue/worker1_scan.txt
scp worker2:~/scan_result.txt ./upload_queue/worker2_scan.txt
```

## Monitoring Uploaded Files

Check what's been uploaded:

```bash
# View state file
cat upload_queue/.gdrive_state.json

# Output:
[
  "a3f2c4d8b1e7...",  # SHA256 hashes of uploaded files
  "b9e1a5c3d2f6...",
  "c7d4e1f2a8b3..."
]
```

Check Google Drive:
- Go to your Drive folder
- All scans appear with their original filenames
- Duplicates are prevented automatically

## Clean Slate

To start fresh:

```bash
# Delete state file
rm upload_queue/.gdrive_state.json

# Delete token (will require re-authentication)
rm token.json

# Restart server
./cnc-server
```

## Tips

1. **Use descriptive filenames** - Include hostname, timestamp, and scan type
   ```bash
   nmap ... -oN "scan_$(hostname)_$(date +%Y%m%d_%H%M%S).txt"
   ```

2. **Compress large results** - Save space in Google Drive
   ```bash
   nmap ... | gzip > scan.txt.gz
   ```

3. **Organize by date** - Use parent_folder_id to upload to dated folders

4. **Monitor disk space** - If `delete_after_upload` is false, files accumulate

5. **Test uploads manually first**
   ```bash
   echo "test" > upload_queue/test.txt
   # Watch logs for upload confirmation
   ```
