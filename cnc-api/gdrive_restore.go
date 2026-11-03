package cnc

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
)

// DriveRestoreStatus contains counts only, never file contents or credentials.
type DriveRestoreStatus struct {
	Status   string `json:"status"`
	Scope    string `json:"scope"`
	Restored int    `json:"restored"`
	Skipped  int    `json:"skipped"`
	Failed   int    `json:"failed"`
	Bytes    int64  `json:"bytes"`
	Error    string `json:"error,omitempty"`
}

func (u *GDriveUploader) RestoreStatus() DriveRestoreStatus {
	u.restoreMu.Lock()
	defer u.restoreMu.Unlock()
	state := u.restoreStatus
	if state.Status == "" {
		state.Status = "idle"
	}
	return state
}

func (u *GDriveUploader) StartRestore() error {
	if !u.IsAuthorized() {
		return fmt.Errorf("Google Drive is not connected")
	}
	u.restoreMu.Lock()
	defer u.restoreMu.Unlock()
	if u.restoreStatus.Status == "running" {
		return fmt.Errorf("Drive restore is already running")
	}
	if u.ctx.Err() != nil {
		return fmt.Errorf("Drive uploader is stopping")
	}
	scope := "CNC uploads in Drive root"
	if u.config.ParentFolderID != "" {
		scope = "Configured Drive folder"
	}
	u.restoreStatus = DriveRestoreStatus{Status: "running", Scope: scope}
	ctx, cancel := context.WithTimeout(u.ctx, time.Hour)
	u.restoreCancel = cancel
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		defer cancel()
		err := u.restoreFromDrive(ctx)
		u.markReauthorizationRequired(err)
		u.restoreMu.Lock()
		defer u.restoreMu.Unlock()
		u.restoreStatus.Status = "completed"
		if err != nil {
			u.restoreStatus.Status = "failed"
			u.restoreStatus.Error = "Restore stopped. Check Drive authorization, available disk space, and connection; retry to resume missing files."
		}
		if err == nil && u.restoreStatus.Failed > 0 {
			u.restoreStatus.Status = "completed_with_errors"
		}
		u.restoreCancel = nil
	}()
	return nil
}

func restoreFilename(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "/\\\x00\r\n") && !strings.HasPrefix(name, ".")
}

func (u *GDriveUploader) restoreFromDrive(ctx context.Context) error {
	u.syncMu.Lock()
	defer u.syncMu.Unlock()
	u.authMu.RLock()
	service := u.service
	u.authMu.RUnlock()
	if service == nil {
		return fmt.Errorf("Google Drive is disconnected")
	}
	if err := os.MkdirAll(u.config.WatchDir, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(u.config.WatchDir)
	if err != nil {
		return err
	}
	defer root.Close()
	parent := u.config.ParentFolderID
	if parent == "" {
		parent = "root"
	}
	parent = strings.ReplaceAll(strings.ReplaceAll(parent, "\\", "\\\\"), "'", "\\'")
	query := "trashed = false and '" + parent + "' in parents and mimeType != 'application/vnd.google-apps.folder'"
	if u.config.ParentFolderID == "" {
		query += " and (description contains 'SHA256: ')"
	}
	page := ""
	for {
		response, err := service.Files.List().Q(query).Fields("nextPageToken,files(id,name,mimeType,size,md5Checksum)").
			PageSize(100).PageToken(page).OrderBy("name,modifiedTime desc").SupportsAllDrives(true).IncludeItemsFromAllDrives(true).Context(ctx).Do()
		if err != nil {
			return err
		}
		for _, file := range response.Files {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !restoreFilename(file.Name) || strings.HasPrefix(file.MimeType, "application/vnd.google-apps.") ||
				filepath.Join(u.config.WatchDir, file.Name) == u.config.TokenPath || filepath.Join(u.config.WatchDir, file.Name) == u.config.StateFilePath {
				u.restoreCount("skipped", 0)
				continue
			}
			if _, err := root.Lstat(file.Name); err == nil {
				u.restoreCount("skipped", 0)
				continue
			} else if !os.IsNotExist(err) {
				u.restoreCount("failed", 0)
				continue
			}
			count, _, err := restoreDriveFile(ctx, service, root, file, func(digest string) error {
				u.uploadedMu.Lock()
				if u.restored == nil {
					u.restored = make(map[string]string)
				}
				u.restored[file.Name] = digest
				u.uploadedMu.Unlock()
				return u.saveState()
			})
			switch {
			case errors.Is(err, os.ErrExist):
				u.restoreCount("skipped", 0)
			case err != nil:
				u.restoreCount("failed", 0)
				if ctx.Err() != nil {
					return ctx.Err()
				}
			default:
				u.restoreCount("restored", count)
			}
		}
		page = response.NextPageToken
		if page == "" {
			break
		}
	}
	return nil
}

func (u *GDriveUploader) restoreCount(kind string, size int64) {
	u.restoreMu.Lock()
	defer u.restoreMu.Unlock()
	switch kind {
	case "restored":
		u.restoreStatus.Restored++
		u.restoreStatus.Bytes += size
	case "skipped":
		u.restoreStatus.Skipped++
	case "failed":
		u.restoreStatus.Failed++
	}
}

// Stream into a hidden staging file, verify integrity, then publish without
// overwriting a concurrent worker-created file. Root confines paths to WatchDir.
func restoreDriveFile(ctx context.Context, service *drive.Service, root *os.Root, remote *drive.File, persist func(string) error) (int64, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	response, err := service.Files.Get(remote.Id).SupportsAllDrives(true).Context(ctx).Download()
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	temp := ".drive-restore-" + rand.Text()
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, "", err
	}
	defer root.Remove(temp)
	sha, md := sha256.New(), md5.New()
	count, err := io.Copy(io.MultiWriter(file, sha, md), response.Body)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, "", err
	}
	if remote.Size > 0 && count != remote.Size {
		return 0, "", fmt.Errorf("Drive size mismatch")
	}
	if remote.Md5Checksum != "" && !strings.EqualFold(remote.Md5Checksum, hex.EncodeToString(md.Sum(nil))) {
		return 0, "", fmt.Errorf("Drive checksum mismatch")
	}
	if ctx.Err() != nil {
		return 0, "", ctx.Err()
	}
	digest := hex.EncodeToString(sha.Sum(nil))
	watcherFileMu.Lock()
	defer watcherFileMu.Unlock()
	// Persist keep-local state before publication, so a watcher can never see
	// the restored file without its protection. A crash before Link is retryable.
	if err = persist(digest); err != nil {
		return 0, "", err
	}
	if err = root.Link(temp, remote.Name); err != nil {
		return 0, "", err
	}
	return count, digest, nil
}

func (s *Server) handleGDriveRestore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	u := s.gdriveUploader
	if u == nil {
		http.Error(w, "Configure Google Drive first", http.StatusConflict)
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, u.RestoreStatus())
		return
	}
	if err := u.StartRestore(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, u.RestoreStatus())
}
