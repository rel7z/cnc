package cnc

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func restoreFixture(t *testing.T, handler driveTestTransport) *GDriveUploader {
	t.Helper()
	dir := t.TempDir()
	service, err := drive.NewService(context.Background(), option.WithHTTPClient(&http.Client{Transport: handler}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &GDriveUploader{config: &GDriveConfig{WatchDir: dir, StateFilePath: filepath.Join(dir, ".state.json"), DeleteAfterUpload: true}, service: service, ctx: ctx, cancelFunc: cancel, uploaded: make(map[string]bool), knownFiles: make(map[string]bool)}
}
func restoreResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func TestDriveRestorePaginationPreservesExistingAndPinsFiles(t *testing.T) {
	content := "historical\n" + strings.Repeat("x", 100000) + "\n"
	checksum := md5.Sum([]byte(content))
	calls := 0
	u := restoreFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Errorf("Restore must not mutate Drive: %s", r.Method)
		}
		if q := r.URL.Query().Get("q"); q != "" {
			if !strings.Contains(q, "'root' in parents") || !strings.Contains(q, "description contains 'SHA256: '") {
				t.Errorf("Unsafe root query: %s", q)
			}
			if r.URL.Query().Get("pageToken") == "" {
				return restoreResponse(r, `{"nextPageToken":"second","files":[{"id":"old","name":"history.txt","size":"`+fmt.Sprint(len(content))+`","md5Checksum":"`+hex.EncodeToString(checksum[:])+`"},{"id":"existing","name":"current.txt"},{"id":"unsafe","name":"../outside.txt"},{"id":"doc","name":"document","mimeType":"application/vnd.google-apps.document"}]}`), nil
			}
			return restoreResponse(r, `{"files":[{"id":"duplicate","name":"history.txt"},{"id":"hidden","name":".credential"}]}`), nil
		}
		calls++
		return restoreResponse(r, content), nil
	})
	os.WriteFile(filepath.Join(u.config.WatchDir, "current.txt"), []byte("new server content"), 0600)
	if err := u.restoreFromDrive(u.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(u.config.WatchDir, "history.txt"))
	if string(got) != content {
		t.Fatal("Restored bytes changed or long line truncated")
	}
	current, _ := os.ReadFile(filepath.Join(u.config.WatchDir, "current.txt"))
	if string(current) != "new server content" {
		t.Fatal("Existing file overwritten")
	}
	st := u.RestoreStatus()
	if st.Restored != 1 || st.Skipped != 5 || calls != 1 {
		t.Fatalf("Unexpected status/downloads: %+v %d", st, calls)
	}
	info, _ := os.Stat(filepath.Join(u.config.WatchDir, "history.txt"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("Restored file must be private")
	}
	reloaded := &GDriveUploader{config: u.config, uploaded: make(map[string]bool), knownFiles: make(map[string]bool)}
	if err := reloaded.loadState(); err != nil {
		t.Fatal(err)
	}
	if reloaded.restored["history.txt"] != sha256Bytes([]byte(content)) {
		t.Fatal("Keep-local state not persisted")
	}
	// Only history is eligible for upload in this scan; it must be pinned.
	os.Remove(filepath.Join(u.config.WatchDir, "current.txt"))
	reloaded.service = u.service
	reloaded.ctx = u.ctx
	reloaded.scanAndUpload()
	if calls != 1 {
		t.Fatal("Restore was uploaded again")
	}
	if _, err := os.Stat(filepath.Join(u.config.WatchDir, "history.txt")); err != nil {
		t.Fatal("Restored file removed by upload cleanup")
	}
}
func TestDriveRestoreFolderScopeAndDownloadFailure(t *testing.T) {
	u := restoreFixture(t, func(r *http.Request) (*http.Response, error) {
		if q := r.URL.Query().Get("q"); q != "" {
			if !strings.Contains(q, "'folder-id' in parents") || strings.Contains(q, "description contains") {
				t.Errorf("Wrong folder query: %s", q)
			}
			return restoreResponse(r, `{"files":[{"id":"broken","name":"broken.txt"}]}`), nil
		}
		return nil, errors.New("interrupted download")
	})
	u.config.ParentFolderID = "folder-id"
	if err := u.restoreFromDrive(u.ctx); err != nil {
		t.Fatal(err)
	}
	if u.RestoreStatus().Failed != 1 {
		t.Fatal("Download failure not counted")
	}
	if _, err := os.Stat(filepath.Join(u.config.WatchDir, "broken.txt")); !os.IsNotExist(err) {
		t.Fatal("Failed file visible")
	}
}
func TestDriveRestoreIntegritySymlinksAndConcurrentCreation(t *testing.T) {
	for _, scenario := range []string{"checksum", "symlink", "concurrent", "state-failure"} {
		t.Run(scenario, func(t *testing.T) {
			u := restoreFixture(t, func(r *http.Request) (*http.Response, error) { return restoreResponse(r, "remote"), nil })
			root, err := os.OpenRoot(u.config.WatchDir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			remote := &drive.File{Id: "one", Name: "copy.txt", Size: 6}
			outside := filepath.Join(t.TempDir(), "outside")
			os.WriteFile(outside, []byte("private"), 0600)
			if scenario == "checksum" {
				remote.Md5Checksum = strings.Repeat("0", 32)
			}
			if scenario == "symlink" {
				os.Symlink(outside, filepath.Join(u.config.WatchDir, remote.Name))
			}
			if scenario == "concurrent" {
				os.WriteFile(filepath.Join(u.config.WatchDir, remote.Name), []byte("worker"), 0600)
			}
			_, _, err = restoreDriveFile(u.ctx, u.service, root, remote, func(string) error {
				if scenario == "state-failure" {
					return errors.New("state write failed")
				}
				return nil
			})
			if err == nil {
				t.Fatal("Expected protected restore failure")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "private" {
				t.Fatal("Symlink destination changed")
			}
			if scenario == "concurrent" {
				data, _ = os.ReadFile(filepath.Join(u.config.WatchDir, remote.Name))
				if string(data) != "worker" {
					t.Fatal("Worker file overwritten")
				}
			}
			entries, _ := os.ReadDir(u.config.WatchDir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".drive-restore-") {
					t.Fatal("Staging file leaked")
				}
			}
		})
	}
}
func TestDriveRestoreAPIAndDuplicateStart(t *testing.T) {
	s := driveSettingsServer(t)
	w := httptest.NewRecorder()
	s.handleGDriveRestore(w, httptest.NewRequest("POST", "/api/config/gdrive/restore", nil))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	u := restoreFixture(t, func(r *http.Request) (*http.Response, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return restoreResponse(r, `{"files":[]}`), nil
	})
	s.gdriveUploader = u
	w = httptest.NewRecorder()
	s.handleGDriveRestore(w, httptest.NewRequest("POST", "/api/config/gdrive/restore", nil))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	<-started
	if err := u.StartRestore(); err == nil {
		t.Fatal("Concurrent restore accepted")
	}
	w = httptest.NewRecorder()
	s.handleGDriveRestore(w, httptest.NewRequest("GET", "/api/config/gdrive/restore", nil))
	var state DriveRestoreStatus
	json.Unmarshal(w.Body.Bytes(), &state)
	if state.Status != "running" {
		t.Fatal(state)
	}
	close(block)
	done := make(chan struct{})
	go func() { u.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Restore did not finish")
	}
	if u.RestoreStatus().Status != "completed" {
		t.Fatal(u.RestoreStatus())
	}
}
