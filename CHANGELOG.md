# Changelog

All notable changes to CNC project.

## [1.0.0] - 2026-09-01

### ✨ Added
- Initial release of CNC distributed cluster controller
- Server coordinator dengan HTTP API dan TCP server
- Worker agent dengan task execution capabilities
- CLI tool untuk job submission dan monitoring
- Support untuk 2 task types: domain_resolve dan ip_scan
- Auto file splitting dengan line-aware algorithm
- Worker auto-discovery dan registration
- Heartbeat monitoring untuk detect offline workers
- Task retry mechanism (configurable max_retries)
- Load balancing across available workers
- RESTful HTTP API untuk external integration
- Configuration files untuk server dan worker
- Makefile untuk easy building
- Integration test script (test_full.sh)
- Complete documentation (README.md, TECHNICAL.md)

### 🏗️ Architecture
- Master-Worker pattern
- TCP communication dengan JSON messages
- HTTP REST API untuk clients
- Async task execution
- Job dan task state management
- File splitting dan chunking

### 📦 Deliverables
- `cnc-server`: Server binary
- `cnc-worker`: Worker binary
- `cnc`: CLI tool binary
- Configuration templates
- Documentation files
- Test scripts

### 🔧 Features Implemented

#### Core Features
- [x] Job submission dan tracking
- [x] Task distribution and execution
- [x] Worker registration and management
- [x] File splitting (line-aware)
- [x] Domain resolution task type
- [x] IP scanning task type
- [x] HTTP API endpoints
- [x] CLI commands
- [x] Configuration management
- [x] Logging dan monitoring

#### Worker Capabilities
- [x] TCP connection ke server
- [x] Heartbeat mechanism
- [x] Task execution
- [x] Result reporting
- [x] Auto-reconnection
- [x] Statistics tracking
- [x] Multiple task types support

#### Server Capabilities
- [x] HTTP server untuk API
- [x] TCP server untuk workers
- [x] Job management
- [x] Task queuing dan dispatching
- [x] Worker health monitoring
- [x] Task retry logic
- [x] Progress tracking
- [x] Load balancing

### ⚠️ Known Issues

#### Critical
- **TCP Blocking Issue**: Task result messages block for ~58 seconds
  - Impact: Jobs tidak complete walaupun output sudah generated
  - Workaround: Output files tetap ter-generate dengan benar
  - Status: Under investigation
  - Priority: HIGH

#### Minor
- Worker encoder storage fragility
- No result file aggregation
- Basic error messages

### 🚧 Limitations

**Security:**
- No authentication
- No encryption
- No access control
- Suitable untuk internal/trusted networks only

**Scalability:**
- In-memory state storage (tidak persistent)
- No database integration
- Limited by single server instance

**Features:**
- No task dependencies
- No job scheduling
- No web UI
- No metrics export

### 📊 Performance

**Tested Configuration:**
- Server: macOS, 16GB RAM
- Workers: 1-3 instances
- Input files: up to 10MB
- Tasks: Domain resolution (10 domains)
- Results: Successfully generated 4335 IP addresses

**Observed Metrics:**
- Task execution: 15-30 seconds
- File splitting: < 1 second
- DNS resolution: Parallel 100 goroutines
- IP scanning: 1000 concurrent workers

### 🎯 Future Roadmap

#### Version 1.1 (Next)
- [ ] Fix TCP blocking issue
- [ ] Implement result aggregation
- [ ] Add authentication
- [ ] Improve error messages
- [ ] Add unit tests

#### Version 1.2
- [ ] Database persistence
- [ ] Web UI dashboard
- [ ] Metrics export (Prometheus)
- [ ] Docker support
- [ ] Kubernetes deployment

#### Version 2.0
- [ ] Task dependencies (DAG)
- [ ] Custom task plugins
- [ ] Multi-tenancy
- [ ] Advanced scheduling
- [ ] Cloud integration

### 📝 Notes

**Development Environment:**
- Go version: 1.25.0
- Platform: macOS (darwin)
- Build tool: Make
- Testing: Manual + integration script

**Deployment:**
- Suitable untuk production dengan caveats
- Use dalam trusted network only
- Monitor logs closely
- Add external monitoring

**Migration:**
- No breaking changes expected dalam 1.x series
- Configuration format akan stable
- API endpoints backward compatible

---

## Version History

### Unreleased
- Working on TCP blocking fix
- Planning authentication implementation
- Designing result aggregation

### [1.0.0] - 2026-09-01
- Initial release
- Core functionality implemented
- Documentation complete
- Ready for testing

---

**Maintained by:** fahrel  
**Repository:** github.com/fahrel/cnc  
**License:** MIT
