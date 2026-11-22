package cnc

import (
	"fmt"
	"os"
)

type bundleFileHash struct {
	info   os.FileInfo
	digest string
}

func sameBundleFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Bundles are published once under unique names by an atomic rename. Reuse
// their hash while identity, size and modification time remain unchanged.
// Mutable exports retain their normal full-content checks on every scan.
func (u *GDriveUploader) hashBundleFile(path string) (string, error) {
	u.bundleHashMu.Lock()
	defer u.bundleHashMu.Unlock()
	before, err := os.Lstat(path)
	if err != nil {
		delete(u.bundleHashes, path)
		return "", err
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("backup bundle must be a regular file")
	}
	if saved, ok := u.bundleHashes[path]; ok && sameBundleFile(saved.info, before) {
		return saved.digest, nil
	}
	digest, err := hashDriveFile(path)
	if err != nil {
		return "", err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !sameBundleFile(before, after) {
		return "", fmt.Errorf("backup bundle changed while hashing")
	}
	if u.bundleHashes == nil {
		u.bundleHashes = make(map[string]bundleFileHash)
	}
	u.bundleHashes[path] = bundleFileHash{info: after, digest: digest}
	return digest, nil
}
