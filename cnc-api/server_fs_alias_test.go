package cnc

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestFileManagerDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	storage := t.TempDir()
	if err := os.WriteFile(filepath.Join(storage, "fixture.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(storage, filepath.Join(root, "merged")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	list := func(dir string) []FileInfo {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleFSListAPI(w, httptest.NewRequest("GET", "/api/fs/list?dir="+url.QueryEscape(dir), nil))
		if w.Code != 200 {
			t.Fatalf("listing failed: %d", w.Code)
		}
		var result struct {
			Files []FileInfo `json:"files"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Files
	}
	files := list(root)
	if len(files) != 1 || files[0].Name != "merged" || !files[0].IsDir {
		t.Fatalf("alias must be a clickable directory: %+v", files)
	}
	files = list(files[0].Path)
	if len(files) != 1 || files[0].Name != "fixture.txt" || files[0].IsDir {
		t.Fatalf("alias contents unavailable: %+v", files)
	}
}
