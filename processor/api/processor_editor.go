package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/user/cnc-processor/cluster"
)

func decodeEditorRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		templateResponse(w, 400, map[string]string{"error": "Invalid processor configuration (maximum 256 KiB)."})
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		templateResponse(w, 400, map[string]string{"error": "Expected one configuration."})
		return false
	}
	return true
}
func (s *Server) ProcessorTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		items, err := s.Store.ProcessorTemplates(r.Context())
		if err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 200, items)
		return
	}
	var req struct {
		Name       string                   `json:"name"`
		Definition cluster.EditorDefinition `json:"definition"`
		Revision   int64                    `json:"revision"`
	}
	if !decodeEditorRequest(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if r.Method == "DELETE" {
		if req.Revision < 1 {
			templateResponse(w, 400, map[string]string{"error": "Template revision is required."})
			return
		}
		if err := s.Store.DeleteProcessorTemplate(r.Context(), id, req.Revision); err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 200, map[string]bool{"deleted": true})
		return
	}
	if err := cluster.ValidateEditorDefinition(req.Definition); err != nil {
		templateResponse(w, 400, map[string]string{"error": err.Error()})
		return
	}
	raw, _ := json.Marshal(req.Definition)
	t, err := s.Store.SaveProcessorTemplate(r.Context(), id, req.Name, raw, req.Revision)
	if err != nil {
		templateError(w, err)
		return
	}
	templateResponse(w, 200, t)
}
func (s *Server) ProcessorResultsHandler(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	run := r.URL.Query().Get("run")
	if name == "" {
		items, err := s.Store.ProcessorResults(r.Context(), run)
		if err != nil {
			templateError(w, err)
			return
		}
		templateResponse(w, 200, items)
		return
	}
	if !cluster.SafeRelative(name) || filepath.Base(name) != name || !strings.HasSuffix(name, ".txt") {
		http.Error(w, "invalid result filename", 400)
		return
	}
	// Release the SQLite cursor before streaming to a potentially slow browser.
	file, err := os.CreateTemp("", "cnc-result-*.txt")
	if err != nil {
		http.Error(w, "download unavailable", 503)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	count, err := s.Store.WriteProcessorResult(r.Context(), name, run, file)
	if err != nil {
		http.Error(w, "download unavailable", 503)
		return
	}
	if count == 0 {
		http.Error(w, "result not found", 404)
		return
	}
	file.Seek(0, io.SeekStart)
	info, _ := file.Stat()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), file)
}
func (s *Server) ProcessorAssetsHandler(w http.ResponseWriter, r *http.Request) {
	if s.AppDir == "" {
		templateResponse(w, 503, map[string]string{"error": "Program uploads are unavailable."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		templateResponse(w, 400, map[string]string{"error": "Choose program files to upload."})
		return
	}
	relative := filepath.Join("ui-programs", "assets", uuid.NewString())
	root := filepath.Join(s.AppDir, relative)
	if err := os.MkdirAll(root, 0700); err != nil {
		templateResponse(w, 503, map[string]string{"error": "Upload storage unavailable."})
		return
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(root)
		}
	}()
	paths := []string{}
	total := int64(0)
	seen := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			templateResponse(w, 400, map[string]string{"error": "Upload incomplete or exceeds 64 MiB."})
			return
		}
		path := strings.TrimPrefix(part.FormName(), "file:")
		if !strings.HasPrefix(part.FormName(), "file:") || !cluster.SafeRelative(path) || seen[path] || len(paths) >= 256 {
			part.Close()
			templateResponse(w, 400, map[string]string{"error": "Invalid or duplicate file path (maximum 256 files)."})
			return
		}
		seen[path] = true
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			templateResponse(w, 400, map[string]string{"error": "Conflicting uploaded file paths."})
			return
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			templateResponse(w, 400, map[string]string{"error": "Conflicting uploaded file paths."})
			return
		}
		n, err := io.Copy(file, io.LimitReader(part, (64<<20)-total+1))
		closeErr := file.Close()
		part.Close()
		total += n
		if err != nil || closeErr != nil || total > 64<<20 {
			templateResponse(w, 400, map[string]string{"error": "Upload failed or exceeds 64 MiB."})
			return
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		templateResponse(w, 400, map[string]string{"error": "Choose at least one program file."})
		return
	}
	ok = true
	templateResponse(w, 201, map[string]any{"folder": relative, "files": paths})
}
func (s *Server) ProcessorSetupHandler(w http.ResponseWriter, r *http.Request) {
	if s.EditorApply == nil || s.EditorList == nil {
		templateResponse(w, 503, map[string]string{"error": "Program configuration unavailable."})
		return
	}
	if r.Method == "GET" {
		data, err := s.EditorList(r.Context())
		if err != nil {
			templateResponse(w, 503, map[string]string{"error": err.Error()})
			return
		}
		templateResponse(w, 200, data)
		return
	}
	var req struct {
		Definition cluster.EditorDefinition `json:"definition"`
		Revision   string                   `json:"revision"`
	}
	if !decodeEditorRequest(w, r, &req) {
		return
	}
	if err := cluster.ValidateEditorDefinition(req.Definition); err != nil {
		templateResponse(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if req.Definition.Folder == "" {
		templateResponse(w, 400, map[string]string{"error": "Upload files or enter their server folder before applying."})
		return
	}
	data, err := s.EditorApply(r.Context(), req.Definition, req.Revision)
	if err != nil {
		templateResponse(w, 409, map[string]string{"error": fmt.Sprint(err)})
		return
	}
	templateResponse(w, 200, data)
}
