package cnc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDriveBundleHashDetectsReplacementWithSameSizeAndTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "internet-map-backup-1795379418-0123456789abcdef0123456789abcdef.tar.gz")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	u := &GDriveUploader{}
	first, err := u.hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".new", []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path+".new", info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
	second, err := u.hashFile(path)
	if err != nil || first == second || second != sha256Bytes([]byte("other")) {
		t.Fatalf("replacement hash was not refreshed: %q, %v", second, err)
	}
	if err := os.WriteFile(path, []byte("larger than before"), 0600); err != nil {
		t.Fatal(err)
	}
	third, err := u.hashFile(path)
	if err != nil || third != sha256Bytes([]byte("larger than before")) {
		t.Fatalf("in-place update hash was not refreshed: %q, %v", third, err)
	}
}

func TestDriveMutableExportsAlwaysHashActualBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.txt")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	u := &GDriveUploader{}
	if _, err := u.hashFile(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	digest, err := u.hashFile(path)
	if err != nil || digest != sha256Bytes([]byte("other")) {
		t.Fatalf("mutable export was cached: %q, %v", digest, err)
	}
}
