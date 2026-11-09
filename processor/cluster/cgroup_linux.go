//go:build linux

package cluster

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var cgroupMu sync.Mutex

func cgroupRoot() string {
	if root := os.Getenv("CNC_PROCESSOR_CGROUP_ROOT"); root != "" {
		return root
	}
	return "/sys/fs/cgroup/cnc-processor"
}
func CgroupAvailable() bool {
	cgroupMu.Lock()
	defer cgroupMu.Unlock()
	root := cgroupRoot()
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "cgroup.subtree_control"), []byte("+cpu +memory"), 0644); err != nil {
		return false
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("+cpu +memory"), 0644); err != nil {
		return false
	}
	probe, err := os.MkdirTemp(root, ".probe-")
	if err != nil {
		return false
	}
	defer os.Remove(probe)
	for _, name := range []string{"cpu.max", "memory.high", "memory.max", "memory.swap.max", "cgroup.procs"} {
		f, err := os.OpenFile(filepath.Join(probe, name), os.O_WRONLY, 0)
		if err != nil {
			return false
		}
		f.Close()
	}
	return true
}

// RunOwnedCommand makes the kernel terminate a scanner when its worker dies,
// even though its resource cgroup is separate from the worker service.
func RunOwnedCommand(cmd *exec.Cmd) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return cmd.Run()
}

// RecoverCommandGroup removes the cgroup left by an interrupted command
// owner. The caller must hold exclusive ownership of the command workspace.
func RecoverCommandGroup(id string) error {
	if !SafeRelative(id) || strings.Contains(id, "/") {
		return fmt.Errorf("invalid cgroup ID")
	}
	path := filepath.Join(cgroupRoot(), id)
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		return nil
	}
	if err := os.WriteFile(filepath.Join(path, "cgroup.kill"), []byte("1"), 0644); err != nil && !os.IsNotExist(err) {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func configureCgroup(cmd *exec.Cmd, id string, r Resources) (func(), error) {
	return configureCgroupRequired(cmd, id, r, false)
}

func configureCgroupRequired(cmd *exec.Cmd, id string, r Resources, required bool) (func(), error) {
	if !CgroupAvailable() {
		if required {
			return nil, fmt.Errorf("shell job resource enforcement is unavailable; job not started")
		}
		return func() {}, nil
	}
	if !SafeRelative(id) || strings.Contains(id, "/") {
		return nil, fmt.Errorf("invalid cgroup ID")
	}
	// Every job and dependency installation is a child of this shared pool.
	// The worker daemon itself stays in its systemd service cgroup.
	total, _, err := hostMemoryMB()
	if err != nil || total <= 0 {
		return nil, fmt.Errorf("cannot determine host memory budget: %v", err)
	}
	budget := JobMemoryEmergencyMB(total)
	if r.MemoryMB > budget {
		return nil, fmt.Errorf("job reservation exceeds shared memory budget")
	}
	cgroupMu.Lock()
	err = os.WriteFile(filepath.Join(cgroupRoot(), "memory.max"), []byte(fmt.Sprint(int64(budget)<<20)), 0644)
	if err == nil {
		// O_NONBLOCK prevents reclaim in the worker's management thread.
		f, openErr := os.OpenFile(filepath.Join(cgroupRoot(), "memory.high"), os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if openErr != nil {
			err = openErr
		} else {
			_, err = f.WriteString("max")
			f.Close()
		}
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(cgroupRoot(), "memory.swap.max"), []byte("0"), 0644)
	}
	cgroupMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("configure shared memory budget: %w", err)
	}
	path := filepath.Join(cgroupRoot(), id)
	if err := os.Mkdir(path, 0755); err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.WriteFile(filepath.Join(path, "cgroup.kill"), []byte("1"), 0644); _ = os.Remove(path) }
	childLimit := r.MemoryMB
	if r.MemoryBurst {
		childLimit = budget
	}
	if err := os.WriteFile(filepath.Join(path, "memory.max"), []byte(fmt.Sprint(int64(childLimit)<<20)), 0644); err != nil {
		cleanup()
		return nil, err
	}
	// RAM limits alone permit an oversized process to consume host swap and
	// stall unrelated jobs and heartbeats. Keep its entire memory budget bounded.
	if err := os.WriteFile(filepath.Join(path, "memory.swap.max"), []byte("0"), 0644); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(path, "cpu.max"), []byte(fmt.Sprintf("%d 100000", r.CPUCores*100000)), 0644); err != nil {
		cleanup()
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		cleanup()
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(f.Fd())
	return func() { cleanup(); f.Close() }, nil
}

func jobMemoryUsageMB() int {
	data, err := os.ReadFile(filepath.Join(cgroupRoot(), "memory.current"))
	if err != nil {
		return 0
	}
	bytes, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return int((bytes + (1 << 20) - 1) >> 20)
}
