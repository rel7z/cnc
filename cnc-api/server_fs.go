package cnc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func (s *Server) handleFSListAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dirPath := r.URL.Query().Get("dir")
	if dirPath == "" {
		dirPath = "."
	}
	
	// Expand path if necessary (e.g. ~)
	dirPath = ExpandPath(dirPath)
	
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		http.Error(w, "Invalid directory path", http.StatusBadRequest)
		return
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read directory: %v", err), http.StatusInternalServerError)
		return
	}

	var files []FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue // Skip if we can't read info (e.g., permissions)
		}
		files = append(files, FileInfo{
			Name:    entry.Name(),
			Path:    filepath.Join(absPath, entry.Name()),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}

	// Sort: Directories first, then alphabetically
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir && !files[j].IsDir {
			return true
		}
		if !files[i].IsDir && files[j].IsDir {
			return false
		}
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"current_dir": absPath,
		"files":       files,
	})
}

func (s *Server) handleFSReadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		http.Error(w, "File parameter is required", http.StatusBadRequest)
		return
	}
	
	filePath = ExpandPath(filePath)

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(content)
}

func (s *Server) handleFSWriteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	filePath := ExpandPath(req.File)
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create parent directory: %v", err), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write file: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleFSDeleteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		http.Error(w, "File parameter is required", http.StatusBadRequest)
		return
	}

	filePath = ExpandPath(filePath)
	if err := os.RemoveAll(filePath); err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete file/directory: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *Server) handleServerExecAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if req.Command == "" {
		http.Error(w, "Command cannot be empty", http.StatusBadRequest)
		return
	}

	// Handle 'cd' commands directly here to return the new CWD to the frontend
	if strings.HasPrefix(strings.TrimSpace(req.Command), "cd ") {
		parts := strings.SplitN(strings.TrimSpace(req.Command), " ", 2)
		if len(parts) == 2 {
			newDir := ExpandPath(parts[1])
			if !filepath.IsAbs(newDir) {
				newDir = filepath.Join(req.Cwd, newDir)
			}
			absDir, err := filepath.Abs(newDir)
			if err != nil {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout": "",
					"stderr": fmt.Sprintf("cd: %v\n", err),
					"cwd":    req.Cwd,
					"exit_code": 1,
				})
				return
			}
			// Check if directory exists
			info, err := os.Stat(absDir)
			if err != nil || !info.IsDir() {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout": "",
					"stderr": fmt.Sprintf("cd: %s: No such file or directory\n", parts[1]),
					"cwd":    req.Cwd,
					"exit_code": 1,
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"stdout": "",
				"stderr": "",
				"cwd":    absDir,
				"exit_code": 0,
			})
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", req.Command)
	if req.Cwd != "" {
		cmd.Dir = ExpandPath(req.Cwd)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
			stderrBuf.WriteString(fmt.Sprintf("\n%v", err))
		}
	}

	// Always return the current cwd
	cwd := req.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stdout":    stdoutBuf.String(),
		"stderr":    stderrBuf.String(),
		"cwd":       cwd,
		"exit_code": exitCode,
	})
}

// handleServerUpdateAPI handles triggering a full git pull, recompile, and restart
// POST /api/server/update
func (s *Server) handleServerUpdateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")

	flusher, hasFlusher := w.(http.Flusher)
	flush := func() {
		if hasFlusher {
			flusher.Flush()
		}
	}

	writeLog := func(msg string) {
		w.Write([]byte(msg + "\n"))
		flush()
	}

	writeLog("[INFO] Initiating server update and recompile...")

	// Locate deploy.py
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, "deploy.py"),
		filepath.Join(cwd, "..", "deploy.py"),
		"/root/cnc/deploy.py",
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(exeDir, "deploy.py"), filepath.Join(exeDir, "..", "deploy.py"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "cnc", "deploy.py"))
	}

	var deployScript string
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				deployScript = abs
				break
			}
		}
	}

	if deployScript == "" {
		writeLog("[ERROR] deploy.py not found in working directory or candidate locations.")
		return
	}

	deployDir := filepath.Dir(deployScript)
	writeLog(fmt.Sprintf("[INFO] Found deployment script at: %s", deployScript))
	writeLog(fmt.Sprintf("[INFO] Working directory: %s", deployDir))
	writeLog("[INFO] Executing: python3 deploy.py --role update")

	cmd := exec.Command("python3", deployScript, "--role", "update")
	cmd.Dir = deployDir
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PATH="+os.Getenv("PATH")+":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin")

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		writeLog(fmt.Sprintf("[ERROR] Failed to get stdout pipe: %v", err))
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		writeLog(fmt.Sprintf("[ERROR] Failed to get stderr pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		writeLog(fmt.Sprintf("[ERROR] Failed to start update process: %v", err))
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			w.Write(append(scanner.Bytes(), '\n'))
			flush()
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			w.Write([]byte("[STDERR] " + scanner.Text() + "\n"))
			flush()
		}
	}()

	wg.Wait()
	err = cmd.Wait()

	if err != nil {
		writeLog(fmt.Sprintf("\n[ERROR] Update failed: %v", err))
	} else {
		writeLog("\n[SUCCESS] Update and rebuild completed successfully! Server is restarting...")
	}
}
