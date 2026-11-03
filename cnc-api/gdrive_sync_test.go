package cnc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func newDeletionTestUploader(t *testing.T, watchDir string, handler http.HandlerFunc) (*GDriveUploader, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	service, err := drive.NewService(context.Background(),
		option.WithEndpoint(server.URL+"/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(server.Client()))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	u := &GDriveUploader{
		config:     &GDriveConfig{WatchDir: watchDir, StateFilePath: filepath.Join(watchDir, ".gdrive_state.json")},
		service:    service,
		uploaded:   make(map[string]bool),
		knownFiles: make(map[string]bool),
		ctx:        context.Background(),
	}
	return u, server.Close
}

func TestDriveDeletionMirrorsLocalDeletionToTrash(t *testing.T) {
	watch := t.TempDir()
	trashed := false
	u, closeServer := newDeletionTestUploader(t, watch, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files"):
			_, _ = w.Write([]byte(`{"files":[{"id":"drive-1","name":"gone.txt"},{"id":"drive-2","name":"gone.txt"}]}`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/files/drive-1"),
			r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/files/drive-2"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["trashed"] != true {
				t.Errorf("expected Drive update to set trashed=true, got %v", body)
			}
			trashed = true
			_, _ = w.Write([]byte(`{"id":"drive-1","trashed":true}`))
		default:
			t.Errorf("unexpected Drive request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	})
	defer closeServer()
	u.knownFiles["gone.txt"] = true
	u.uploaded["gone.txt:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"] = true

	if err := u.syncDeletions(); err != nil {
		t.Fatal(err)
	}
	if !trashed {
		t.Fatal("Drive copies were not moved to Trash")
	}
	if u.knownFiles["gone.txt"] || len(u.uploaded) != 0 {
		t.Fatal("deleted file remained in sync state")
	}
}

func TestDriveDeletionMovesLocalCopyToHiddenRecovery(t *testing.T) {
	watch := t.TempDir()
	path := filepath.Join(watch, "gone.txt")
	if err := os.WriteFile(path, []byte("keep this for recovery\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	u, closeServer := newDeletionTestUploader(t, watch, func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"files":[]}`))
	})
	defer closeServer()
	u.knownFiles["gone.txt"] = true

	if err := u.syncDeletions(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("local active file still exists: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(watch, ".cnc-deleted", "*", "gone.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one hidden recovery copy, matches=%v err=%v", matches, err)
	}
	contents, err := os.ReadFile(matches[0])
	if err != nil || string(contents) != "keep this for recovery\n" {
		t.Fatalf("recovery copy mismatch: %q err=%v", contents, err)
	}
	if requests != 1 || u.knownFiles["gone.txt"] {
		t.Fatal("remote deletion was not reconciled exactly once")
	}
}

func TestUntrackedDriveFileIsNotDeletedAndDeleteAfterUploadIsPreserved(t *testing.T) {
	for _, deleteAfterUpload := range []bool{false, true} {
		t.Run(map[bool]string{false: "untracked", true: "delete_after_upload"}[deleteAfterUpload], func(t *testing.T) {
			watch := t.TempDir()
			updated := false
			u, closeServer := newDeletionTestUploader(t, watch, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"files":[{"id":"drive-1","name":"remote.txt"}]}`))
					return
				}
				updated = true
				_, _ = w.Write([]byte(`{"id":"drive-1","trashed":true}`))
			})
			defer closeServer()
			u.config.DeleteAfterUpload = deleteAfterUpload
			if !deleteAfterUpload {
				// A Drive-only object unknown to CNC is outside the mirror's delete scope.
				if err := u.syncDeletions(); err != nil {
					t.Fatal(err)
				}
				if updated || u.knownFiles["remote.txt"] {
					t.Fatal("untracked Drive file was unexpectedly changed")
				}
				return
			}
			u.knownFiles["remote.txt"] = true
			if err := u.syncDeletions(); err != nil {
				t.Fatal(err)
			}
			if updated || !u.knownFiles["remote.txt"] {
				t.Fatal("delete-after-upload remote copy was unexpectedly removed")
			}
		})
	}
}

func TestLegacyDriveStateMigratesKnownFilenames(t *testing.T) {
	watch := t.TempDir()
	statePath := filepath.Join(watch, ".gdrive_state.json")
	legacy := `["results.txt:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]`
	if err := os.WriteFile(statePath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	u := &GDriveUploader{config: &GDriveConfig{StateFilePath: statePath}, uploaded: map[string]bool{}, knownFiles: map[string]bool{}}
	if err := u.loadState(); err != nil {
		t.Fatal(err)
	}
	if !u.knownFiles["results.txt"] {
		t.Fatal("legacy state did not recover its synchronized filename")
	}
	if err := u.saveState(); err != nil {
		t.Fatal(err)
	}
	var saved persistedGDriveState
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || len(saved.KnownFiles) != 1 || saved.KnownFiles[0] != "results.txt" {
		t.Fatalf("unexpected migrated state: %+v", saved)
	}
}

func TestRevokedDriveTokenClearsAuthorizationStatus(t *testing.T) {
	u := &GDriveUploader{service: &drive.Service{}}
	if !u.IsAuthorized() {
		t.Fatal("fixture should begin authorized")
	}
	u.markReauthorizationRequired(errors.New("oauth2: invalid_grant Token has been expired or revoked"))
	if u.IsAuthorized() || u.ConnectedEmail() != "" {
		t.Fatal("revoked token was still reported as connected")
	}
}
