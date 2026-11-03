package cnc

import (
	"crypto/rand"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxUploadSize int64 = 90 << 20

// Each request uploads one file; the UI sends multiple selections separately.
// Publish only a complete file and never replace an existing destination.
func (s *Server) handleFSUploadAPI(w http.ResponseWriter, r *http.Request) {
	s.uploadFile(w, r, maxUploadSize)
}
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, limit int64) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", 405)
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "Choose a destination directory", 400)
		return
	}
	dir, err := filepath.Abs(ExpandPath(dir))
	if err != nil {
		http.Error(w, "Invalid directory", 400)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.Error(w, "Destination directory is unavailable", 404)
		return
	}
	defer root.Close()
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Expected a multipart file upload", 400)
		return
	}
	failRead := func(err error) {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			http.Error(w, "File exceeds the upload size limit", 413)
		} else {
			http.Error(w, "Incomplete or invalid upload", 400)
		}
	}
	part, err := reader.NextPart()
	if err != nil {
		failRead(err)
		return
	}
	defer part.Close()
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	name := params["filename"]
	if err != nil || params["name"] != "file" || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || len(name) > 255 {
		http.Error(w, "Invalid filename", 400)
		return
	}
	tempName := ".cnc-upload-" + rand.Text()
	file, err := root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		http.Error(w, "Cannot write to this directory", 403)
		return
	}
	defer root.Remove(tempName)
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(part, limit+1))
	if err != nil {
		failRead(err)
		return
	}
	if size > limit {
		http.Error(w, "File exceeds the upload size limit", 413)
		return
	}
	// Reject additional parts before publishing; no partial successes.
	if extra, err := reader.NextPart(); err != io.EOF {
		if extra != nil {
			extra.Close()
		}
		http.Error(w, "Upload exactly one file per request", 400)
		return
	}
	if err := r.Context().Err(); err != nil {
		http.Error(w, "Upload cancelled", 400)
		return
	}
	if err := file.Sync(); err != nil {
		http.Error(w, "Failed to save uploaded file", 500)
		return
	}
	if err := file.Close(); err != nil {
		http.Error(w, "Failed to save uploaded file", 500)
		return
	}
	watcherFileMu.Lock()
	err = root.Link(tempName, name)
	watcherFileMu.Unlock()
	if errors.Is(err, os.ErrExist) {
		http.Error(w, "A file with this name already exists. Rename it before uploading.", 409)
		return
	}
	if err != nil {
		http.Error(w, "Failed to publish uploaded file", 500)
		return
	}
	if uploader := s.driveUploader(); uploader != nil {
		uploader.NotifyResults()
	}
	writeJSON(w, http.StatusCreated, FileInfo{Name: name, Path: filepath.Join(dir, name), Size: size, ModTime: time.Now().Format(time.RFC3339)})
}
