package cnc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The processor remains private to this host. Only the dashboard proxy can
// reach this integration; its bearer secret never goes to the browser.
func (s *Server) handleProcessorAPI(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("CNC_PROCESSOR_ENABLED") != "1" {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/processor/attempts/") || strings.HasPrefix(r.URL.Path, "/api/processor/bundles/") {
		s.proxyProcessorWorker(w, r)
		return
	}
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	local := ""
	if s.config != nil {
		local, _, _ = net.SplitHostPort(s.config.HTTPAddr)
	}
	if !linodeLocalCaller(remote, local) {
		http.Error(w, "processor management requires the local dashboard proxy", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/processor/cluster/") {
		s.handleProcessorBroker(w, r)
		return
	}
	routes := map[string]string{
		"/api/processor/checkpoints/reset": "POST",
		"/api/processor/program-priorities": "GET",
		"/api/processor/cluster":            "GET",
		"/api/processor/health":             "GET",
		"/api/processor/runs":               "GET",
		"/api/processor/processing-state":   "PUT",
	}
	method, ok := routes[r.URL.Path]
	if strings.HasPrefix(r.URL.Path, "/api/processor/runs/") && (strings.HasSuffix(r.URL.Path, "/retry") || strings.HasSuffix(r.URL.Path, "/cancel") || strings.HasSuffix(r.URL.Path, "/log")) {
		method = "POST"
		if strings.HasSuffix(r.URL.Path, "/log") {
			method = "GET"
		}
		ok = true
	}
	if strings.HasPrefix(r.URL.Path, "/api/processor/program-priorities/") && strings.TrimPrefix(r.URL.Path, "/api/processor/program-priorities/") != "" && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/api/processor/program-priorities/"), "/") {
		method = "PUT"
		ok = true
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	token := os.Getenv("CNC_PROCESSOR_TOKEN")
	if len(token) < 32 {
		http.Error(w, "processor secret is not configured", 503)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/processor")
	req, err := http.NewRequestWithContext(r.Context(), method, "http://127.0.0.1:9191/v1"+path, http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		http.Error(w, "invalid processor request", 400)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "processor unavailable; processing state is unknown", 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 2<<20))
}

// processorCaptureBeforeDelete must run while watcherFileMu is held, so CNC
// writers cannot append between the capture acknowledgment and unlink.
func processorCaptureBeforeDelete(path string) error {
	if os.Getenv("CNC_PROCESSOR_ENABLED") != "1" {
		return nil
	}
	token := os.Getenv("CNC_PROCESSOR_TOKEN")
	if len(token) < 32 {
		return fmt.Errorf("processor secret not configured")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"filename": filepath.Base(absolute), "directory": filepath.Dir(absolute)})
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:9191/v1/capture", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("processor capture unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("processor capture not acknowledged (HTTP %d)", resp.StatusCode)
	}
	var result struct {
		Captured bool `json:"captured"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil || !result.Captured {
		return fmt.Errorf("invalid processor capture acknowledgment")
	}
	return nil
}
