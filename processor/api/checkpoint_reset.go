package api

import (
	"errors"
	"net/http"

	"github.com/user/cnc-processor/internal/store"
)

func (s *Server) ResetCheckpointsHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !decodeEditorRequest(w, r, &req) {
		return
	}
	if req.Confirm != "reset-checkpoints" {
		templateResponse(w, 400, map[string]string{"error": "Confirm reset-checkpoints to reread existing input after processing is enabled."})
		return
	}
	if s.ResetCheckpoints == nil {
		templateResponse(w, 503, map[string]string{"error": "Checkpoint reset is unavailable."})
		return
	}
	result, err := s.ResetCheckpoints(r.Context())
	if err != nil {
		code := 500
		message := "Could not reset checkpoints. Existing checkpoints were preserved."
		if errors.Is(err, store.ErrResetBusy) {
			code = 409
			message = err.Error()
		}
		templateResponse(w, code, map[string]string{"error": message})
		return
	}
	templateResponse(w, 200, result)
}
