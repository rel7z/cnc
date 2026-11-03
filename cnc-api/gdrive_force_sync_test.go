package cnc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDriveManualSyncPublishesRestoredCopies(t *testing.T) {
	dir := t.TempDir()
	data := []byte("retained local data\n")
	name := "restored.txt"
	os.WriteFile(filepath.Join(dir, name), data, 0600)
	hash := sha256Bytes(data)
	u := &GDriveUploader{config: &GDriveConfig{WatchDir: dir, StateFilePath: filepath.Join(dir, ".state.json")}, uploaded: map[string]bool{}, knownFiles: map[string]bool{name: true, "unrelated.txt": true}, restored: map[string]string{name: hash}, dirty: make(chan struct{}, 1)}
	if u.countPendingFiles() != 0 {
		t.Fatal("restored file incorrectly reported pending")
	}
	if err := u.RequestFullSync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := u.restored[name]; ok {
		t.Fatal("manual sync did not release restore-only flag")
	}
	if u.knownFiles[name] || !u.knownFiles["unrelated.txt"] {
		t.Fatal("manual upload would mirror deletion or disturb unrelated state")
	}
	if u.countPendingFiles() != 1 {
		t.Fatal("manual sync did not queue restored file")
	}
	got, _ := os.ReadFile(filepath.Join(dir, name))
	if string(got) != string(data) {
		t.Fatal("local file modified")
	}
	select {
	case <-u.dirty:
	default:
		t.Fatal("uploader not notified")
	}
}

func TestDriveManualSyncFailureRetainsRestoreProtection(t *testing.T) {
	dir := t.TempDir()
	name := "restored.txt"
	data := []byte("keep\n")
	os.WriteFile(filepath.Join(dir, name), data, 0600)
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0600)
	u := &GDriveUploader{config: &GDriveConfig{WatchDir: dir, StateFilePath: filepath.Join(blocker, "state")}, uploaded: map[string]bool{name + ":" + sha256Bytes(data): true}, knownFiles: map[string]bool{name: true}, restored: map[string]string{name: sha256Bytes(data)}, dirty: make(chan struct{}, 1)}
	if u.RequestFullSync() == nil || u.restored[name] == "" || !u.knownFiles[name] {
		t.Fatal("failed persistence lost restore protection")
	}
}
