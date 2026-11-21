package cnc

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Shell tasks share installed executables, but never share writable job files.
func newShellWorkspace(dataDir, sourceDir, executableDir string) (string, error) {
	if dataDir == "" {
		dataDir = os.TempDir()
	}
	base, err := filepath.Abs(filepath.Join(dataDir, "shell-runs"))
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "task-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	if err = os.Mkdir(filepath.Join(dir, "tmp"), 0700); err != nil {
		return "", err
	}
	// Keep ./tools/foo and tools-relative dependencies compatible. This is an
	// executable reference, not a copy of previous inputs or output files.
	for _, root := range []string{sourceDir, executableDir} {
		if root == "" {
			continue
		}
		tools := filepath.Join(root, "tools")
		if info, e := os.Stat(tools); e == nil && info.IsDir() {
			if err = os.Symlink(tools, filepath.Join(dir, "tools")); err != nil {
				return "", err
			}
			break
		}
	}
	ok = true
	return dir, nil
}

func shellWorkspacePath(dir, name string) (string, error) {
	name = strings.TrimPrefix(name, "~/")
	clean := filepath.Clean(name)
	if name == "" || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("task filename must be relative to its temporary workspace")
	}
	if strings.Split(clean, string(filepath.Separator))[0] == "tools" {
		return "", fmt.Errorf("task input/output cannot use the shared tools directory")
	}
	path := filepath.Join(dir, clean)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	return path, nil
}

func shellWorkspaceEnv(env []string, dir string) []string {
	replace := map[string]string{"HOME": dir, "TMPDIR": filepath.Join(dir, "tmp"), "TMP": filepath.Join(dir, "tmp"), "TEMP": filepath.Join(dir, "tmp")}
	result := make([]string, 0, len(env)+len(replace))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if _, ok := replace[key]; !ok {
			result = append(result, item)
		}
	}
	for key, value := range replace {
		result = append(result, key+"="+value)
	}
	return result
}

// retainSpreadInput keeps the most recently received chunk at the configured
// stable path while execution continues to use its own task input.
func retainSpreadInput(root, name, source string) error {
	destination, err := shellWorkspacePath(root, name)
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".spread-input-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0600); err != nil {
		return err
	}
	if _, err := io.Copy(temp, input); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), destination)
}
