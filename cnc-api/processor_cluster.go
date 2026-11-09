package cnc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/cnc-processor/cluster"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Must be called with s.mu held. Include in-flight reservations so a heartbeat
// that predates task delivery cannot make already reserved slots available.
func (s *Server) reservedLoad(id string) int {
	n := 0
	for _, t := range s.tasks {
		if t.AssignedTo == id && (t.Status == TaskStatusAssigned || t.Status == TaskStatusRunning) {
			n++
		}
	}
	return n
}
func (s *Server) workerLoad(w *Worker) int {
	n := s.reservedLoad(w.ID)
	if n < w.CurrentLoad {
		n = w.CurrentLoad
	}
	return n
}
func (s *Server) handleProcessorBroker(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("CNC_PROCESSOR_TOKEN")
	if len(token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if r.URL.Path == "/api/processor/cluster/workers" && r.Method == "GET" {
		s.mu.RLock()
		workers := []cluster.Worker{}
		for _, worker := range s.workers {
			reserved, processorLoad := s.processorReservations(worker.ID)
			workers = append(workers, cluster.Worker{ID: worker.ID, Address: worker.Address, Metadata: worker.Metadata, Workspaces: worker.ProcessorWorkspaces, Status: string(worker.Status), CurrentLoad: s.workerLoad(worker), MaxTasks: worker.MaxTasks, Protocol: worker.ProcessorProtocol, Versions: worker.ProcessorVersions, ActiveAttempts: worker.ProcessorAttempts, Resources: worker.Resources, Reservations: reserved, ProcessorLoad: processorLoad})
		}
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(workers)
		return
	}
	if r.URL.Path == "/api/processor/cluster/publish" && r.Method == "POST" {
		var publication struct {
			Name  string   `json:"name"`
			Lines []string `json:"lines"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&publication); err != nil {
			http.Error(w, "invalid publication", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "invalid publication", 400)
			return
		}
		if len(publication.Lines) > 1000 {
			http.Error(w, "too many publication lines", 400)
			return
		}
		watchDir := ""
		if s.config.GDrive != nil {
			watchDir = ExpandPath(s.config.GDrive.WatchDir)
		}
		if watchDir == "" {
			http.Error(w, "merged directory not configured", 503)
			return
		}
		watcherFileMu.Lock()
		err := cluster.MergeText(watchDir, publication.Name, publication.Lines)
		watcherFileMu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		if uploader := s.driveUploader(); uploader != nil {
			uploader.NotifyResults()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"merged":true}`))
		return
	}
	if r.URL.Path != "/api/processor/cluster/assign" || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		WorkerID   string              `json:"worker_id"`
		Assignment *cluster.Assignment `json:"assignment"`
		Install    *cluster.Install    `json:"install"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if err := d.Decode(&request); err != nil || (request.Assignment == nil) == (request.Install == nil) {
		http.Error(w, "invalid assignment", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	worker := s.workers[request.WorkerID]
	if worker == nil || worker.SendCh == nil || worker.Status == WorkerStatusOffline || worker.ProcessorProtocol != cluster.ProtocolVersion {
		http.Error(w, "worker unavailable", 409)
		return
	}
	id := fmt.Sprintf("processor-install-%s-%d", worker.ID, time.Now().UnixNano())
	taskType := "processor_install"
	if a := request.Assignment; a != nil {
		id = a.AttemptID
		taskType = "processor"
		if a.WorkerID != worker.ID || a.AttemptID == "" {
			http.Error(w, "invalid worker assignment", 400)
			return
		}
		if old := s.tasks[id]; old != nil {
			if old.AssignedTo != worker.ID {
				http.Error(w, "assignment conflict", 409)
				return
			}
			w.Write([]byte(`{"accepted":true}`))
			return
		}
		if worker.Metadata["processor_workspace_root"] != "" {
			group, e := cluster.WorkspaceName(a.ProgramID)
			if e != nil {
				http.Error(w, "invalid program workspace", 400)
				return
			}
			for _, active := range worker.ProcessorWorkspaces {
				if active == group {
					http.Error(w, "waiting for the program workspace to become idle", 409)
					return
				}
			}
			for _, current := range s.tasks {
				if current.AssignedTo != worker.ID || current.ProcessorAssignment == nil || (current.Status != TaskStatusAssigned && current.Status != TaskStatusRunning) {
					continue
				}
				other, _ := cluster.WorkspaceName(current.ProcessorAssignment.ProgramID)
				if other == group {
					http.Error(w, "waiting for the program workspace to become idle", 409)
					return
				}
			}
		}
		ready := false
		for _, v := range worker.ProcessorVersions {
			if v == a.Version {
				ready = true
			}
		}
		if !ready {
			http.Error(w, "program version unavailable", 409)
			return
		}

	}
	reserved, processorLoad := s.processorReservations(worker.ID)
	resources := cluster.Program{}.Resources()
	limits := cluster.WorkerLimits{}
	if request.Assignment != nil {
		resources = request.Assignment.Resources
		if resources.CPUCores <= 0 || resources.MemoryMB <= 0 {
			resources = cluster.Program{}.Resources()
		}
		limits = request.Assignment.WorkerLimits
	} else {
		limits = request.Install.WorkerLimits
	}
	snapshot := cluster.Worker{MaxTasks: worker.MaxTasks, CurrentLoad: s.workerLoad(worker), Resources: worker.Resources}
	if reason := cluster.Admission(snapshot, resources, limits, reserved, processorLoad); reason != "" {
		http.Error(w, reason, 409)
		return
	}
	if worker.MaxTasks <= 0 || s.workerLoad(worker) >= worker.MaxTasks {
		http.Error(w, "worker capacity unavailable", 409)
		return
	}
	now := time.Now()
	task := &Task{ID: id, Type: taskType, Status: TaskStatusAssigned, AssignedTo: worker.ID, CreatedAt: now, StartedAt: &now, Payload: map[string]interface{}{}, ProcessorAssignment: request.Assignment, ProcessorInstall: request.Install}
	msg, err := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: *task, ProcessorAssignment: request.Assignment, ProcessorInstall: request.Install})
	if err != nil {
		http.Error(w, "invalid task", 400)
		return
	}
	select {
	case worker.SendCh <- msg:
		worker.CurrentLoad = s.workerLoad(worker) + 1
		s.tasks[id] = task
		if worker.CurrentLoad >= worker.MaxTasks {
			worker.Status = WorkerStatusBusy
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accepted":true}`))
	default:
		http.Error(w, "worker send queue unavailable", 409)
	}
}
func (s *Server) proxyProcessorWorker(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/processor")
	req, err := http.NewRequestWithContext(r.Context(), r.Method, "http://127.0.0.1:9191/v1"+path, http.MaxBytesReader(w, r.Body, cluster.MaxArtifactBytes))
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, "processor unavailable", 503)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 90<<20))
}
func (w *WorkerAgent) processorRoot() string {
	root, _ := filepath.Abs(filepath.Join(w.config.DataDir, "processor-v2"))
	return root
}
func (w *WorkerAgent) activeTaskIDs() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	ids := []string{}
	for id := range w.tasks {
		ids = append(ids, id)
	}
	return ids
}
func (w *WorkerAgent) activeProcessorIDs() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	ids := []string{}
	for id, t := range w.tasks {
		if t.Type == "processor" {
			ids = append(ids, id)
		}
	}
	return ids
}
func (w *WorkerAgent) activeProcessorWorkspaces() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	names := map[string]bool{}
	for _, t := range w.tasks {
		if t.ProcessorAssignment != nil {
			name, err := cluster.WorkspaceName(t.ProcessorAssignment.ProgramID)
			if err == nil {
				names[name] = true
			}
		}
	}
	out := []string{}
	for name := range names {
		out = append(out, name)
	}
	return out
}
func (w *WorkerAgent) processorWorkerSnapshot() Worker {
	w.mu.RLock()
	snapshot := *w.worker
	snapshot.CurrentLoad = len(w.tasks)
	w.mu.RUnlock()
	snapshot.Metadata = map[string]string{"processor_workspace_root": w.config.ProcessorWorkspaceRoot, "ssh_host": w.config.SSHHost, "ssh_user": w.config.SSHUser, "ssh_port": fmt.Sprint(w.config.SSHPort)}
	if snapshot.Metadata["ssh_host"] == "" {
		snapshot.Metadata["ssh_host"] = publicWorkerIP()
	}
	if w.draining.Load() {
		// Zero capacity is understood by existing controllers and queue brokers.
		// Preserve active attempts, the connection and lease renewals.
		snapshot.MaxTasks = 0
		snapshot.Metadata["maintenance"] = "draining"
	}
	snapshot.Resources = cluster.SampleResources()
	snapshot.ProcessorProtocol = cluster.ProtocolVersion
	snapshot.ProcessorVersions = cluster.InstalledVersions(w.processorRoot())
	snapshot.ProcessorAttempts = w.activeProcessorIDs()
	snapshot.ProcessorWorkspaces = w.activeProcessorWorkspaces()
	return snapshot
}
func (w *WorkerAgent) prepareProcessorRecovery() {
	w.processorRunner = &cluster.Runner{Root: w.processorRoot(), WorkspacesRoot: w.config.ProcessorWorkspaceRoot}
	entries, _ := os.ReadDir(filepath.Join(w.processorRoot(), "attempts"))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(w.processorRoot(), "attempts", entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "acknowledged")); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "assignment.json"))
		if err != nil {
			continue
		}
		var a cluster.Assignment
		if json.Unmarshal(data, &a) != nil || a.WorkerID != w.config.WorkerID {
			continue
		}
		w.tasks[a.AttemptID] = &Task{ID: a.AttemptID, Type: "processor", ProcessorAssignment: &a}
	}
}
func (w *WorkerAgent) executeProcessorTask(task *Task) (*TaskResult, error) {
	if task.Type == "processor_install" {
		if task.ProcessorInstall == nil {
			return nil, errors.New("missing bundle installation")
		}
		ctx, cancel := context.WithTimeout(w.ctx, 5*time.Minute)
		defer cancel()
		_, err := cluster.InstallBundle(ctx, w.processorRoot(), *task.ProcessorInstall)
		if err != nil {
			return nil, err
		}
		if w.processorRunner != nil && w.config.ProcessorWorkspaceRoot != "" {
			if _, err = w.processorRunner.PrepareWorkspace(ctx, task.ProcessorInstall.Version); err != nil {
				return nil, err
			}
		}
		return &TaskResult{ExitCode: 0}, nil
	}
	if task.ProcessorAssignment == nil || task.ProcessorAssignment.WorkerID != w.config.WorkerID {
		return nil, errors.New("invalid processor assignment")
	}
	if w.processorRunner == nil {
		return nil, errors.New("processor runner unavailable")
	}
	err := w.processorRunner.Run(w.ctx, *task.ProcessorAssignment)
	if err != nil {
		return nil, err
	}
	return &TaskResult{ExitCode: 0}, nil
}

func (w *WorkerAgent) publicationDeliveryLoop() {
	defer w.wg.Done()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-tick.C:
			w.retryEnumPublications(w.ctx)
			if w.processorRunner != nil {
				_ = w.processorRunner.DeliverPending(w.ctx)
			}
		}
	}
}

// Called with s.mu held: include installs and not-yet-heartbeated reservations.
func (s *Server) processorReservations(worker string) (cluster.Resources, int) {
	reserved := cluster.Resources{}
	load := 0
	for _, task := range s.tasks {
		if task.AssignedTo != worker || (task.Status != TaskStatusAssigned && task.Status != TaskStatusRunning) {
			continue
		}
		if task.Type == "processor" || task.Type == "processor_install" {
			load++
		}
		r := cluster.Program{}.Resources()
		if task.ExecutionResources != nil {
			r = *task.ExecutionResources
		}
		if task.ProcessorAssignment != nil && task.ProcessorAssignment.Resources.CPUCores > 0 {
			r = task.ProcessorAssignment.Resources
		}
		if w := s.workers[worker]; w != nil && w.Resources.TotalMemoryMB > 0 {
			r = cluster.EffectiveReservation(cluster.Worker{MaxTasks: w.MaxTasks, Resources: w.Resources}, r)
		}
		reserved.CPUCores += r.CPUCores
		reserved.MemoryMB += r.MemoryMB
	}
	return reserved, load
}
