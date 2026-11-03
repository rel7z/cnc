package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/store"
)

type Server struct {
	ResetCheckpoints    func(context.Context) (store.CheckpointReset, error)
	ConfigurationStatus func() (int, int)
	AppDir              string
	EditorApply         func(context.Context, cluster.EditorDefinition, string) (any, error)
	EditorList          func(context.Context) (any, error)
	Store               *store.Store
	Capture             func(context.Context, string) error
	WatchDir            string
	WorkerAPI           http.Handler
	ClusterStatus       http.HandlerFunc
}

func (s *Server) HealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	enabled, err := s.Store.IsEnabled(r.Context())
	if err != nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	counts, err := s.Store.RunCounts(r.Context())
	if err != nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	result := map[string]any{"status": "ok", "enabled": enabled, "counts": counts}
	if s.ConfigurationStatus != nil {
		programs, routes := s.ConfigurationStatus()
		result["configured_programs"], result["configured_routes"], result["watch_dir"] = programs, routes, s.WatchDir
		if programs == 0 {
			result["blocked_reason"] = "No programs configured. Open Setup to configure the processor."
		} else if routes == 0 {
			result["blocked_reason"] = "No input routes configured. Open Setup to select incoming files."
		}
	}
	json.NewEncoder(w).Encode(result)
}

func (s *Server) RunsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	runs, err := s.Store.ListRuns(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	enabled, err := s.Store.IsEnabled(r.Context())
	if err != nil {
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if !enabled {
		for i := range runs {
			if runs[i].Status == "queued" {
				runs[i].BlockedReason = "processing is paused"
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runs)
}

func (s *Server) ProcessingStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Enabled *bool `json:"enabled"`
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.Enabled == nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetEnabled(r.Context(), *req.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"enabled": req.Enabled,
	})
}

// Handler protects all controls and results; an empty secret fails closed.
func (s *Server) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	for _, route := range []string{"GET /v1/processor-templates", "POST /v1/processor-templates", "PUT /v1/processor-templates/{id}", "DELETE /v1/processor-templates/{id}"} {
		mux.HandleFunc(route, s.ProcessorTemplatesHandler)
	}
	mux.HandleFunc("GET /v1/processor-results", s.ProcessorResultsHandler)
	mux.HandleFunc("POST /v1/processor-assets", s.ProcessorAssetsHandler)
	mux.HandleFunc("GET /v1/processor-setups", s.ProcessorSetupHandler)
	mux.HandleFunc("PUT /v1/processor-setups", s.ProcessorSetupHandler)
	mux.HandleFunc("GET /v1/program-priorities", s.ProgramPrioritiesHandler)
	mux.HandleFunc("PUT /v1/program-priorities/{id}", s.ProgramPrioritiesHandler)
	for _, route := range []string{"GET /v1/version-check-templates", "POST /v1/version-check-templates", "GET /v1/version-check-templates/{id}", "PUT /v1/version-check-templates/{id}", "DELETE /v1/version-check-templates/{id}"} {
		mux.HandleFunc(route, s.VersionTemplatesHandler)
	}
	mux.HandleFunc("/v1/health", s.HealthHandler)
	mux.HandleFunc("POST /v1/checkpoints/reset", s.ResetCheckpointsHandler)
	mux.HandleFunc("/v1/capture", s.CaptureHandler)
	mux.HandleFunc("/v1/runs", s.RunsHandler)
	mux.HandleFunc("/v1/processing-state", s.ProcessingStateHandler)
	if s.ClusterStatus != nil {
		mux.HandleFunc("GET /v1/cluster", s.ClusterStatus)
	}
	mux.HandleFunc("POST /v1/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.CancelRun(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Write([]byte(`{"cancelled":true}`))
	})
	mux.HandleFunc("GET /v1/runs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		path, err := s.Store.RunLog(r.Context(), r.PathValue("id"))
		if err != nil || path == "" {
			http.Error(w, "log not available yet", 404)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, "log unavailable", 404)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/plain")
		io.Copy(w, io.LimitReader(f, 4<<20))
	})
	mux.HandleFunc("POST /v1/runs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.RetryRun(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"queued":true}`))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.WorkerAPI != nil && (strings.HasPrefix(r.URL.Path, "/v1/attempts/") || strings.HasPrefix(r.URL.Path, "/v1/bundles/")) {
			s.WorkerAPI.ServeHTTP(w, r)
			return
		}
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// CaptureHandler is a deletion barrier, not an arbitrary-path reader.
func (s *Server) CaptureHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	if s.Capture == nil {
		http.Error(w, "capture unavailable", 503)
		return
	}
	var req struct {
		Filename  string `json:"filename"`
		Directory string `json:"directory"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil || req.Filename == "" {
		http.Error(w, "invalid filename", 400)
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		http.Error(w, "invalid body", 400)
		return
	}
	if s.WatchDir == "" || filepath.Clean(req.Directory) != filepath.Clean(s.WatchDir) {
		http.Error(w, "capture directory mismatch", 409)
		return
	}
	if err := s.Capture(r.Context(), req.Filename); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"captured":true}`))
}
