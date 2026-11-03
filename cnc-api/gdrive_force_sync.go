package cnc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Explicit manual sync publishes restored local files, while automatic scans
// continue preserving the user's restore-only copies until they are changed.
func (u *GDriveUploader) RequestFullSync() error {
	u.syncMu.Lock()
	defer u.syncMu.Unlock()
	u.uploadedMu.Lock()
	oldUploaded := make(map[string]bool, len(u.uploaded))
	for k, v := range u.uploaded {
		oldUploaded[k] = v
	}
	oldKnown := make(map[string]bool, len(u.knownFiles))
	for k, v := range u.knownFiles {
		oldKnown[k] = v
	}
	oldRestored := make(map[string]string, len(u.restored))
	for k, v := range u.restored {
		oldRestored[k] = v
	}
	for name := range u.restored {
		if name == "" || name == "." || filepath.Base(name) != name || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := os.Lstat(filepath.Join(u.config.WatchDir, name))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		delete(u.restored, name)
		delete(u.knownFiles, name)
		for key := range u.uploaded {
			if strings.HasPrefix(key, name+":") {
				delete(u.uploaded, key)
			}
		}
	}
	u.uploadedMu.Unlock()
	if err := u.saveState(); err != nil {
		u.uploadedMu.Lock()
		u.uploaded = oldUploaded
		u.knownFiles = oldKnown
		u.restored = oldRestored
		u.uploadedMu.Unlock()
		return fmt.Errorf("cannot save explicit Drive synchronization state")
	}
	u.NotifyResults()
	return nil
}
