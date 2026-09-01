# CNC Documentation Index

Panduan lengkap untuk semua dokumentasi CNC system.

## 📚 Documentation Files

### For Users

1. **[README.md](README.md)** - Main Documentation
   - Overview dan fitur
   - Installation guide
   - Configuration
   - Job types (domain resolve, IP scan)
   - API reference
   - Deployment guide
   - Troubleshooting
   - **START HERE for new users**

2. **[QUICKSTART.md](QUICKSTART.md)** - Quick Start Guide
   - 5-minute setup
   - Basic usage examples
   - Common tasks
   - Quick reference
   - **START HERE if you want to try it quickly**

### For Developers

3. **[TECHNICAL.md](TECHNICAL.md)** - Technical Documentation
   - Architecture details
   - Code structure
   - Component descriptions
   - Data flow diagrams
   - Known issues dengan deep analysis
   - Future improvements
   - Development guide
   - **START HERE for developers/AI assistants**

4. **[STATUS.md](STATUS.md)** - Current Project Status
   - What's working
   - What's not working
   - Bug tracker
   - Metrics
   - Next steps
   - **CHECK THIS FIRST untuk current state**

5. **[TODO.md](TODO.md)** - Task List
   - Prioritized tasks
   - Effort estimates
   - Action items
   - Milestone planning
   - **USE THIS for planning work**

6. **[CHANGELOG.md](CHANGELOG.md)** - Version History
   - Release notes
   - Feature list
   - Breaking changes
   - Migration guide
   - **CHECK THIS for version info**

### Configuration Examples

7. **server_config.json** - Server Configuration
   ```json
   {
     "http_addr": ":8080",
     "tcp_addr": ":9090",
     "data_dir": "./cnc_data",
     "max_retries": 3,
     "heartbeat_ttl": "120s"
   }
   ```

8. **worker_config.json** - Worker Configuration
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

### Build & Test

9. **[Makefile](Makefile)** - Build Automation
   - `make build` - Build all binaries
   - `make clean` - Clean artifacts
   - `make linux-all` - Build for Linux
   - `make test` - Run tests

10. **[test_full.sh](test_full.sh)** - Integration Test
    - End-to-end test script
    - Automated testing
    - Verification

## 🗺️ Reading Guide

### If you are a **New User**:
```
1. QUICKSTART.md       (5 min) - Get started
2. README.md           (20 min) - Learn features
3. Server/Worker configs (2 min) - Configure
4. Start using!
```

### If you are a **Developer**:
```
1. STATUS.md           (10 min) - Current state
2. TECHNICAL.md        (30 min) - Architecture
3. TODO.md             (5 min) - What to work on
4. Code files          - Implementation
```

### If you are an **AI Assistant**:
```
1. STATUS.md           - Current state & issues
2. TECHNICAL.md        - Deep technical details
3. README.md           - User-facing features
4. TODO.md             - Prioritized tasks
5. Code files          - Implementation details
```

### If you want to **Deploy**:
```
1. README.md           - Deployment section
2. server_config.json  - Server setup
3. worker_config.json  - Worker setup
4. Makefile            - Build commands
```

### If you found a **Bug**:
```
1. STATUS.md           - Check known issues
2. TECHNICAL.md        - Understand system
3. Create GitHub issue
```

## 📖 Quick Reference

### Essential Commands

```bash
# Build
make build

# Run server
./cnc-server -config server_config.json

# Run worker
./cnc-worker -config worker_config.json

# Test
./test_full.sh
```

### Essential API Calls

```bash
# Stats
curl http://localhost:8080/api/stats

# Workers
curl http://localhost:8080/api/workers

# Submit job
curl -X POST http://localhost:8080/api/jobs \
  -d '{"job":{...}}'
```

### Essential Files

- `server.go` - Server implementation
- `worker.go` - Worker implementation
- `protocol.go` - Message types
- `cli.go` - CLI tool

## ⚠️ Current Status (Sep 2026)

**Overall:** FUNCTIONAL WITH ISSUES ⚠️

**Working:**
- ✅ Task execution
- ✅ File processing
- ✅ Output generation
- ✅ API endpoints

**Not Working:**
- ❌ Job completion reporting (TCP blocking issue)

**Priority #1:** Fix TCP message blocking

See [STATUS.md](STATUS.md) for details.

## 🔗 External Links

- Go Documentation: https://golang.org/doc/
- Similar Projects: Apache Spark, Dask, Ray
- Protocol: TCP, HTTP, WebSocket
- Tools: Make, Go, JSON

## 📝 Contributing

Want to contribute?

1. Read TECHNICAL.md for architecture
2. Check TODO.md for tasks
3. Pick an issue
4. Make changes
5. Update relevant docs
6. Submit PR

## 💡 Tips

**For fast learning:**
- Start with QUICKSTART.md
- Run test_full.sh to see it work
- Read logs to understand flow

**For deep understanding:**
- Read TECHNICAL.md completely
- Study the data flow section
- Review code with comments

**For maintenance:**
- Keep STATUS.md updated
- Update TODO.md as tasks complete
- Document all changes in CHANGELOG.md

## 🎯 Documentation Goals

1. **Complete** - Cover all aspects
2. **Clear** - Easy to understand
3. **Current** - Keep up to date
4. **Actionable** - Provide next steps
5. **Searchable** - Easy to find info

---

**Index maintained by:** fahrel  
**Last updated:** September 1, 2026  
**Total documents:** 10+  
**Total pages:** 100+
