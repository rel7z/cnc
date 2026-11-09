# WordPress Discovery Tool - Aggressive Reconnaissance

**Maximum resource utilization, worldwide WordPress site discovery.**

## Overview

`wp-discovery` is an **aggressive, high-concurrency WordPress discovery engine** designed to find fresh WordPress installations across the internet. It uses multiple discovery methods simultaneously to maximize coverage and speed.

### Features

✅ **Multiple Discovery Methods**:
- **Direct Scanning**: HTTP probing of targets on multiple ports
- **Reverse DNS Lookup**: Aggressively resolve IPs to domains, scan all found domains
- **CIDR Range Scanning**: Scan entire IP ranges (e.g., `192.168.0.0/16`)
- **Certificate Transparency Logs**: Real-time discovery from CT logs (domain variations)

✅ **Maximum Performance**:
- 2000+ concurrent threads (configurable up to 5000)
- Connection pooling with keep-alive
- Buffered result channels (10k+ results in-flight)
- Parallel discovery methods (not sequential)

✅ **WordPress Detection**:
- HTTP header fingerprinting (`WordPress`, `X-Powered-By`)
- Redirect detection (302 responses)
- Path probing (`/wp-admin/`, `/wp-login.php`, `/wp-json/`)

✅ **Flexible Input**:
- Domain names
- IP addresses
- CIDR ranges (e.g., `10.0.0.0/8`)
- Mixed input files

## Installation

### Build from Source
```bash
cd cnc-backend/wp-discovery
go build -o ../../cnc-api/tools/wp-discovery main.go

# Linux version
GOOS=linux GOARCH=amd64 go build -o ../../cnc-api/tools/wp-discovery-linux main.go
```

### Pre-built Binaries
- `cnc-api/tools/wp-discovery` (macOS)
- `cnc-api/tools/wp-discovery-linux` (Linux x86_64)

## Usage

### Basic Usage
```bash
./wp-discovery -input targets.txt
```

### Aggressive Mode (Full Spectrum)
```bash
./wp-discovery \
  -input targets.txt \
  -t 3000 \
  -timeout 5 \
  -ct \
  -dns-rev \
  -cidr \
  -ports "80,443,8080,8888" \
  -out wordpress_sites.json
```

### Parameters

| Flag | Default | Description |
|------|---------|-------------|
| `-input` | (required) | Input file: domains, IPs, or CIDRs |
| `-out` | `wordpress_sites.json` | Output JSON file |
| `-t` | 2000 | Concurrent threads (1000-5000 for max aggression) |
| `-timeout` | 5 | HTTP timeout in seconds |
| `-ports` | `80,443,8080,8888,8081` | Ports to scan (comma-separated) |
| `-ct` | true | Enable Certificate Transparency log scanning |
| `-dns-rev` | true | Enable reverse DNS lookup discovery |
| `-cidr` | false | Enable CIDR range scanning (WARNING: resource intensive) |
| `-level` | `max` | Aggressiveness: `low`, `medium`, `high`, `max` |

## Input File Formats

### Example 1: Domain List
```
example.com
domain.org
site.net
```

### Example 2: IP List
```
192.168.1.1
10.0.0.1
172.16.0.1
```

### Example 3: CIDR Ranges
```
192.168.0.0/16
10.0.0.0/8
172.16.0.0/12
```

### Example 4: Mixed
```
example.com
192.168.1.0/24
10.0.0.5
wordpress.com
```

## Output

### Format
```json
[
  {"url":"http://example.com:80","ip":"93.184.216.34","port":80,"method":"direct","timestamp":"2024-11-09T17:31:00Z"},
  {"url":"http://wordpress.com:443","ip":"192.0.2.1","port":443,"method":"ct-log","timestamp":"2024-11-09T17:31:05Z"},
  ...
]
```

### Fields
- `url`: Full URL of discovered site
- `ip`: Resolved IP address
- `port`: Port number
- `method`: Discovery method (direct, reverse-dns, cidr-scan, ct-log)
- `timestamp`: Discovery time (RFC3339)

## Discovery Methods Explained

### Direct Scanning
Probes each target + port combination directly via HTTP.
- **Speed**: Fast (limited by timeout)
- **Accuracy**: High (direct response)
- **Resources**: Medium

```
Input: example.com
→ Probe http://example.com:80/wp-admin/
→ Probe http://example.com:443/wp-admin/
```

### Reverse DNS Lookup
Aggressive reverse DNS resolution to find all domains on an IP.
- **Speed**: Slow (DNS queries)
- **Accuracy**: Medium (finds all DNS records)
- **Resources**: Medium

```
Input: 192.168.1.1
→ Reverse lookup → finds "web1.example.com", "site2.example.com"
→ Probes all found domains
```

### CIDR Range Scanning
Scans entire IP ranges (WARNING: very resource intensive).
- **Speed**: Very slow (millions of IPs)
- **Accuracy**: High (exhaustive)
- **Resources**: VERY HIGH

```
Input: 10.0.0.0/24 (256 IPs)
→ Probes 10.0.0.1 - 10.0.0.254
→ Each on ports 80, 443, ...
```

### Certificate Transparency Logs
Discovers domains and subdomains from SSL certificates.
- **Speed**: Medium (CT API queries)
- **Accuracy**: High (real domains only)
- **Resources**: Medium

```
Input: example.com
→ Generates variations: www.example.com, blog.example.com, ...
→ Probes each variation
```

## Integration with CNC

### In `cnc-api/tools.go`:
```go
"wp-discovery": {
    Executable: "wp-discovery",
    Scope:      "server",    // Server-scoped (runs once, distributes results)
    DefaultMode: "broadcast", // Results broadcast to all workers
    ...
}
```

### Automated Pipeline (cnc-api/pipeline.go):
```go
// Option 1: Full Discovery + Enumeration
"wordpress-full-discovery" → wp-discovery → cms-scan → enum → wp-plugins-detector

// Option 2: Discovery Only
"wordpress-discovery-only" → wp-discovery (fast)

// Option 3: Scan Pre-discovered Sites
"wordpress-aggressive-scan" → cms-scan → enum → wp-plugins-detector
```

## Performance Tuning

### For Maximum Speed
```bash
./wp-discovery -input targets.txt -t 4000 -timeout 3 -ct -dns-rev -ports "80,443"
```

### For Maximum Coverage
```bash
./wp-discovery -input targets.txt -t 2000 -timeout 10 -cidr -ct -dns-rev -ports "80,443,8080,8888,8081"
```

### For Stealth (Slow, Lower Resource)
```bash
./wp-discovery -input targets.txt -t 100 -timeout 15 -level low
```

## Resource Requirements

### Single Machine
- **CPU**: 4+ cores (scales up to 8+ for 3000+ threads)
- **Memory**: 2-4GB
- **Network**: 1Gbps+ recommended (3000 threads ≈ 500Mbps)
- **Disk**: 100MB for output (1M results ≈ 150MB)

### Example Throughput
- **Direct scanning**: ~100-500 targets/sec
- **Reverse DNS**: ~50-200 targets/sec
- **CIDR scanning**: ~500-2000 IPs/sec (port-dependent)
- **CT logs**: ~500-1000 domains/sec

## Limitations & Safety

⚠️ **Legal Warning**: Use only on targets you own or have authorization to scan.

⚠️ **Rate Limiting**: Some targets/ISPs may rate-limit or block aggressive scanning. Use `-timeout` and `-t` judiciously.

⚠️ **DNS Load**: Reverse DNS lookups send many queries. ISPs may throttle.

⚠️ **CIDR Scanning**: Scanning large ranges (e.g., /16) can take hours and consume massive bandwidth.

## Examples

### Scan WordPress Sites in Your Domain
```bash
./wp-discovery -input my_domains.txt -t 1000 -ct -dns-rev
```

### Aggressive CIDR Scan (Use with caution!)
```bash
./wp-discovery -input cidr_ranges.txt -t 2000 -cidr -timeout 3
```

### Quick Certificate Transparency Scan
```bash
./wp-discovery -input domains.txt -t 3000 -ct -dns-rev=false -timeout 5
```

### Export for Second-Stage Scanning
```bash
./wp-discovery -input fresh_discoveries.txt -t 2000 | jq '.[] | .url' > for_cms_scan.txt
# Then feed to cms-scan, enum, wp-plugins-detector
```

## Troubleshooting

### "Connection refused" errors
- Check target is reachable
- Verify ports are open
- Increase `-timeout`

### Slow performance
- Increase `-t` (threads)
- Decrease `-timeout`
- Disable `-cidr` if not needed
- Check network bandwidth

### High memory usage
- Decrease `-t` (concurrent threads)
- Decrease result channel buffer (modify source)

### No results found
- Verify input format
- Check if targets are WordPress
- Increase `-timeout` (slow servers)
- Try different `-ports`

## License

Part of the CNC reconnaissance suite.
