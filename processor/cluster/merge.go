package cluster

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MergeText is called under CNC's writer/cleanup lock in production. It is
// idempotent after a crash between an append and its database acknowledgement.
func MergeText(root, name string, lines []string) error {
	if !SafeRelative(name) || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return errors.New("invalid output filename")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	path := filepath.Join(root, name)
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW, 0644)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err = syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("output is not regular")
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err = f.ReadAt(last[:], info.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			return errors.New("output has an unfinished line")
		}
	}
	wanted := map[string]bool{}
	for _, line := range lines {
		if !strings.HasSuffix(line, "\n") || strings.Contains(strings.TrimSuffix(line, "\n"), "\n") {
			return errors.New("invalid output line")
		}
		wanted[line] = true
	}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64<<10), (1<<20)+1)
	for scan.Scan() {
		delete(wanted, scan.Text()+"\n")
	}
	if err = scan.Err(); err != nil {
		return err
	}
	if _, err = f.Seek(0, 2); err != nil {
		return err
	}
	for _, line := range lines {
		if wanted[line] {
			if _, err = f.WriteString(line); err != nil {
				return err
			}
			delete(wanted, line)
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
