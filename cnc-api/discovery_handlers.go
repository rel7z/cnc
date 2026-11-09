package cnc

import (
	"encoding/json"
	"net/http"
)

// handleDiscoveryAPI routes discovery service requests
func (s *Server) handleDiscoveryAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case "GET":
		// GET /api/discovery/status
		if r.URL.Path == "/api/discovery/status" {
			s.handleDiscoveryStatus(w, r)
		} else {
			http.NotFound(w, r)
		}

	case "POST":
		// POST /api/discovery/start
		if r.URL.Path == "/api/discovery/start" {
			s.handleDiscoveryStart(w, r)
		} else if r.URL.Path == "/api/discovery/stop" {
			s.handleDiscoveryStop(w, r)
		} else {
			http.NotFound(w, r)
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
	}
}

// handleDiscoveryStatus returns current discovery service status
func (s *Server) handleDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	status := s.GetDiscoveryStatus()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// handleDiscoveryStart starts the discovery service
func (s *Server) handleDiscoveryStart(w http.ResponseWriter, r *http.Request) {
	if err := s.StartDiscovery(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	status := s.GetDiscoveryStatus()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// handleDiscoveryStop stops the discovery service
func (s *Server) handleDiscoveryStop(w http.ResponseWriter, r *http.Request) {
	if err := s.StopDiscovery(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	status := s.GetDiscoveryStatus()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// handleDiscoveryResults returns recent discovered sites
func (s *Server) handleDiscoveryResults(w http.ResponseWriter, r *http.Request) {
	// TODO: Query database for recent discoveries
	// Return paginated results, filterable by date/method

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	results := map[string]interface{}{
		"total": 0,
		"limit": 100,
		"sites": []DiscoveredSite{},
	}

	json.NewEncoder(w).Encode(results)
}
