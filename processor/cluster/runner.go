package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Runner keeps a durable start marker and completion manifest. Re-delivery may
// resume uploads, but never runs Python a second time for the same attempt.
type Runner struct {
	Root           string
	WorkspacesRoot string
	mu             sync.Mutex
	active         map[string]bool
}

type cappedWriter struct {
	mu        sync.Mutex
	file      *os.File
	remaining int
	limit     int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.limit == 0 {
		w.limit = w.remaining
	}
	if w.limit <= 0 {
		return n, nil
	}
	if len(p) > w.remaining {
		// Retain recent output, including completion summaries. Compact by half
		// rather than rewriting the entire log on every subsequent small write.
		if len(p) >= w.limit {
			p = p[len(p)-w.limit:]
		}
		keep := min(w.limit/2, w.limit-len(p), w.limit-w.remaining)
		tail := make([]byte, keep)
		if keep > 0 {
			if _, err := w.file.ReadAt(tail, int64(w.limit-w.remaining-keep)); err != nil {
				return 0, err
			}
		}
		if err := w.file.Truncate(0); err != nil {
			return 0, err
		}
		if _, err := w.file.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := w.file.Write(tail); err != nil {
			return 0, err
		}
		w.remaining = w.limit - keep
	}
	if len(p) > 0 {
		written, err := w.file.Write(p)
		w.remaining -= written
		if err != nil {
			return written, err
		}
	}
	return n, nil
}

func durableFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
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

func (a Assignment) endpoint(action string) string {
	return strings.TrimRight(a.BaseURL, "/") + "/attempts/" + a.AttemptID + "/" + action
}
func attemptRequest(ctx context.Context, a Assignment, method, action string, body []byte) ([]byte, int, error) {
	resp, err := request(ctx, method, a.endpoint(action), a.Token, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxArtifactBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(data) > MaxArtifactBytes {
		return nil, resp.StatusCode, errors.New("attempt response exceeds limit")
	}
	if resp.StatusCode >= 300 {
		return data, resp.StatusCode, fmt.Errorf("attempt %s HTTP %d", action, resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

func (r *Runner) Run(ctx context.Context, a Assignment) error {
	if !SafeRelative(a.AttemptID) || strings.Contains(a.AttemptID, "/") || !SafeRelative(a.RunID) || strings.Contains(a.RunID, "/") {
		return errors.New("invalid attempt or run ID")
	}
	r.mu.Lock()
	if r.active == nil {
		r.active = map[string]bool{}
	}
	if r.active[a.AttemptID] {
		r.mu.Unlock()
		return errors.New("attempt already active")
	}
	r.active[a.AttemptID] = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.active, a.AttemptID); r.mu.Unlock() }()
	dir := filepath.Join(r.Root, "attempts", a.AttemptID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	assignmentBytes, _ := json.Marshal(a)
	if old, err := os.ReadFile(filepath.Join(dir, "assignment.json")); err == nil {
		if !bytes.Equal(old, assignmentBytes) {
			return errors.New("attempt assignment changed")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err = durableFile(filepath.Join(dir, "assignment.json"), assignmentBytes); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "acknowledged")); err == nil {
		return nil
	}

	// A conservative monotonic deadline prevents execution after lease loss,
	// even if wall clocks differ or a renewal response is delayed in transit.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var leaseMu sync.Mutex
	deadline := time.Now()
	leasePhase := ""
	renew := func() error {
		sent := time.Now()
		data, _, err := attemptRequest(runCtx, a, "POST", "renew", nil)
		if err != nil {
			return err
		}
		var lease Lease
		if err = json.Unmarshal(data, &lease); err != nil {
			return err
		}
		if lease.Seconds < 2 || lease.Seconds > 30 {
			return errors.New("invalid lease duration")
		}
		leaseMu.Lock()
		deadline = sent.Add(time.Duration(lease.Seconds-1) * time.Second)
		leasePhase = lease.Phase
		leaseMu.Unlock()
		return nil
	}
	if err := renew(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		next := time.Now().Add(HeartbeatInterval)
		for {
			select {
			case <-runCtx.Done():
				return
			case now := <-tick.C:
				leaseMu.Lock()
				expired := !now.Before(deadline)
				leaseMu.Unlock()
				if expired {
					cancel()
					return
				}
				if !now.Before(next) {
					renewalCtx, cancelRenew := context.WithTimeout(runCtx, 2*time.Second)
					sent := time.Now()
					data, code, err := attemptRequest(renewalCtx, a, "POST", "renew", nil)
					cancelRenew()
					if code == http.StatusConflict || code == http.StatusUnauthorized {
						cancel()
						return
					}
					if err == nil {
						var lease Lease
						if json.Unmarshal(data, &lease) == nil && lease.Seconds >= 2 && lease.Seconds <= 30 {
							leaseMu.Lock()
							deadline = sent.Add(time.Duration(lease.Seconds-1) * time.Second)
							leasePhase = lease.Phase
							leaseMu.Unlock()
						}
					}
					if logData, e := os.ReadFile(filepath.Join(dir, "log")); e == nil {
						logCtx, stop := context.WithTimeout(runCtx, 2*time.Second)
						_, _, _ = attemptRequest(logCtx, a, "PUT", "log", logData)
						stop()
					}
					next = time.Now().Add(HeartbeatInterval)
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()

	var completion Completion
	saved, err := os.ReadFile(filepath.Join(dir, "completion.json"))
	if err == nil {
		if err = json.Unmarshal(saved, &completion); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if _, err = os.Stat(filepath.Join(dir, "started")); err == nil {
			completion = Completion{ExitCode: -1, Interrupted: true, Error: "worker restarted after execution authorization; execution outcome is uncertain"}
		} else if !os.IsNotExist(err) {
			return err
		} else {
			completion, err = r.execute(runCtx, a, dir)
			if err != nil {
				return err
			}
		}
		saved, _ = json.Marshal(completion)
		if err = durableFile(filepath.Join(dir, "completion.json"), saved); err != nil {
			return err
		}
	}
	// Keep completed work on disk until an idempotent server acknowledgment.
	for {
		leaseMu.Lock()
		terminal := leasePhase == "completed" || leasePhase == "failed" || leasePhase == "interrupted"
		leaseMu.Unlock()
		if terminal {
			// The controller may have quarantined a damaged staged result. Its
			// immutable completion receipt releases the slot without reuploading
			// missing files or starting the program again.
			_, _, err = attemptRequest(runCtx, a, "POST", "complete", saved)
		} else {
			err = r.upload(runCtx, a, dir, completion, saved)
		}
		if err == nil {
			return durableFile(filepath.Join(dir, "acknowledged"), []byte("ok"))
		}
		if runCtx.Err() != nil {
			return fmt.Errorf("result retained locally: %w", err)
		}
		select {
		case <-runCtx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}

func (r *Runner) execute(ctx context.Context, a Assignment, dir string) (Completion, error) {
	b, err := LoadBundle(r.Root, a.Version)
	if err != nil {
		return Completion{}, err
	}
	if b.ProgramID != a.ProgramID {
		return Completion{}, errors.New("program version mismatch")
	}
	packageSourceDir := filepath.Dir(b.PackageLock)
	input, _, err := attemptRequest(ctx, a, "GET", "input", nil)
	if err != nil {
		return Completion{}, err
	}
	work := filepath.Join(dir, "work")
	out := filepath.Join(dir, "output")
	if r.WorkspacesRoot != "" {
		var unlock func()
		work, unlock, err = r.lockWorkspace(ctx, a.ProgramID)
		if err != nil {
			return Completion{}, err
		}
		defer unlock()
		out = filepath.Join(work, "output")
		b, err = workspaceBundle(b)
		if err != nil {
			return Completion{}, err
		}
		if err = archiveWorkspaceOutputs(work, b, a.AttemptID); err != nil {
			return Completion{}, err
		}
		if err = recordWorkspace(work, dir, a); err != nil {
			return Completion{}, err
		}
	}
	if err = os.MkdirAll(work, 0700); err != nil {
		return Completion{}, err
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		return Completion{}, err
	}
	// Each attempt gets its own writable program tree; cached bundles stay immutable.
	for name, data := range b.Files {
		if err = NoSymlinks(work, name); err != nil {
			return Completion{}, err
		}
		dst := filepath.Join(work, name)
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return Completion{}, err
		}
		if err = os.WriteFile(dst, data, 0600); err != nil {
			return Completion{}, err
		}
	}
	inputPath := filepath.Join(dir, "input.txt")
	if r.WorkspacesRoot != "" {
		inputPath = filepath.Join(work, "input.txt")
	}
	if r.WorkspacesRoot != "" {
		if err = NoSymlinks(work, "input.txt"); err != nil {
			return Completion{}, err
		}
	}
	if err = durableFile(inputPath, input); err != nil {
		return Completion{}, err
	}
	workingDir := work
	if b.Script != nil {
		p := Program{Entrypoint: b.Entrypoint, RequirementsLock: b.RequirementsLock, PackageLock: b.PackageLock, Script: b.Script}
		for name := range b.Files {
			p.Files = append(p.Files, name)
		}
		if err = p.ValidateScript(); err != nil {
			return Completion{}, err
		}
		workingDir = filepath.Join(work, filepath.Dir(b.Entrypoint))
		relative := filepath.Join(filepath.Dir(b.Entrypoint), b.Script.Input)
		if err = NoSymlinks(work, relative); err != nil {
			return Completion{}, err
		}
		inputPath = filepath.Join(work, relative)
		if err = os.MkdirAll(filepath.Dir(inputPath), 0700); err != nil {
			return Completion{}, err
		}
		if err = durableFile(inputPath, input); err != nil {
			return Completion{}, err
		}
	}
	for {
		_, code, startErr := attemptRequest(ctx, a, "POST", "start", nil)
		if startErr == nil {
			break
		}
		if code != http.StatusLocked {
			return Completion{}, startErr
		}
		select {
		case <-ctx.Done():
			return Completion{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	// Persist before starting. A crash between this marker and cmd.Start is
	// intentionally treated as uncertain rather than risking duplicate effects.
	if err = durableFile(filepath.Join(dir, "started"), []byte("authorized")); err != nil {
		return Completion{}, err
	}
	python := "python3"
	if b.RequirementsLock != "" {
		python = filepath.Join(r.Root, "bundles", a.Version, "venv", "bin", "python")
	}
	if b.PackageLock != "" {
		source := filepath.Join(r.Root, "bundles", a.Version, packageSourceDir, "node_modules")
		target := filepath.Join(work, filepath.Dir(b.PackageLock), "node_modules")
		if err := copyDependencies(source, target); err != nil {
			return Completion{}, err
		}
	}
	execCtx, cancel := context.WithCancel(ctx)
	if a.TimeoutSeconds > 0 {
		cancel()
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(a.TimeoutSeconds)*time.Second)
	}
	defer cancel()
	args := []string{filepath.Join(work, b.Entrypoint), inputPath, out}
	if b.Script != nil {
		args = args[:1]
	}
	if b.AutoPublish.Enabled {
		watch := out
		if b.AutoPublish.Directory == "work" {
			watch = work
		}
		var watches any = watch
		if b.AutoPublish.Directory == "both" {
			watches = []string{work, out}
		}
		excluded := []string{inputPath}
		for name := range b.Files {
			excluded = append(excluded, filepath.Join(work, name))
		}
		settings := map[string]any{"watch": watches, "excluded": excluded, "entrypoint": filepath.Join(work, b.Entrypoint), "input": inputPath, "output": out}
		if b.Script != nil {
			settings["watch"] = workingDir
			settings["outputs"] = b.Script.Outputs
			settings["args"] = []string{}
			command := []string{python, filepath.Join(work, b.Entrypoint)}
			switch b.Script.Runtime {
			case "node":
				command = []string{"node", filepath.Join(work, b.Entrypoint)}
			case "bash":
				command = []string{"bash", filepath.Join(work, b.Entrypoint)}
			case "binary":
				if err := os.Chmod(filepath.Join(work, b.Entrypoint), 0700); err != nil {
					return Completion{}, err
				}
				command = []string{filepath.Join(work, b.Entrypoint)}
			}
			settings["command"] = command
		}
		config, marshalErr := json.Marshal(settings)
		if marshalErr != nil {
			return Completion{}, marshalErr
		}
		args = []string{filepath.Join(work, "processor_supervise.py"), string(config)}
	}
	cmd := exec.CommandContext(execCtx, python, args...)
	cmd.Dir = workingDir
	cmd.Env = publicationEnv(a, dir, work)
	if b.AutoPublish.Enabled {
		cmd.Env = append(cmd.Env, "CNC_PUBLICATION_PREFIX="+b.ProgramID+"__")
	}
	resources := a.Resources
	if resources.CPUCores <= 0 {
		resources = Program{}.Resources()
	}
	if a.RequireCgroups && !CgroupAvailable() {
		return Completion{}, errors.New("required cgroup enforcement unavailable")
	}
	cleanupGroup, groupErr := configureCgroup(cmd, a.AttemptID, resources)
	if groupErr != nil {
		return Completion{}, groupErr
	}
	defer cleanupGroup()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	logFile, err := os.OpenFile(filepath.Join(dir, "log"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return Completion{}, err
	}
	writer := &cappedWriter{file: logFile, remaining: MaxLogBytes}
	cmd.Stdout = writer
	cmd.Stderr = writer
	executionErr := cmd.Run()
	// A successful parent exit must not leave background children writing
	// artifacts after collection or continuing beyond this attempt's lease.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	syncErr := logFile.Sync()
	closeErr := logFile.Close()
	c := Completion{ExitCode: 0, Artifacts: []Artifact{}}
	if executionErr != nil {
		c.ExitCode = -1
		var exit *exec.ExitError
		if errors.As(executionErr, &exit) {
			c.ExitCode = exit.ExitCode()
		}
		c.Error = executionErr.Error()
		if ctx.Err() != nil {
			c.Interrupted = true
		}
	}
	if syncErr != nil {
		c.ExitCode = -1
		c.Error = syncErr.Error()
	}
	if closeErr != nil {
		c.ExitCode = -1
		c.Error = closeErr.Error()
	}
	if c.ExitCode == 0 && !a.PublishOnly {
		locations := []string{out}
		if a.Export {
			locations = append(locations, work)
		}
		used := map[string]bool{}
		total := int64(0)
		for _, location := range locations {
			entries, readErr := os.ReadDir(location)
			if readErr != nil {
				c.ExitCode = -1
				c.Error = readErr.Error()
				break
			}
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".txt") {
					continue
				}
				src := filepath.Join(location, e.Name())
				info, statErr := os.Lstat(src)
				if statErr != nil || !info.Mode().IsRegular() {
					c.ExitCode = -1
					c.Error = "output must be a regular file"
					break
				}
				if src == inputPath || location == work && b.Files[e.Name()] != nil {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".txt") + "_" + a.RunID + ".txt"
				if used[name] {
					c.ExitCode = -1
					c.Error = "duplicate artifact name"
					break
				}
				used[name] = true
				total += info.Size()
				if total > MaxArtifactBytes || len(c.Artifacts) >= 1000 {
					c.ExitCode = -1
					c.Error = "output limit exceeded"
					break
				}
				data, readErr := os.ReadFile(src)
				if readErr != nil {
					c.ExitCode = -1
					c.Error = readErr.Error()
					break
				}
				artifactDir := filepath.Join(dir, "artifacts")
				if err = os.MkdirAll(artifactDir, 0700); err != nil {
					return Completion{}, err
				}
				if err = durableFile(filepath.Join(artifactDir, name), data); err != nil {
					return Completion{}, err
				}
				c.Artifacts = append(c.Artifacts, Artifact{Name: name, SHA256: Digest(data), Size: int64(len(data))})
			}
			if c.ExitCode != 0 {
				break
			}
		}
	}
	return c, nil
}
func (r *Runner) upload(ctx context.Context, a Assignment, dir string, c Completion, manifest []byte) error {
	if err := drainPublications(ctx, a, dir); err != nil {
		return err
	}
	logData, err := os.ReadFile(filepath.Join(dir, "log"))
	if os.IsNotExist(err) {
		logData = nil
	} else if err != nil {
		return err
	}
	if _, _, err = attemptRequest(ctx, a, "PUT", "log", logData); err != nil {
		return err
	}
	for _, artifact := range c.Artifacts {
		if !SafeRelative(artifact.Name) || strings.Contains(artifact.Name, "/") {
			return errors.New("invalid stored artifact")
		}
		data, err := os.ReadFile(filepath.Join(dir, "artifacts", artifact.Name))
		if err != nil {
			return err
		}
		if Digest(data) != artifact.SHA256 || int64(len(data)) != artifact.Size {
			return errors.New("stored artifact checksum mismatch")
		}
		if _, _, err = attemptRequest(ctx, a, "PUT", "artifacts/"+artifact.Name, data); err != nil {
			return err
		}
	}
	_, _, err = attemptRequest(ctx, a, "POST", "complete", manifest)
	return err
}

func publicationEnv(a Assignment, dir, work string) []string {
	env := os.Environ()
	// Never inherit naming policy from the worker service into unrelated jobs.
	env = slices.DeleteFunc(env, func(entry string) bool { return strings.HasPrefix(entry, "CNC_PUBLICATION_PREFIX=") })
	values := map[string]string{"CNC_PUBLICATION_OUTBOX": filepath.Join(dir, "publications.db"), "CNC_PUBLICATION_URL": a.endpoint("publish"), "CNC_PUBLICATION_TOKEN": a.Token, "CNC_JOB_ID": a.RunID, "CNC_TOOL_THREADS": fmt.Sprint(a.Resources.Threads), "PYTHONPATH": work + string(os.PathListSeparator) + os.Getenv("PYTHONPATH"), "PYTHONUNBUFFERED": "1", "PYTHONDONTWRITEBYTECODE": "1"}
	if a.Resources.Threads <= 0 {
		values["CNC_TOOL_THREADS"] = "1"
	}
	for _, name := range []string{"OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS", "NUMEXPR_NUM_THREADS"} {
		values[name] = values["CNC_TOOL_THREADS"]
	}
	filtered := []string{}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if _, ok := values[name]; !ok {
			filtered = append(filtered, entry)
		}
	}
	for name, value := range values {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}
func drainPublications(ctx context.Context, a Assignment, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "publications.db")); os.IsNotExist(err) {
		return nil
	}
	command := exec.CommandContext(ctx, "python3", "-c", string(publicationHelper), "--drain")
	command.Dir = dir
	command.Env = publicationEnv(a, dir, "")
	if data, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("publication outbox retained: %w: %s", err, data)
	}
	return nil
}

// DeliverPending retries only result transfer, independently of execution leases.
// Expired attempts can publish their durable partial results but cannot run again.
func (r *Runner) DeliverPending(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(r.Root, "attempts"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		r.mu.Lock()
		busy := r.active[entry.Name()]
		r.mu.Unlock()
		if busy {
			continue
		}
		dir := filepath.Join(r.Root, "attempts", entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "acknowledged")); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "publications-delivered")); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "publications.db")); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "assignment.json"))
		if err != nil {
			continue
		}
		var a Assignment
		if json.Unmarshal(data, &a) != nil {
			continue
		}
		transferCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = drainPublications(transferCtx, a, dir)
		if err == nil {
			if logData, e := os.ReadFile(filepath.Join(dir, "log")); e == nil {
				_, _, err = attemptRequest(transferCtx, a, "PUT", "log", logData)
			}
		}
		cancel()
		if err == nil {
			_ = durableFile(filepath.Join(dir, "publications-delivered"), []byte("ok"))
		}
		if err != nil && ctx.Err() != nil {
			return err
		}
	}
	return nil
}

// Materialize npm dependencies privately; generated links must stay inside cache.
func copyDependencies(source, target string) error {
	if _, err := os.Stat(source); os.IsNotExist(err) {
		// A new lockfile may have no dependencies; do not retain the old tree.
		return os.RemoveAll(target)
	}
	// Build a fresh tree so repeated workspace runs and dependency updates cannot
	// retain old links or files. The workspace lock serializes this replacement.
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".dependencies-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	canonicalSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	err = filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(stage, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			inside, err := filepath.Rel(canonicalSource, resolved)
			if err != nil || !SafeRelative(inside) {
				return errors.New("dependency link escapes cache")
			}
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) {
				return errors.New("absolute dependency link")
			}
			return os.Symlink(link, dst)
		}
		if !d.Type().IsRegular() {
			return errors.New("invalid dependency file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0600|info.Mode().Perm()&0111)
	})
	if err != nil {
		return err
	}
	previous := stage + "-previous"
	if err = os.Rename(target, previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Rename(stage, target); err != nil {
		_ = os.Rename(previous, target)
		return err
	}
	return os.RemoveAll(previous)
}
