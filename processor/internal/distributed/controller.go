// Package distributed coordinates processor work; it never executes programs.
package distributed

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

type Controller struct {
	configMu                                      sync.RWMutex
	Store                                         *store.Store
	AppDir, WatchDir, BrokerURL, WorkerURL, Token string
	mu                                            sync.Mutex
	workers                                       []cluster.Worker
	lastError                                     string
	installAfter                                  map[string]time.Time
	workerRetry                                   map[string]workerRetry
	cpuEmergency                                  map[string]cpuEmergency
	unavailableSince                              map[string]time.Time
	resultMu                                      sync.Mutex
	config                                        cluster.Config
	PinnedPrograms                                map[string]bool
}

func (c *Controller) root() string { return filepath.Join(c.AppDir, "queue-v2") }
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".stage-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (c *Controller) Prepare(ctx context.Context) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()
	return c.prepareLocked(ctx)
}

func (c *Controller) ResolveConfig(ctx context.Context, config cluster.Config) (cluster.Config, error) {
	retained := map[string]cluster.Program{}
	for id, pinned := range c.PinnedPrograms {
		if !pinned {
			continue
		}
		spec, err := c.Store.CurrentProgram(ctx, id)
		if err != nil {
			return cluster.Config{}, err
		}
		settings, err := c.Store.Settings(ctx, spec.Version)
		if err != nil {
			return cluster.Config{}, err
		}
		retained[id] = settings
	}
	return config.ResolveManifestsRetained(c.AppDir, retained)
}
func (c *Controller) prepareLocked(ctx context.Context) error {
	data, err := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if err != nil {
		return err
	}
	var config cluster.Config
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	if config.ExecutionMode != "distributed" && config.ExecutionMode != "queue" && config.ExecutionMode != "" {
		return errors.New("processor requires execution_mode=queue")
	}
	if err := config.AutoBalance.Validate(); err != nil {
		return err
	}
	if err := validateFileProcessing(config.FileProcessing); err != nil {
		return err
	}
	config, err = c.ResolveConfig(ctx, config)
	if err != nil {
		return err
	}
	ranks := map[string]int{}
	for id, p := range config.Programs {
		if p.PriorityRank() < 1 || p.PriorityRank() > 5 {
			return fmt.Errorf("program %s rank must be between 1 and 5", id)
		}
		ranks[id] = p.PriorityRank()
	}
	if err = c.Store.SetConfiguredRanks(ctx, ranks); err != nil {
		return err
	}
	c.config = config
	exported := map[string]bool{}
	for _, id := range config.ExportResults {
		exported[id] = true
	}
	ids := map[string]bool{}
	for _, programs := range config.Routes {
		for _, id := range programs {
			ids[id] = true
		}
	}
	// Register all manifest children, including scripts reached only through outputs.
	for id, p := range config.Programs {
		if p.Script != nil {
			ids[id] = true
		}
	}
	preparing := map[string]bool{}
	prepared := map[string]bool{}
	var prepare func(string) error
	prepare = func(id string) error {
		if prepared[id] {
			return nil
		}
		if preparing[id] {
			return fmt.Errorf("program connection cycle")
		}
		preparing[id] = true
		defer func() { delete(preparing, id) }()
		p, ok := config.Programs[id]
		if !ok {
			return fmt.Errorf("program %s has no managed bundle definition", id)
		}
		// Resolve downstream pins before comparing retained settings. Registered
		// settings contain these internal targets even though manifests do not.
		if p.Script != nil {
			for i, o := range p.Script.Policies {
				if o.SendTo != "" {
					if err := prepare(o.SendTo); err != nil {
						return err
					}
					target, err := c.Store.CurrentProgram(ctx, o.SendTo)
					if err != nil {
						return err
					}
					p.Script.Policies[i].Target = &cluster.PinnedTarget{ProgramID: o.SendTo, Version: target.Version, Limit: target.Limit, RetrySafe: target.RetrySafe, Export: target.Export}
				}
			}
		}
		// A core-only rollout can retain existing versions without opening tool files.
		if c.PinnedPrograms[id] {
			spec, e := c.Store.CurrentProgram(ctx, id)
			if e != nil {
				return fmt.Errorf("pinned program %s: %w", id, e)
			}
			old, e := c.Store.Settings(ctx, spec.Version)
			if e != nil {
				return e
			}
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(p)
			if !bytes.Equal(a, b) || spec.Limit != p.Limit() || spec.RetrySafe != p.RetrySafe || spec.Export != exported[id] {
				return fmt.Errorf("pinned program %s settings changed; explicitly rebuild its bundle", id)
			}
			if _, e = os.Stat(filepath.Join(c.root(), "bundles", spec.Version+".json")); e != nil {
				return e
			}
			prepared[id] = true
			return nil
		}
		data, version, err := cluster.BuildBundle(c.AppDir, id, p)
		if err != nil {
			return fmt.Errorf("bundle %s: %w", id, err)
		}
		if err = writeAtomic(filepath.Join(c.root(), "bundles", version+".json"), data); err != nil {
			return err
		}
		if err = c.Store.RegisterSettings(ctx, version, p); err != nil {
			return err
		}
		if err = c.Store.RegisterProgram(ctx, id, store.ProgramSpec{Version: version, Limit: p.Limit(), RetrySafe: p.RetrySafe, Export: exported[id]}); err != nil {
			return err
		}
		prepared[id] = true
		return nil
	}
	for id := range ids {
		if err := prepare(id); err != nil {
			return err
		}
	}
	return c.Store.MigrateQueue(ctx)
}
func (c *Controller) broker(ctx context.Context, method, path string, body any, out any) (int, error) {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BrokerURL, "/")+"/api/processor/cluster/"+path, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return resp.StatusCode, fmt.Errorf("CNC broker %s HTTP %d: %s", path, resp.StatusCode, safeBrokerReason(body))
	}
	if out != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
	}
	return resp.StatusCode, err
}
func (c *Controller) installToken(version string) string {
	expires := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	m := hmac.New(sha256.New, []byte(c.Token))
	m.Write([]byte(version + ":" + expires))
	return expires + "." + hex.EncodeToString(m.Sum(nil))
}
func (c *Controller) validInstall(version, token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	until, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || until < time.Now().Unix() || until > time.Now().Add(11*time.Minute).Unix() {
		return false
	}
	m := hmac.New(sha256.New, []byte(c.Token))
	m.Write([]byte(version + ":" + parts[0]))
	expected := hex.EncodeToString(m.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) == 1
}
func hasVersion(w cluster.Worker, v string) bool {
	for _, version := range w.Versions {
		if version == v {
			return true
		}
	}
	return false
}
func (c *Controller) Start(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if err := c.step(ctx); err != nil && ctx.Err() == nil {
			c.mu.Lock()
			c.lastError = err.Error()
			c.mu.Unlock()
			log.Printf("Distributed processor: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (c *Controller) step(ctx context.Context) error {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	c.resultMu.Lock()
	publicationErr := c.flushPublications(ctx)
	c.resultMu.Unlock()
	if publicationErr != nil {
		log.Printf("Publication transfer pending: %v", publicationErr)
	}
	if err := c.Store.ExpireAttempts(ctx); err != nil {
		return err
	}
	active, err := c.Store.ActiveAttempts(ctx)
	if err != nil {
		return err
	}
	for _, a := range active {
		if a.Phase == "uploading" {
			var completion cluster.Completion
			if e := json.Unmarshal([]byte(a.Completion), &completion); e != nil {
				if err = c.Store.FinishAttempt(ctx, a.ID, "failed", "", "", "Saved completion is invalid; results retained for inspection"); err != nil {
					return err
				}
				continue
			}
			c.resultMu.Lock()
			if verifyErr := c.verify(&a, completion); verifyErr != nil {
				err = c.Store.FinishAttempt(ctx, a.ID, "failed", "", "", "Saved completion could not be recovered: "+verifyErr.Error())
			} else {
				err = c.publish(ctx, &a)
			}
			c.resultMu.Unlock()
			if err != nil {
				// A transfer problem must not block unrelated work or lease expiry.
				log.Printf("Completion recovery pending for %s: %v", a.ID, err)
			}
		}
	}
	var workers []cluster.Worker
	if _, err = c.broker(ctx, "GET", "workers", nil, &workers); err != nil {
		return err
	}
	c.mu.Lock()
	c.workers = append([]cluster.Worker(nil), workers...)
	c.lastError = ""
	if c.installAfter == nil {
		c.installAfter = map[string]time.Time{}
	}
	c.mu.Unlock()
	if err := c.balanceCPU(ctx, workers, active); err != nil {
		return err
	}
	sort.Slice(workers, func(i, j int) bool {
		a, b := workers[i], workers[j]
		if c.config.AutoBalance.Enabled && a.Resources.CPUPercent != b.Resources.CPUPercent {
			return a.Resources.CPUPercent < b.Resources.CPUPercent
		}
		if a.MaxTasks <= 0 {
			return false
		}
		if b.MaxTasks <= 0 {
			return true
		}
		lhs, rhs := a.CurrentLoad*b.MaxTasks, b.CurrentLoad*a.MaxTasks
		if lhs != rhs {
			return lhs < rhs
		}
		return a.ID < b.ID
	})
	enabled, err := c.Store.IsEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	if err = c.recoverWorkerShares(ctx, workers, time.Now()); err != nil {
		return err
	}
	runs, err := c.Store.QueuedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		filename, preferred, _, infoErr := c.Store.AssignmentInfo(ctx, run.ID)
		if infoErr != nil {
			return infoErr
		}
		spread := c.config.FileProcessing[filename].Distribution == "spread"
		spec, err := c.Store.PinRun(ctx, run.ID)
		if err != nil {
			c.Store.BlockRun(ctx, run.ID, "program bundle is unavailable")
			continue
		}
		settings, settingsErr := c.Store.Settings(ctx, spec.Version)
		if settingsErr != nil {
			return settingsErr
		}
		chosen := -1
		waiting := "waiting for an online worker with the required program version"
		if preferred != "" {
			waiting = fmt.Sprintf("waiting for assigned worker %s", preferred)
		}
		for i, w := range workers {
			if preferred != "" && w.ID != preferred {
				continue
			}
			if reason := c.cpuAdmissionReason(w); reason != "" {
				waiting = reason
				continue
			}
			if reason := c.workerRetryReason(w.ID); reason != "" {
				waiting = reason
				continue
			}
			if w.Protocol != cluster.ProtocolVersion || (w.Status != "online" && w.Status != "busy") || w.MaxTasks <= w.CurrentLoad {
				continue
			}
			if w.Metadata["processor_workspace_root"] != "" {
				name, _ := cluster.WorkspaceName(run.ProgramID)
				if slices.Contains(w.Workspaces, name) {
					waiting = "waiting for the program workspace to become idle"
					continue
				}
			}
			if reason := cluster.RuntimeAdmission(settings, w.Resources.Capabilities); reason != "" {
				waiting = reason
				continue
			}
			reserved := cluster.Resources{}
			load := 0
			for _, attempt := range active {
				if attempt.WorkerID == w.ID {
					p, e := c.Store.Settings(ctx, attempt.Version)
					if e != nil {
						return e
					}
					r := p.Resources()
					reserved.CPUCores += r.CPUCores
					reserved.MemoryMB += r.MemoryMB
					load++
				}
			}
			if w.ProcessorLoad > load {
				load = w.ProcessorLoad
			}
			if spread && load > 0 {
				waiting = fmt.Sprintf("worker %s is finishing its current processor share", w.ID)
				continue
			}
			if w.Reservations.CPUCores > reserved.CPUCores {
				reserved.CPUCores = w.Reservations.CPUCores
			}
			if w.Reservations.MemoryMB > reserved.MemoryMB {
				reserved.MemoryMB = w.Reservations.MemoryMB
			}
			if reason := cluster.Admission(w, settings.Resources(), c.balancedWorkerLimits(w), reserved, load); reason != "" {
				waiting = reason
				continue
			}
			if hasVersion(w, spec.Version) {
				chosen = i
				break
			}

			key := w.ID + ":" + spec.Version
			c.mu.Lock()
			after := c.installAfter[key]
			c.mu.Unlock()
			if time.Now().After(after) {
				installation := cluster.Install{Version: spec.Version, BundleURL: strings.TrimRight(c.WorkerURL, "/") + "/bundles/" + spec.Version, Token: c.installToken(spec.Version), WorkerLimits: c.balancedWorkerLimits(w)}
				code, installErr := c.broker(ctx, "POST", "assign", map[string]any{"worker_id": w.ID, "install": installation}, nil)
				if installErr == nil {
					workers[i].CurrentLoad++
					workers[i].ProcessorLoad++
					workers[i].Reservations.CPUCores++
					workers[i].Reservations.MemoryMB += 512
					c.mu.Lock()
					c.installAfter[key] = time.Now().Add(30 * time.Second)
					c.mu.Unlock()
				} else if code == 409 {
					waiting = c.deferWorker(w.ID, installErr)
				} else {
					return installErr
				}
			}
		}
		if chosen < 0 {
			c.Store.BlockRun(ctx, run.ID, waiting)
			continue
		}
		var a *store.Attempt
		if c.config.AutoBalance.Enabled && settings.MaxParallel <= 0 && spec.Limit == settings.Limit() {
			a, err = c.Store.AllocateBalancedAttempt(ctx, run.ID, workers[chosen].ID, cluster.LeaseDuration, c.balancedProgramLimit(workers))
		} else {
			a, err = c.Store.AllocateAttempt(ctx, run.ID, workers[chosen].ID, cluster.LeaseDuration)
		}
		if errors.Is(err, store.ErrUnavailable) {
			c.Store.BlockRun(ctx, run.ID, "waiting for program concurrency limit or processing to resume")
			continue
		}
		if err != nil {
			return err
		}
		assignment := cluster.Assignment{AttemptID: a.ID, RunID: run.ID, ProgramID: run.ProgramID, Version: a.Version, WorkerID: a.WorkerID, BaseURL: c.WorkerURL, Token: a.Token, Export: spec.Export, TimeoutSeconds: settings.TimeoutSeconds, Resources: settings.Resources(), PublishOnly: true, WorkerLimits: c.balancedWorkerLimits(workers[chosen]), RequireCgroups: workers[chosen].Resources.Cgroups}
		code, err := c.broker(ctx, "POST", "assign", map[string]any{"worker_id": a.WorkerID, "assignment": assignment}, nil)
		if err != nil {
			if code == 409 {
				reason := c.deferWorker(a.WorkerID, err)
				if err = c.Store.RejectAttempt(ctx, a.ID, reason); err != nil {
					return err
				}
				if err = c.Store.BlockRun(ctx, run.ID, reason); err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			if err = c.Store.CommitPriorityTurn(ctx, a.ID); err != nil {
				return err
			}
			workers[chosen].CurrentLoad++
		}
		// Re-sort on the next tick after each reservation, using fresh CNC capacity.
		return nil
	}
	return nil
}
func (c *Controller) Status(w http.ResponseWriter, r *http.Request) {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"execution_mode": "queue", "workers": c.workers, "error": c.lastError, "auto_balance": c.config.AutoBalance, "cpu_emergency": c.cpuEmergency, "file_processing": c.config.FileProcessing})
}

// WorkerHandler uses per-attempt credentials, never the main management secret.
func (c *Controller) WorkerHandler(w http.ResponseWriter, r *http.Request) {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if strings.HasPrefix(path, "bundles/") {
		version := strings.TrimPrefix(path, "bundles/")
		if r.Method != "GET" || len(version) != 64 || !c.validInstall(version, token) {
			http.Error(w, "Unauthorized", 401)
			return
		}
		if _, err := hex.DecodeString(version); err != nil {
			http.Error(w, "invalid version", 400)
			return
		}
		data, err := os.ReadFile(filepath.Join(c.root(), "bundles", version+".json"))
		if err != nil {
			http.Error(w, "bundle unavailable", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "attempts" {
		http.NotFound(w, r)
		return
	}
	a, err := c.Store.AuthenticateAttempt(r.Context(), parts[1], token)
	if err != nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	action := strings.Join(parts[2:], "/")
	// Execution heartbeats and durable receipts must never wait behind a slow
	// merge/Drive transfer. Completion staging alone needs the publication lock.
	if action != "renew" && action != "start" && action != "input" && action != "publish" {
		c.resultMu.Lock()
		defer c.resultMu.Unlock()
	}
	// Re-read under the publication lock: a concurrent completion may have
	// fenced this request while authentication was reading the old phase.
	a, err = c.Store.Attempt(r.Context(), parts[1])
	if err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	if action == "publish" && r.Method == "POST" {
		if a.Phase == "assigned" || a.Phase == "rejected" {
			http.Error(w, "execution not authorized", 409)
			return
		}
		var publication cluster.Publication
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&publication); decodeErr != nil {
			http.Error(w, "invalid publication", 400)
			return
		}
		if decodeErr := decoder.Decode(new(any)); decodeErr != io.EOF {
			http.Error(w, "invalid publication", 400)
			return
		}
		if acceptErr := c.Store.AcceptPublication(r.Context(), a, publication, c.config.Routes[publication.Name]); acceptErr != nil {
			http.Error(w, acceptErr.Error(), 409)
			return
		}
		// Background delivery is independent of execution and receipt acceptance.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accepted":true}`))
		return
	}
	terminal := a.Phase == "completed" || a.Phase == "failed" || a.Phase == "interrupted"
	if action == "log" && r.Method == "PUT" && terminal && a.Completion == "" {
		data, e := io.ReadAll(http.MaxBytesReader(w, r.Body, cluster.MaxLogBytes))
		if e != nil {
			http.Error(w, "invalid log", 413)
			return
		}
		logPath := filepath.Join(c.AppDir, "logs", a.ID+".log")
		if e = writeAtomic(logPath, data); e == nil {
			e = c.Store.SetRunLog(r.Context(), a.RunID, logPath)
		}
		if e != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		w.Write([]byte(`{}`))
		return
	}
	// Staged or terminal completion is immutable and permits delivery retries.
	if action == "renew" && r.Method == "POST" && (a.Phase == "uploading" || terminal && a.Completion != "") {
		json.NewEncoder(w).Encode(cluster.Lease{Seconds: 30, Phase: a.Phase})
		return
	}
	if action == "complete" && r.Method == "POST" && terminal && a.Completion != "" {
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if readErr == nil && string(data) == a.Completion {
			w.Write([]byte(`{"acknowledged":true}`))
			return
		}
		http.Error(w, "result fenced", 409)
		return
	}
	if terminal && a.Completion != "" && r.Method == "PUT" && (action == "log" || strings.HasPrefix(action, "artifacts/")) {
		name := action
		limit := int64(cluster.MaxLogBytes)
		if action != "log" {
			artifact := strings.TrimPrefix(action, "artifacts/")
			if !cluster.SafeRelative(artifact) || strings.Contains(artifact, "/") {
				http.Error(w, "invalid artifact", 400)
				return
			}
			limit = cluster.MaxArtifactBytes
		}
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		old, oldErr := os.ReadFile(filepath.Join(c.root(), "attempts", a.ID, name))
		if readErr != nil || oldErr != nil || !bytes.Equal(data, old) {
			http.Error(w, "result fenced", 409)
			return
		}
		w.Write([]byte(`{}`))
		return
	}
	if terminal || a.LeaseUntil <= time.Now().UnixMilli() && a.Phase != "uploading" {
		http.Error(w, "attempt fenced", 409)
		return
	}
	stage := filepath.Join(c.root(), "attempts", a.ID)
	switch {
	case action == "renew" && r.Method == "POST":
		if err = c.Store.RenewAttempt(r.Context(), a.ID, cluster.LeaseDuration); err == nil {
			json.NewEncoder(w).Encode(cluster.Lease{Seconds: 30, Phase: a.Phase})
			return
		}
	case action == "start" && r.Method == "POST":
		err = c.Store.AuthorizeAttempt(r.Context(), a.ID)
		if errors.Is(err, store.ErrUnavailable) {
			http.Error(w, "processing paused", 423)
			return
		}
		if err == nil {
			w.Write([]byte(`{"authorized":true}`))
			return
		}
	case action == "input" && r.Method == "GET":
		run, runErr := c.Store.Run(r.Context(), a.RunID)
		if runErr != nil {
			err = runErr
			break
		}
		records, readErr := c.Store.GetBatchRecords(r.Context(), run.BatchAcceptanceID)
		if readErr != nil {
			err = readErr
			break
		}
		var lines []string
		if err = json.Unmarshal([]byte(records), &lines); err != nil {
			break
		}
		w.Header().Set("Content-Type", "text/plain")
		for _, line := range lines {
			io.WriteString(w, line)
			if !strings.HasSuffix(line, "\n") {
				io.WriteString(w, "\n")
			}
		}
		return
	case (action == "log" || strings.HasPrefix(action, "artifacts/")) && r.Method == "PUT":
		name := action
		limit := int64(cluster.MaxLogBytes)
		if action != "log" {
			name = strings.TrimPrefix(action, "artifacts/")
			if !cluster.SafeRelative(name) || strings.Contains(name, "/") || !strings.HasSuffix(name, "_"+a.RunID+".txt") {
				http.Error(w, "invalid artifact", 400)
				return
			}
			name = "artifacts/" + name
			limit = cluster.MaxArtifactBytes
		}
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if readErr != nil {
			http.Error(w, "upload exceeds limit", 413)
			return
		}
		if a.Phase == "uploading" {
			old, readErr := os.ReadFile(filepath.Join(stage, name))
			if readErr != nil || !bytes.Equal(old, data) {
				http.Error(w, "staged result is immutable", 409)
				return
			}
			w.Write([]byte(`{}`))
			return
		}
		if a.Phase != "running" {
			http.Error(w, "execution not authorized", 409)
			return
		}
		if action != "log" {
			total := int64(len(data))
			count := 1
			entries, _ := os.ReadDir(filepath.Join(stage, "artifacts"))
			for _, entry := range entries {
				if entry.Name() == filepath.Base(name) {
					continue
				}
				info, statErr := entry.Info()
				if statErr != nil {
					http.Error(w, "storage unavailable", 503)
					return
				}
				total += info.Size()
				count++
			}
			if total > cluster.MaxArtifactBytes || count > 1000 {
				http.Error(w, "artifact limit exceeded", 413)
				return
			}
		}
		if err = writeAtomic(filepath.Join(stage, name), data); err == nil {
			if action == "log" {
				logPath := filepath.Join(c.AppDir, "logs", a.ID+".log")
				if err = writeAtomic(logPath, data); err == nil {
					err = c.Store.SetRunLog(r.Context(), a.RunID, logPath)
				}
				if err != nil {
					break
				}
			}
			w.Write([]byte(`{}`))
			return
		}
	case action == "complete" && r.Method == "POST":
		data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if readErr != nil {
			http.Error(w, "invalid manifest", 400)
			return
		}
		var completion cluster.Completion
		if err = json.Unmarshal(data, &completion); err != nil {
			http.Error(w, "invalid manifest", 400)
			return
		}
		if err = c.verify(a, completion); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		if err = c.Store.StageCompletion(r.Context(), a.ID, string(data)); err != nil {
			break
		}
		a.Completion = string(data)
		a.Phase = "uploading"
		if err = c.publish(r.Context(), a); err == nil {
			w.Write([]byte(`{"acknowledged":true}`))
			return
		}
	default:
		http.Error(w, "Method or route not supported", 405)
		return
	}
	if errors.Is(err, store.ErrFenced) {
		http.Error(w, "attempt fenced", 409)
	} else {
		http.Error(w, "storage unavailable", 503)
	}
}
func (c *Controller) verify(a *store.Attempt, completion cluster.Completion) error {
	if len(completion.Artifacts) > 1000 {
		return errors.New("too many artifacts")
	}
	seen := map[string]bool{}
	total := int64(0)
	for _, artifact := range completion.Artifacts {
		if !cluster.SafeRelative(artifact.Name) || strings.Contains(artifact.Name, "/") || !strings.HasSuffix(artifact.Name, "_"+a.RunID+".txt") || seen[artifact.Name] || artifact.Size < 0 {
			return errors.New("invalid artifact manifest")
		}
		seen[artifact.Name] = true
		total += artifact.Size
		if total > cluster.MaxArtifactBytes {
			return errors.New("artifact limit exceeded")
		}
		data, err := os.ReadFile(filepath.Join(c.root(), "attempts", a.ID, "artifacts", artifact.Name))
		if err != nil || int64(len(data)) != artifact.Size || cluster.Digest(data) != artifact.SHA256 {
			return errors.New("artifact checksum mismatch")
		}
	}
	if _, err := os.Stat(filepath.Join(c.root(), "attempts", a.ID, "log")); err != nil {
		return errors.New("execution log is missing")
	}
	return nil
}
func (c *Controller) publish(ctx context.Context, a *store.Attempt) error {
	var completion cluster.Completion
	if err := json.Unmarshal([]byte(a.Completion), &completion); err != nil {
		return err
	}
	if err := c.verify(a, completion); err != nil {
		return err
	}
	logData, err := os.ReadFile(filepath.Join(c.root(), "attempts", a.ID, "log"))
	if err != nil {
		return err
	}
	logPath := filepath.Join(c.AppDir, "logs", a.ID+".log")
	if err = writeAtomic(logPath, logData); err != nil {
		return err
	}
	status := "completed"
	if completion.ExitCode != 0 || completion.Error != "" {
		status = "failed"
	}
	if completion.Interrupted {
		status = "interrupted"
	}
	if status == "completed" {
		filename, _, records, e := c.Store.AssignmentInfo(ctx, a.RunID)
		if e != nil {
			return e
		}
		if c.config.FileProcessing[filename].RequireSummary {
			if e = validateCompletionSummary(logData, records); e != nil {
				status = "failed"
				completion.Error = e.Error()
			}
		}
	}
	names, err := c.Store.OutputNames(ctx, a.RunID)
	if err != nil {
		return err
	}
	outputPaths := []string{}
	for _, name := range names {
		outputPaths = append(outputPaths, filepath.Join(c.WatchDir, name))
	}
	if status == "completed" {
		for _, artifact := range completion.Artifacts {
			data, err := os.ReadFile(filepath.Join(c.root(), "attempts", a.ID, "artifacts", artifact.Name))
			if err != nil {
				return err
			}
			dst := filepath.Join(c.WatchDir, artifact.Name)
			marker := filepath.Join(c.root(), "attempts", a.ID, "published", artifact.Name)
			published, markerErr := os.ReadFile(marker)
			if markerErr != nil && !os.IsNotExist(markerErr) {
				return markerErr
			}
			if markerErr == nil && string(published) != artifact.SHA256 {
				return errors.New("publication marker checksum mismatch")
			}
			if os.IsNotExist(markerErr) {
				// Avoid rewriting an already published file after a crash between
				// the rename and marker. Once marked, downstream deletion is final.
				old, readErr := os.ReadFile(dst)
				if readErr != nil && !os.IsNotExist(readErr) {
					return readErr
				}
				if readErr != nil || cluster.Digest(old) != artifact.SHA256 {
					if err = writeAtomic(dst, data); err != nil {
						return err
					}
				}
				if err = writeAtomic(marker, []byte(artifact.SHA256)); err != nil {
					return err
				}
			}
			outputPaths = append(outputPaths, dst)
		}
	}
	return c.Store.FinishAttempt(ctx, a.ID, status, logPath, strings.Join(outputPaths, "\n"), completion.Error)
}

func (c *Controller) flushPublications(ctx context.Context) error {
	for {
		pending, err := c.Store.PendingOutput(ctx)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		grouped := map[string][]string{}
		ids := []int64{}
		for _, line := range pending {
			grouped[line.Name] = append(grouped[line.Name], line.Line)
			ids = append(ids, line.ID)
		}
		for name, lines := range grouped {
			if c.BrokerURL != "" {
				_, err = c.broker(ctx, "POST", "publish", map[string]any{"name": name, "lines": lines}, nil)
			} else {
				err = cluster.MergeText(c.WatchDir, name, lines)
			}
			if err != nil {
				return err
			}
		}
		if err = c.Store.MarkOutput(ctx, ids); err != nil {
			return err
		}
		// Bound work per tick so a busy publication stream cannot starve dispatch.
		return nil
	}
}
