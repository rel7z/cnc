package cnc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cnc-enum/version"
	"github.com/user/cnc-processor/cluster"
)

func (w *WorkerAgent) ensureEnumGateway() (*version.Gateway, error) {
	w.enumGatewayMu.Lock()
	defer w.enumGatewayMu.Unlock()
	if w.enumGateway != nil {
		return w.enumGateway, nil
	}
	limit, err := version.BudgetLimit(os.Getenv("ENUM_MAX_INFLIGHT_REQUESTS"))
	if err != nil {
		return nil, err
	}
	gateway, err := version.StartGateway(limit)
	if err != nil {
		return nil, err
	}
	w.enumGateway = gateway
	return gateway, nil
}
func (w *WorkerAgent) enumBudgetSnapshot() map[string]version.BudgetMetrics {
	w.enumGatewayMu.Lock()
	gateway := w.enumGateway
	w.enumGatewayMu.Unlock()
	if gateway == nil {
		return nil
	}
	return gateway.Budget.Snapshot()
}

func (w *WorkerAgent) enumRoot() string {
	root, err := filepath.Abs(filepath.Join(w.config.DataDir, "enum"))
	if err != nil {
		return filepath.Join(w.config.DataDir, "enum")
	}
	return root
}
func enumPayloadString(task *Task, key string) string {
	value, _ := task.Payload[key].(string)
	return value
}
func enumPayloadInt(task *Task, key string, fallback int) int {
	switch value := task.Payload[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	}
	return fallback
}

func (w *WorkerAgent) downloadEnumFile(ctx context.Context, url, token, path string, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.getServerHTTPAddr()+url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Enum-Job-Token", token)
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("enum input transfer failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("enum input transfer rejected (%d)", response.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	n, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if err != nil || n > limit {
		return fmt.Errorf("enum transfer is incomplete or oversized")
	}
	return file.Sync()
}

func (w *WorkerAgent) executeEnumTask(task *Task) (*TaskResult, error) {
	if !version.ValidTransportID(task.ID) || !version.ValidTransportID(task.JobID) {
		return nil, fmt.Errorf("invalid enum task binding")
	}
	timeout := enumPayloadInt(task, "timeout_seconds", NoTimeout)
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout == NoTimeout {
		ctx, cancel = context.WithCancel(w.ctx)
	} else if timeout > 0 {
		ctx, cancel = context.WithTimeout(w.ctx, time.Duration(timeout)*time.Second)
	} else {
		return nil, fmt.Errorf("invalid enum timeout")
	}
	w.taskCancelsMu.Lock()
	w.taskCancels[task.ID] = cancel
	w.taskCancelsMu.Unlock()
	defer func() { cancel(); w.taskCancelsMu.Lock(); delete(w.taskCancels, task.ID); w.taskCancelsMu.Unlock() }()
	root := w.enumRoot()
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(root, task.ID+"-")
	if err != nil {
		return nil, err
	}
	artifactPath := filepath.Join(directory, "artifact.json")
	token := enumPayloadString(task, "enum_token")
	if token == "" {
		return nil, fmt.Errorf("missing enum transfer authorization")
	}
	if err = w.downloadEnumFile(ctx, enumPayloadString(task, "enum_artifact_url"), token, artifactPath, 8*1024*1024); err != nil {
		return nil, err
	}
	artifact, err := version.LoadArtifact(artifactPath, enumPayloadString(task, "enum_artifact_sha256"), task.JobID)
	if err != nil {
		return nil, fmt.Errorf("invalid enum artifact: %w", err)
	}
	inputPath := filepath.Join(directory, "input.txt")
	if err = w.downloadEnumFile(ctx, enumPayloadString(task, "enum_input_url"), token, inputPath, 256*1024*1024); err != nil {
		return nil, err
	}
	threads, requestTimeout := enumPayloadInt(task, "threads", 8), enumPayloadInt(task, "request_timeout_ms", 10000)
	if threads < 1 || threads > 256 || requestTimeout < 1000 || requestTimeout > 120000 {
		return nil, fmt.Errorf("invalid enum resource settings")
	}
	output := filepath.Join(directory, "output")
	args := []string{"-l", inputPath, "-t", strconv.Itoa(threads), "-timeout", (time.Duration(requestTimeout) * time.Millisecond).String(), "--version-rules-file", artifactPath, "--version-rules-sha256", enumPayloadString(task, "enum_artifact_sha256"), "--enum-job-id", task.JobID, "--enum-shard-id", task.ID, "--enum-output-dir", output}
	automatic, _ := task.Payload["auto_concurrency"].(bool)
	args = append(args, "-auto-concurrency="+strconv.FormatBool(automatic))
	for _, wordlist := range []struct{ key, name, flag string }{{"enum_wp_plugins_url", "wp_plugins.txt", "-wp-plugins"}, {"enum_joomla_exts_url", "joomla_exts.txt", "-joomla-exts"}} {
		if url := enumPayloadString(task, wordlist.key); url != "" {
			path := filepath.Join(directory, wordlist.name)
			if err = w.downloadEnumFile(ctx, url, token, path, 8*1024*1024); err != nil {
				return nil, err
			}
			args = append(args, wordlist.flag, path)
		}
	}
	gateway, err := w.ensureEnumGateway()
	if err != nil {
		return nil, err
	}
	evidenceToken, unregister, err := gateway.Register(ctx, artifact)
	if err != nil {
		return nil, err
	}
	defer unregister()
	binary := os.Getenv("CNC_ENUM_BINARY")
	if binary == "" {
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		binary = filepath.Join(filepath.Dir(exe), "tools", "cnc-enum")
		if _, e = os.Stat(binary); e != nil {
			binary, err = filepath.Abs("tools/cnc-enum")
			if err != nil {
				return nil, err
			}
		}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "ENUM_EVIDENCE_GATEWAY="+gateway.URL, "ENUM_EVIDENCE_TOKEN="+evidenceToken)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cleanupLimits, limitErr := cluster.ConfigureCommandLimits(command, "enum-"+task.ID, w.taskExecutionResources(task))
	if limitErr != nil {
		return nil, fmt.Errorf("configure enum job resource limits: %w", limitErr)
	}
	defer cleanupLimits()
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	stdout, stderr := &enumLogBuffer{}, &enumLogBuffer{}
	stdoutStream := &BufferedStreamWriter{w: w, taskID: task.ID}
	stderrStream := &BufferedStreamWriter{w: w, taskID: task.ID, isStderr: true}
	command.Stdout = io.MultiWriter(stdout, stdoutStream)
	command.Stderr = io.MultiWriter(stderr, stderrStream)
	runErr := command.Run()
	stdoutStream.Close()
	stderrStream.Close()
	exit := 0
	if runErr != nil {
		exit = 1
		if e, ok := runErr.(*exec.ExitError); ok {
			exit = e.ExitCode()
		}
	}
	result := &TaskResult{Stdout: string(stdout.data), Stderr: string(stderr.data), ExitCode: exit, PublicationStatus: "none"}
	basename, _ := version.ResultBasename(task.JobID, task.ID)
	path := filepath.Join(output, basename)
	if _, err := os.Stat(path); err == nil {
		entry := enumOutbox{TaskID: task.ID, JobID: task.JobID, WorkerID: w.config.WorkerID, Token: token, Path: path, Basename: basename}
		file, err := os.Open(path)
		if err != nil {
			return result, err
		}
		digest := sha256.New()
		_, err = io.Copy(digest, file)
		file.Close()
		if err != nil {
			return result, err
		}
		entry.SHA256 = hex.EncodeToString(digest.Sum(nil))
		outbox, err := w.persistEnumPublication(entry)
		if err != nil {
			return result, err
		}
		result.PublicationStatus = "pending"
		w.startEnumPublication(w.ctx, outbox, entry)
	}
	return result, nil
}

type enumLogBuffer struct{ data []byte }

func (b *enumLogBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > 64*1024 {
		b.data = append([]byte{}, b.data[len(b.data)-64*1024:]...)
	}
	return n, nil
}

type enumOutbox struct{ TaskID, JobID, WorkerID, Token, Path, Basename, SHA256 string }

func (w *WorkerAgent) persistEnumPublication(entry enumOutbox) (string, error) {
	directory := filepath.Join(w.enumRoot(), "outbox")
	data, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	return version.WriteImmutable(directory, entry.TaskID+"-"+entry.SHA256+".json", data)
}
func (w *WorkerAgent) deliverEnumPublication(ctx context.Context, entry enumOutbox) error {
	file, err := os.Open(entry.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.getServerHTTPAddr()+"/api/enum/results/"+entry.TaskID, file)
	if err != nil {
		return err
	}
	req.Header.Set("X-Enum-Job-Token", entry.Token)
	req.Header.Set("X-Worker-ID", entry.WorkerID)
	req.Header.Set("X-Enum-Filename", entry.Basename)
	req.Header.Set("X-Enum-SHA256", entry.SHA256)
	req.Header.Set("Content-Type", "application/x-ndjson")
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("enum publication transfer failed")
	}
	defer response.Body.Close()
	if response.StatusCode == 409 {
		return fmt.Errorf("enum publication collision")
	}
	if response.StatusCode != 200 {
		return fmt.Errorf("enum publication rejected (%d)", response.StatusCode)
	}
	var ack struct{ Status, SHA256 string }
	if json.NewDecoder(response.Body).Decode(&ack) != nil || ack.Status != "accepted" || ack.SHA256 != entry.SHA256 {
		return fmt.Errorf("invalid enum publication acknowledgement")
	}
	return nil
}
func (w *WorkerAgent) retryEnumPublications(ctx context.Context) {
	paths, _ := filepath.Glob(filepath.Join(w.enumRoot(), "outbox", "*.json"))
	for _, path := range paths {
		if ctx.Err() != nil {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var entry enumOutbox
		if json.Unmarshal(data, &entry) != nil {
			continue
		}
		w.startEnumPublication(ctx, path, entry)
	}
}

// Transfers have their own bounded concurrency and do not occupy CNC execution
// slots or block processor publication retries. The outbox remains authoritative
// across worker shutdown, failed attempts, and controller outages.
func (w *WorkerAgent) startEnumPublication(ctx context.Context, path string, entry enumOutbox) bool {
	w.enumTransferMu.Lock()
	if ctx.Err() != nil || w.enumTransfers[path] || len(w.enumTransfers) >= 2 {
		w.enumTransferMu.Unlock()
		return false
	}
	if w.enumTransfers == nil {
		w.enumTransfers = map[string]bool{}
	}
	w.enumTransfers[path] = true
	w.wg.Add(1)
	w.enumTransferMu.Unlock()
	go func() {
		defer w.wg.Done()
		defer func() {
			w.enumTransferMu.Lock()
			delete(w.enumTransfers, path)
			w.enumTransferMu.Unlock()
		}()
		if err := w.deliverEnumPublication(ctx, entry); err == nil {
			os.Remove(path)
		} else if strings.Contains(err.Error(), "collision") {
			// The controller has durably failed the shard. Retain its local
			// outbox metadata and bytes, but do not retry a permanent collision.
			conflicts := filepath.Join(w.enumRoot(), "conflicted-publications")
			if os.MkdirAll(conflicts, 0700) == nil {
				os.Rename(path, filepath.Join(conflicts, filepath.Base(path)))
			}
		}
	}()
	return true
}
