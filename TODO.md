# CNC TODO List

Priority-sorted list of tasks untuk improve CNC system.

## 🔥 CRITICAL (Do First)

### [ ] Fix TCP Message Blocking
**Priority:** P0 (CRITICAL)  
**Effort:** 2-4 hours  
**Impact:** HIGH - Makes jobs complete properly

**Options:**
1. ⭐ Use HTTP POST for task results (RECOMMENDED)
   - Simplest solution
   - Proven HTTP stack
   - Clear separation
   
2. Switch to WebSocket
   - Better async support
   - More complex
   
3. Use separate TCP connections
   - One for heartbeat, one for results
   
**Action Items:**
- [ ] Implement HTTP POST endpoint `/api/task-result`
- [ ] Modify worker to POST results
- [ ] Test with multiple workers
- [ ] Update documentation

---

## 🔴 HIGH Priority

### [ ] Result File Aggregation
**Priority:** P1 (HIGH)  
**Effort:** 4-6 hours  
**Impact:** HIGH - User convenience

**Requirements:**
- Merge split output files
- Single output per job
- Preserve order
- Show progress

**Action Items:**
- [ ] Design aggregation strategy
- [ ] Implement merge function
- [ ] Add to job completion
- [ ] Test with large files

### [ ] Add Basic Authentication
**Priority:** P1 (HIGH)  
**Effort:** 6-8 hours  
**Impact:** HIGH - Security

**Requirements:**
- Worker registration tokens
- API key authentication
- Config-based secrets
- No external dependencies

**Action Items:**
- [ ] Design auth scheme
- [ ] Implement token validation
- [ ] Add to worker registration
- [ ] Add to API endpoints
- [ ] Update configs
- [ ] Document auth flow

### [ ] Add Persistence Layer
**Priority:** P1 (HIGH)  
**Effort:** 8-12 hours  
**Impact:** HIGH - Reliability

**Requirements:**
- Save job/task state
- Resume after restart
- Job history
- SQLite for simplicity

**Action Items:**
- [ ] Design database schema
- [ ] Implement DB layer
- [ ] Add save/load functions
- [ ] Test persistence
- [ ] Migration support

---

## 🟡 MEDIUM Priority

### [ ] Write Unit Tests
**Priority:** P2 (MEDIUM)  
**Effort:** 8-16 hours  
**Impact:** MEDIUM - Code quality

**Coverage Target:** 60%

**Action Items:**
- [ ] Test file splitting
- [ ] Test task execution
- [ ] Test message encoding
- [ ] Test error handling
- [ ] Mock dependencies
- [ ] CI integration

### [ ] Improve Error Messages
**Priority:** P2 (MEDIUM)  
**Effort:** 4-6 hours  
**Impact:** MEDIUM - User experience

**Action Items:**
- [ ] Categorize error types
- [ ] Add error codes
- [ ] Detailed error context
- [ ] User-friendly messages
- [ ] Logging improvements

### [ ] Add Metrics Export
**Priority:** P2 (MEDIUM)  
**Effort:** 6-8 hours  
**Impact:** MEDIUM - Monitoring

**Requirements:**
- Prometheus format
- Standard metrics
- Custom metrics
- HTTP endpoint

**Action Items:**
- [ ] Add prometheus client
- [ ] Define metrics
- [ ] Implement collectors
- [ ] Add /metrics endpoint
- [ ] Create Grafana dashboard

### [ ] Build Web UI
**Priority:** P2 (MEDIUM)  
**Effort:** 20-30 hours  
**Impact:** MEDIUM - User experience

**Features:**
- Dashboard overview
- Job list and status
- Worker management
- Real-time updates
- Submit jobs

**Action Items:**
- [ ] Choose framework (React?)
- [ ] Design UI mockups
- [ ] Implement frontend
- [ ] WebSocket for real-time
- [ ] Deploy strategy

---

## 🟢 LOW Priority

### [ ] Add Docker Support
**Priority:** P3 (LOW)  
**Effort:** 4-6 hours  
**Impact:** LOW - Deployment

**Action Items:**
- [ ] Create Dockerfile (server)
- [ ] Create Dockerfile (worker)
- [ ] Docker Compose setup
- [ ] Multi-stage builds
- [ ] Documentation

### [ ] Kubernetes Deployment
**Priority:** P3 (LOW)  
**Effort:** 8-12 hours  
**Impact:** LOW - Scalability

**Action Items:**
- [ ] Create K8s manifests
- [ ] Helm chart
- [ ] StatefulSet for server
- [ ] Deployment for workers
- [ ] Service definitions
- [ ] ConfigMaps

### [ ] Add More Task Types
**Priority:** P3 (LOW)  
**Effort:** 4-8 hours per type  
**Impact:** LOW - Features

**Candidates:**
- [ ] HTTP request task
- [ ] File download task
- [ ] Data transformation task
- [ ] Screenshot capture task
- [ ] Custom script execution

### [ ] Implement Task Dependencies
**Priority:** P3 (LOW)  
**Effort:** 16-24 hours  
**Impact:** LOW - Advanced features

**Requirements:**
- DAG support
- Conditional execution
- Parallel branches
- Wait conditions

---

## 🎨 Nice to Have

### [ ] CLI Improvements
- [ ] Interactive mode
- [ ] Tab completion
- [ ] Color output
- [ ] Progress bars
- [ ] Better formatting

### [ ] Configuration
- [ ] YAML support
- [ ] Environment variables
- [ ] Config validation
- [ ] Hot reload

### [ ] Logging
- [ ] Structured logging
- [ ] Log levels
- [ ] Log rotation
- [ ] Remote logging

### [ ] Performance
- [ ] Benchmark tests
- [ ] Profiling
- [ ] Optimization
- [ ] Caching

### [ ] Documentation
- [ ] API reference (OpenAPI)
- [ ] Architecture diagrams
- [ ] Video tutorials
- [ ] FAQ section

---

## 🔧 Technical Debt

### [ ] Code Cleanup
- [ ] Remove dead code
- [ ] Extract magic numbers
- [ ] Consistent naming
- [ ] Remove duplicates

### [ ] Refactoring
- [ ] Split large functions
- [ ] Reduce complexity
- [ ] Improve error handling
- [ ] Better abstractions

### [ ] Dependencies
- [ ] Update Go version
- [ ] Update packages
- [ ] Remove unused deps
- [ ] Vendor management

---

## 📅 Milestone Planning

### Version 1.1 (Target: 1-2 weeks)
- [x] Fix TCP blocking ← MUST DO FIRST
- [ ] Result aggregation
- [ ] Basic auth
- [ ] Unit tests (basic)

### Version 1.2 (Target: 1 month)
- [ ] Persistence layer
- [ ] Metrics export
- [ ] Web UI
- [ ] Docker support

### Version 2.0 (Target: 3 months)
- [ ] Task dependencies
- [ ] Advanced scheduling
- [ ] Multi-tenancy
- [ ] Cloud integration

---

## 📝 Notes

**Work Methodology:**
1. Pick highest priority unblocked task
2. Create feature branch
3. Implement with tests
4. Update documentation
5. Submit PR
6. Review and merge

**Quality Standards:**
- All features must have tests
- Documentation must be updated
- No new warnings/errors
- Follow Go best practices
- Review before merge

**Time Estimates:**
- Based on single developer
- Includes testing and docs
- May vary with complexity
- Buffer for unknowns

---

**List maintained by:** fahrel  
**Last updated:** September 1, 2026  
**Total items:** 40+  
**Completed:** 0  
**In Progress:** 0
