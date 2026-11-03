package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// WorkspaceName groups manifest steps beneath their tool's stable directory.
func WorkspaceName(program string) (string, error) {
	name, _, _ := strings.Cut(program, "--")
	if !visibleID(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid workspace name")
	}
	return name, nil
}

func (r *Runner) lockWorkspace(ctx context.Context, program string) (string, func(), error) {
	name, err := WorkspaceName(program)
	if err != nil {
		return "", nil, err
	}
	work := filepath.Join(r.WorkspacesRoot, name)
	if err = NoSymlinks(r.WorkspacesRoot, filepath.Join(name, ".cnc", "lock")); err != nil {
		return "", nil, err
	}
	if err = os.MkdirAll(filepath.Join(work, ".cnc"), 0700); err != nil {
		return "", nil, err
	}
	f, err := os.OpenFile(filepath.Join(work, ".cnc", "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return work, func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return "", nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return "", nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Relocate only the entrypoint directory, preserving other declared paths and
// rejecting ambiguous bundles rather than overwriting two files with one name.
func workspaceBundle(original *Bundle) (*Bundle, error) {
	b := *original
	if b.Script != nil {
		for _, path := range append([]string{b.Script.Input}, b.Script.Outputs...) {
			if strings.Split(path, string(filepath.Separator))[0] == ".cnc" {
				return nil, fmt.Errorf(".cnc is reserved for workspace state")
			}
		}
	}
	base := filepath.Dir(b.Entrypoint)
	relocate := func(name string) string {
		if base != "." {
			return strings.TrimPrefix(name, base+string(filepath.Separator))
		}
		return name
	}
	b.Files = map[string][]byte{}
	for name, data := range original.Files {
		target := relocate(name)
		if !SafeRelative(target) || strings.Split(target, string(filepath.Separator))[0] == ".cnc" {
			return nil, fmt.Errorf("reserved workspace file")
		}
		if _, exists := b.Files[target]; exists {
			return nil, fmt.Errorf("conflicting workspace file %s", target)
		}
		b.Files[target] = data
	}
	b.Entrypoint = relocate(b.Entrypoint)
	b.PackageLock = relocate(b.PackageLock)
	b.RequirementsLock = relocate(b.RequirementsLock)
	return &b, nil
}

// Retain previous results before clearing selected output paths for a new run.
// That prevents old lines being attributed to a later batch. No source or input
// is removed; arbitrary files outside declared outputs remain in place.
func archiveWorkspaceOutputs(work string, b *Bundle, attempt string) error {
	names := []string{}
	if b.Script != nil {
		names = append(names, b.Script.Outputs...)
	} else {
		entries, err := os.ReadDir(work)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".txt") && b.Files[e.Name()] == nil && e.Name() != "input.txt" {
				names = append(names, e.Name())
			}
		}
	}
	for _, name := range names {
		if err := NoSymlinks(work, name); err != nil {
			return err
		}
		source := filepath.Join(work, name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace output must be a regular file")
		}
		target := filepath.Join(work, ".cnc", "history", attempt, name)
		if err = NoSymlinks(work, filepath.Join(".cnc", "history", attempt, name)); err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if _, err = os.Lstat(target); err == nil {
			return fmt.Errorf("workspace history already exists")
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = os.Rename(source, target); err != nil {
			return err
		}
	}
	output := filepath.Join(work, "output")
	if err := NoSymlinks(work, "output"); err != nil {
		return err
	}
	entries, err := os.ReadDir(output)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		name := filepath.Join("output", entry.Name())
		if err := NoSymlinks(work, name); err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("output must be a regular file")
		}
		target := filepath.Join(work, ".cnc", "history", attempt, name)
		if err := NoSymlinks(work, filepath.Join(".cnc", "history", attempt, name)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			return fmt.Errorf("workspace output history already exists")
		}
		if err := os.Rename(filepath.Join(work, name), target); err != nil {
			return err
		}
	}
	return nil
}

// PrepareWorkspace installs registered bytes only; it never launches the program.
func (r *Runner) PrepareWorkspace(ctx context.Context, version string) (string, error) {
	if r.WorkspacesRoot == "" {
		return "", fmt.Errorf("workspace root is not configured")
	}
	b, err := LoadBundle(r.Root, version)
	if err != nil {
		return "", err
	}
	work, unlock, err := r.lockWorkspace(ctx, b.ProgramID)
	if err != nil {
		return "", err
	}
	defer unlock()
	b, err = workspaceBundle(b)
	if err != nil {
		return "", err
	}
	for name, data := range b.Files {
		if err = NoSymlinks(work, name); err != nil {
			return "", err
		}
		dst := filepath.Join(work, name)
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return "", err
		}
		if err = durableFile(dst, data); err != nil {
			return "", err
		}
		if name == b.Entrypoint && b.Script != nil && b.Script.Runtime == "binary" {
			if err = os.Chmod(dst, 0700); err != nil {
				return "", err
			}
		}
	}
	return work, nil
}

func recordWorkspace(work, dir string, a Assignment) error {
	// No token, command contents, or credentials in user-facing metadata.
	data, _ := json.MarshalIndent(map[string]string{"run_id": a.RunID, "attempt_id": a.AttemptID, "program_id": a.ProgramID, "version": a.Version, "working_directory": work, "state_directory": dir, "log": filepath.Join(dir, "log")}, "", "  ")
	return durableFile(filepath.Join(work, ".cnc", "current.json"), append(data, '\n'))
}
