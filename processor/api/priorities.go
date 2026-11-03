package api

import (
	"encoding/json"
	"errors"
	"github.com/user/cnc-processor/internal/store"
	"io"
	"net/http"
)

func (s *Server) ProgramPrioritiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		p, err := s.Store.ProgramPriorities(r.Context())
		if err != nil {
			templateResponse(w, 503, map[string]string{"error": "Tool priorities are unavailable."})
			return
		}
		templateResponse(w, 200, p)
		return
	}
	var body struct {
		Rank     json.RawMessage `json:"rank"`
		Revision int64           `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Revision < 1 || len(body.Rank) == 0 {
		templateResponse(w, 400, map[string]string{"error": "Provide rank (1–5 or null to reset) and a positive revision."})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		templateResponse(w, 400, map[string]string{"error": "Expected one priority request."})
		return
	}
	var rank *int
	if err := json.Unmarshal(body.Rank, &rank); err != nil || (rank != nil && (*rank < 1 || *rank > 5)) {
		templateResponse(w, 400, map[string]string{"error": "Rank must be an integer from 1 to 5, or null to reset."})
		return
	}
	p, err := s.Store.UpdateProgramPriority(r.Context(), r.PathValue("id"), rank, body.Revision)
	if err != nil {
		status := 503
		message := "Tool priority could not be saved. Your edits are preserved."
		if errors.Is(err, store.ErrPriorityNotFound) {
			status = 404
			message = err.Error()
		}
		if errors.Is(err, store.ErrPriorityConflict) {
			status = 409
			message = err.Error()
		}
		templateResponse(w, status, map[string]string{"error": message})
		return
	}
	templateResponse(w, 200, p)
}
