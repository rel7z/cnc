package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/user/cnc-processor/internal/store"
)

func templateResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func templateError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	message := "Template storage is unavailable. Try again."
	switch {
	case errors.Is(err, store.ErrTemplateNotFound):
		status = 404
		message = err.Error()
	case errors.Is(err, store.ErrTemplateConflict), errors.Is(err, store.ErrTemplateName):
		status = 409
		message = err.Error()
	}
	templateResponse(w, status, map[string]string{"error": message})
}
func (s *Server) VersionTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == "GET" {
		if id != "" {
			t, err := s.Store.GetVersionTemplate(r.Context(), id)
			if err != nil {
				templateError(w, err)
				return
			}
			templateResponse(w, 200, t)
			return
		}
		templates, err := s.Store.ListVersionTemplates(r.Context())
		if err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 200, templates)
		return
	}
	var body struct {
		Name     string                `json:"name"`
		Checks   []store.TemplateCheck `json:"checks"`
		Revision int64                 `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		templateResponse(w, 400, map[string]string{"error": "Invalid template request (maximum 8 MiB)."})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		templateResponse(w, 400, map[string]string{"error": "Expected one template request."})
		return
	}
	if r.Method != "POST" && body.Revision < 1 {
		templateResponse(w, 400, map[string]string{"error": "Template revision is required."})
		return
	}
	if r.Method == "DELETE" {
		if err := s.Store.DeleteVersionTemplate(r.Context(), id, body.Revision); err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 200, map[string]bool{"deleted": true})
		return
	}
	if err := store.ValidateTemplate(body.Name, body.Checks); err != nil {
		templateResponse(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if r.Method == "POST" {
		t, err := s.Store.CreateVersionTemplate(r.Context(), body.Name, body.Checks)
		if err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 201, t)
	} else {
		t, err := s.Store.UpdateVersionTemplate(r.Context(), id, body.Name, body.Checks, body.Revision)
		if err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 200, t)
	}
}
