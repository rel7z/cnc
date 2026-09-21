package cnc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

	// Capture output
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to start command: %v", err), http.StatusInternalServerError)
		return
	}

	stdout, _ := io.ReadAll(stdoutPipe)
	stderr, _ := io.ReadAll(stderrPipe)

	err := cmd.Wait()
	
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
			stderr = append(stderr, []byte(fmt.Sprintf("\n%v", err))...)
		}
	}

	// Always return the current cwd
	cwd := req.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stdout":    string(stdout),
		"stderr":    string(stderr),
		"cwd":       cwd,
		"exit_code": exitCode,
	})
}
