package cnc

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cnc-enum/version"
)

type EnumJobConfig struct {
	WordPressHomepages bool                `json:"wordpress_homepages,omitempty"`
	ArtifactSHA256     string              `json:"artifact_sha256"`
	Actor              string              `json:"actor"`
	AcknowledgedAt     time.Time           `json:"acknowledged_at"`
	TargetScope        version.TargetScope `json:"target_scope"`
	Automatic          bool                `json:"automatic"`
	Threads            int                 `json:"threads"`
	RequestTimeoutMS   int                 `json:"request_timeout_ms"`
	Wordlists          []string            `json:"wordlists"`
	Token              string              `json:"-"`
	ArtifactFilename   string              `json:"-"`
}
type enumLaunchRequest struct {
	FreshBatchID   string                 `json:"fresh_batch_id"`
	ToolID         string                 `json:"tool_id"`
	InputFile      string                 `json:"input_file"`
	Mode           string                 `json:"mode"`
	TimeoutSeconds *int                   `json:"timeout_seconds"`
	Options        map[string]interface{} `json:"options"`
	WPPlugins      string                 `json:"wp_plugins"`
	JoomlaExts     string                 `json:"joomla_exts"`
	VersionRules   []version.VersionRule  `json:"version_rules"`
	Authorization  struct {
		Acknowledged bool                `json:"acknowledged"`
		Scope        version.TargetScope `json:"scope"`
	} `json:"target_authorization"`
}

func dashboardAuditSignature(secret, actor, timestamp string, body []byte) string {
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s\n%s\nPOST\n/api/tools/launch\n%s", actor, timestamp, hex.EncodeToString(digest[:]))
	return hex.EncodeToString(mac.Sum(nil))
}
func dashboardActor(r *http.Request, body []byte) (string, error) {
	secret := os.Getenv("CNC_DASHBOARD_AUDIT_SECRET")
	actor, stamp := r.Header.Get("X-CNC-Actor"), r.Header.Get("X-CNC-Audit-Time")
	ms, err := strconv.ParseInt(stamp, 10, 64)
	if len(secret) < 32 || actor == "" || len(actor) > 128 || strings.ContainsAny(actor, "\r\n") || err != nil || time.Since(time.UnixMilli(ms)).Abs() > 2*time.Minute {
		return "", fmt.Errorf("authenticated dashboard identity is required")
	}
	expected := dashboardAuditSignature(secret, actor, stamp, body)
	if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-CNC-Audit-Signature"))) {
		return "", fmt.Errorf("invalid dashboard identity signature")
	}
	return actor, nil
}

func randomEnumToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func enumNumber(options map[string]interface{}, name string, fallback, min, max int) (int, error) {
	value, ok := options[name]
	if !ok {
		return fallback, nil
	}
	n, ok := value.(float64)
	if !ok || n != float64(int(n)) || n < float64(min) || n > float64(max) {
		return 0, fmt.Errorf("%s must be an integer from %d to %d", name, min, max)
	}
	return int(n), nil
}

func (s *Server) launchEnum(w http.ResponseWriter, r *http.Request, body []byte) {
	fail := func(status int, err error) { writeJSON(w, status, map[string]string{"error": err.Error()}) }
	actor, err := dashboardActor(r, body)
	if err != nil {
		fail(http.StatusUnauthorized, err)
		return
	}
	var request enumLaunchRequest
	if json.Unmarshal(body, &request) != nil {
		fail(400, fmt.Errorf("invalid enum payload"))
		return
	}
	if request.Mode != "" && request.Mode != string(JobModeSpread) {
		fail(400, fmt.Errorf("enum jobs require spread mode"))
		return
	}
	bulkWordPress := request.ToolID == "wordpress-bulk"
	jobID := ""
	if request.FreshBatchID != "" {
		decoded, err := hex.DecodeString(request.FreshBatchID)
		if !bulkWordPress || err != nil || len(decoded) != 16 || request.FreshBatchID != strings.ToLower(request.FreshBatchID) {
			fail(400, fmt.Errorf("invalid WordPress fresh batch ID"))
			return
		}
		jobID = version.WordPressFreshJobPrefix + request.FreshBatchID
		s.wordpressFreshMu.Lock()
		defer s.wordpressFreshMu.Unlock()
		s.mu.RLock()
		existing := s.jobs[jobID]
		s.mu.RUnlock()
		if existing != nil {
			if existing.Enum == nil || existing.Enum.Actor != actor {
				fail(409, fmt.Errorf("fresh batch ID already in use"))
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"job_id": jobID})
			return
		}
	}
	if bulkWordPress && (len(request.VersionRules) != 0 || request.WPPlugins != "" || request.JoomlaExts != "") {
		fail(400, fmt.Errorf("bulk WordPress verification accepts homepage targets only"))
		return
	}
	// New scans do not restrict targets by host, CIDR, protocol, or port.
	// The legacy authorization field is accepted for old clients but ignored.
	scope := version.TargetScope{}
	rules, err := version.NormalizeRules(request.VersionRules)
	if err != nil {
		fail(400, err)
		return
	}
	defaultThreads, defaultRequestTimeout := 256, 10
	if bulkWordPress {
		defaultThreads, defaultRequestTimeout = 64, 5
	}
	threads, err := enumNumber(request.Options, "threads", defaultThreads, 1, 256)
	if err != nil {
		fail(400, err)
		return
	}
	requestTimeout, err := enumNumber(request.Options, "timeout", defaultRequestTimeout, 1, 120)
	if err != nil {
		fail(400, err)
		return
	}
	for key := range request.Options {
		if key != "auto_concurrency" && key != "threads" && key != "timeout" && key != "output_file" {
			fail(400, fmt.Errorf("unsupported enum option %s", key))
			return
		}
	}
	automatic := true
	if value, ok := request.Options["auto_concurrency"]; ok {
		var valid bool
		automatic, valid = value.(bool)
		if !valid {
			fail(400, fmt.Errorf("auto_concurrency must be boolean"))
			return
		}
	}
	timeout := NoTimeout
	if request.TimeoutSeconds != nil {
		timeout = *request.TimeoutSeconds
		if timeout != NoTimeout && timeout <= 0 {
			fail(400, fmt.Errorf("invalid overall timeout"))
			return
		}
	}
	input, err := os.Open(ExpandPath(request.InputFile))
	if err != nil {
		fail(400, fmt.Errorf("cannot read source file"))
		return
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		fail(400, fmt.Errorf("source must be a non-empty regular file"))
		return
	}
	suffix, err := randomEnumToken()
	if err != nil {
		fail(500, fmt.Errorf("cannot create enum job"))
		return
	}
	if jobID == "" {
		jobID = "enum_" + suffix[:24]
	}
	directory := filepath.Join(s.config.DataDir, "enum-jobs", jobID)
	if request.FreshBatchID != "" {
		if _, err := os.Stat(filepath.Join(directory, "state.json")); os.IsNotExist(err) {
			if err := os.RemoveAll(directory); err != nil {
				fail(500, fmt.Errorf("cannot clean incomplete fresh batch"))
				return
			}
		} else if err != nil {
			fail(500, fmt.Errorf("cannot read fresh batch state"))
			return
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		fail(500, fmt.Errorf("cannot persist enum job"))
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(directory)
		}
	}()
	snapshot, err := os.OpenFile(filepath.Join(directory, "input.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail(500, err)
		return
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lines, skipped := 0, 0
	snapshotWriter := bufio.NewWriterSize(snapshot, 64*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if bulkWordPress {
			home, _ := version.WordPressHomepage(raw)
			if home == "" {
				skipped++
				continue
			}
			raw = home
		}
		target, targetErr := version.CanonicalTarget(raw)
		if targetErr != nil {
			skipped++
			continue
		}
		snapshotTarget := target.URL()
		if !strings.Contains(raw, "://") {
			snapshotTarget = strings.TrimPrefix(snapshotTarget, "https://")
			snapshotTarget = strings.Replace(snapshotTarget, ":443/", "/", 1)
		}
		if _, err = fmt.Fprintln(snapshotWriter, snapshotTarget); err != nil {
			snapshot.Close()
			fail(500, fmt.Errorf("cannot persist input snapshot"))
			return
		}
		lines++
	}
	if scanner.Err() != nil || lines == 0 {
		snapshot.Close()
		fail(400, fmt.Errorf("input contains no valid HTTP targets"))
		return
	}
	if err = snapshotWriter.Flush(); err == nil {
		err = snapshot.Sync()
	}
	if err != nil {
		snapshot.Close()
		fail(500, err)
		return
	}
	snapshot.Close()
	artifact := version.ArtifactV1{SchemaVersion: 1, JobID: jobID, TargetScope: scope, Rules: rules, WordPressHomepages: bulkWordPress}
	data, digest, err := version.ArtifactBytes(artifact)
	if err != nil {
		fail(400, err)
		return
	}
	artifactName := jobID + "-" + digest + ".json"
	if _, err = version.WriteImmutable(directory, artifactName, data); err != nil {
		fail(500, fmt.Errorf("cannot persist rule artifact"))
		return
	}
	token, err := randomEnumToken()
	if err != nil {
		fail(500, err)
		return
	}
	config := &EnumJobConfig{WordPressHomepages: bulkWordPress, ArtifactSHA256: digest, Actor: actor, TargetScope: scope, Automatic: automatic, Threads: threads, RequestTimeoutMS: requestTimeout * 1000, ArtifactFilename: artifactName, Token: token, Wordlists: []string{}}
	for name, content := range map[string]string{"wp_plugins.txt": request.WPPlugins, "joomla_exts.txt": request.JoomlaExts} {
		if content == "" {
			continue
		}
		if _, err = version.WriteImmutable(directory, name, []byte(content)); err != nil {
			fail(500, fmt.Errorf("cannot persist wordlist"))
			return
		}
		config.Wordlists = append(config.Wordlists, name)
	}
	audit, _ := json.Marshal(struct {
		JobID        string         `json:"job_id"`
		Acknowledged bool           `json:"acknowledged"`
		Config       *EnumJobConfig `json:"config"`
	}{jobID, false, config})
	if _, err = version.WriteImmutable(directory, "audit.json", audit); err != nil {
		fail(500, fmt.Errorf("cannot persist authorization audit"))
		return
	}
	job := &Job{ID: jobID, Name: "Tool: enum", Mode: JobModeSpread, InputFile: filepath.Join(directory, "input.txt"), TimeoutSeconds: timeout, Enum: config}
	if bulkWordPress {
		job.Name = "Tool: wordpress-bulk"
		job.OutputFile = version.WordPressOutputName(jobID)
	}
	keep = true
	s.submitJob(job)
	writeJSON(w, http.StatusCreated, map[string]interface{}{"status": "launched", "job_id": jobID, "mode": JobModeSpread, "artifact_sha256": digest, "strict": len(rules) > 0, "skipped_inputs": skipped})
}

func (s *Server) handleEnumAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/enum/"), "/")
	if len(parts) >= 3 && parts[0] == "jobs" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		s.mu.RLock()
		job := s.jobs[parts[1]]
		if job == nil || job.Enum == nil || subtle.ConstantTimeCompare([]byte(job.Enum.Token), []byte(r.Header.Get("X-Enum-Job-Token"))) != 1 {
			s.mu.RUnlock()
			http.Error(w, "invalid enum job", 403)
			return
		}
		name := parts[2]
		path := ""
		switch name {
		case "artifact":
			if len(parts) == 3 {
				path = filepath.Join(s.config.DataDir, "enum-jobs", job.ID, job.Enum.ArtifactFilename)
			}
		case "chunks":
			if len(parts) == 4 {
				if task := s.tasks[parts[3]]; task != nil && task.JobID == job.ID {
					path, _ = task.Payload["chunk_path"].(string)
				}
			}
		case "wp_plugins.txt", "joomla_exts.txt":
			if len(parts) == 3 {
				for _, registered := range job.Enum.Wordlists {
					if registered == name {
						path = filepath.Join(s.config.DataDir, "enum-jobs", job.ID, name)
					}
				}
			}
		}
		s.mu.RUnlock()
		if path == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
		return
	}
	if len(parts) == 2 && parts[0] == "results" && r.Method == http.MethodPost {
		s.acceptEnumResults(w, r, parts[1])
		return
	}
	http.NotFound(w, r)
}

func (s *Server) acceptEnumResults(w http.ResponseWriter, r *http.Request, taskID string) {
	s.mu.RLock()
	task := s.tasks[taskID]
	if task == nil || task.Type != "enum" {
		s.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	job := s.jobs[task.JobID]
	if job == nil || job.Enum == nil || task.AssignedTo != r.Header.Get("X-Worker-ID") || subtle.ConstantTimeCompare([]byte(job.Enum.Token), []byte(r.Header.Get("X-Enum-Job-Token"))) != 1 {
		s.mu.RUnlock()
		http.Error(w, "invalid publication binding", 403)
		return
	}
	jobID, digest, artifactPath := job.ID, job.Enum.ArtifactSHA256, filepath.Join(s.config.DataDir, "enum-jobs", job.ID, job.Enum.ArtifactFilename)
	chunkPath, _ := task.Payload["chunk_path"].(string)
	s.mu.RUnlock()
	artifact, err := version.LoadArtifact(artifactPath, digest, jobID)
	if err != nil {
		http.Error(w, "invalid stored artifact", 500)
		return
	}
	basename, err := version.ResultBasename(jobID, taskID)
	if err != nil || r.Header.Get("X-Enum-Filename") != basename {
		http.Error(w, "invalid result filename", 400)
		return
	}
	targets := map[version.TargetLocator]bool{}
	chunk, err := os.Open(chunkPath)
	if err != nil {
		http.Error(w, "missing task input", 500)
		return
	}
	scanner := bufio.NewScanner(chunk)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		target, e := version.CanonicalTarget(scanner.Text())
		if e != nil {
			chunk.Close()
			http.Error(w, "invalid task input", 500)
			return
		}
		targets[target] = true
	}
	scanErr := scanner.Err()
	chunk.Close()
	if scanErr != nil {
		http.Error(w, "incomplete task input", 500)
		return
	}
	publicationDirectory := filepath.Join(s.config.DataDir, "enum-jobs", jobID, "publications")
	if err := os.MkdirAll(publicationDirectory, 0700); err != nil {
		http.Error(w, "cannot stage publication", 500)
		return
	}
	staged, err := os.CreateTemp(publicationDirectory, ".receiving-*")
	if err != nil {
		http.Error(w, "cannot stage publication", 500)
		return
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	contentDigest, err := stageEnumJSONL(r.Body, staged, artifact, taskID, targets)
	if err != nil {
		http.Error(w, "invalid or incomplete JSONL result", 400)
		return
	}
	if contentDigest != r.Header.Get("X-Enum-SHA256") {
		http.Error(w, "result digest mismatch", 400)
		return
	}
	if err := staged.Sync(); err != nil {
		http.Error(w, "cannot sync publication", 500)
		return
	}
	// Structured publications are internal receipts/evidence, never user exports.
	directory := publicationDirectory
	watcherFileMu.Lock()
	receiptPath := filepath.Join(publicationDirectory, taskID+".receipt.json")
	if receipt, e := os.ReadFile(receiptPath); e == nil {
		var accepted struct {
			SHA256 string `json:"sha256"`
		}
		if json.Unmarshal(receipt, &accepted) != nil {
			watcherFileMu.Unlock()
			http.Error(w, "invalid publication receipt", 500)
			return
		}
		if accepted.SHA256 != contentDigest {
			watcherFileMu.Unlock()
			s.enumPublicationState(taskID, "conflicted")
			http.Error(w, "publication filename collision", 409)
			return
		}
		watcherFileMu.Unlock()
		if err := s.enumPublicationState(taskID, "accepted"); err != nil {
			http.Error(w, "cannot persist publication state", 500)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "accepted", "sha256": contentDigest})
		return
	} else if !os.IsNotExist(e) {
		watcherFileMu.Unlock()
		http.Error(w, "cannot inspect publication receipt", 500)
		return
	}
	storedPath := filepath.Join(publicationDirectory, basename)
	if previous, e := enumFileDigest(storedPath); e == nil {
		if previous != contentDigest {
			watcherFileMu.Unlock()
			s.enumPublicationState(taskID, "conflicted")
			http.Error(w, "publication filename collision", 409)
			return
		}
	} else if !os.IsNotExist(e) {
		watcherFileMu.Unlock()
		http.Error(w, "cannot inspect durable publication", 500)
		return
	} else if _, e = copyEnumImmutable(publicationDirectory, basename, staged.Name()); e != nil {
		watcherFileMu.Unlock()
		http.Error(w, "cannot retain publication", 500)
		return
	}
	path := filepath.Join(directory, basename)
	if previous, e := enumFileDigest(path); e == nil {
		if previous != contentDigest {
			watcherFileMu.Unlock()
			s.enumPublicationState(taskID, "conflicted")
			http.Error(w, "publication filename collision", 409)
			return
		}
	} else if !os.IsNotExist(e) {
		watcherFileMu.Unlock()
		http.Error(w, "cannot inspect result destination", 500)
		return
	} else if _, e = copyEnumImmutable(directory, basename, staged.Name()); e != nil {
		watcherFileMu.Unlock()
		http.Error(w, "cannot persist result", 500)
		return
	}
	receipt, _ := json.Marshal(map[string]string{"sha256": contentDigest, "filename": basename})
	if _, err := version.WriteImmutable(publicationDirectory, taskID+".receipt.json", receipt); err != nil {
		watcherFileMu.Unlock()
		http.Error(w, "cannot persist publication receipt", 500)
		return
	}
	watcherFileMu.Unlock()
	if err := s.enumPublicationState(taskID, "accepted"); err != nil {
		http.Error(w, "cannot persist publication state", 500)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "accepted", "sha256": contentDigest})
}
func (s *Server) enumWatchDirectory() string {
	if s.config.GDrive != nil && s.config.GDrive.WatchDir != "" {
		return ExpandPath(s.config.GDrive.WatchDir)
	}
	return filepath.Join(s.config.DataDir, "merged")
}
func (s *Server) enumPublicationState(taskID, state string) error {
	s.mu.Lock()
	task := s.tasks[taskID]
	if task == nil {
		s.mu.Unlock()
		return fmt.Errorf("unknown enum task")
	}
	if task.EnumPublication == "conflicted" && state == "accepted" {
		s.mu.Unlock()
		return nil
	}
	task.EnumPublication = state
	if state == "conflicted" {
		task.Error = "publication filename collision"
		task.Status = TaskStatusFailed
		s.updateJobProgress(task.JobID)
	}
	copy := *task
	err := s.persistEnumJobLocked(task.JobID)
	s.mu.Unlock()
	s.broadcast(SSEEvent{Type: SSEEventTask, Payload: copy})
	return err
}
