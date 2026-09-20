package cnc

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrRestartRequested is returned by Server.Start() when a restart was requested.
var ErrRestartRequested = errors.New("server restart requested")

func restartProcess() error {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	// Wait briefly for OS to release network sockets
	time.Sleep(300 * time.Millisecond)

	log.Printf("[restart] Re-executing %s with args: %v", executable, os.Args)
	err = syscallExec(executable, os.Args, os.Environ())
	if err == nil {
		return nil
	}

	log.Printf("[restart] syscall.Exec error: %v, falling back to os/exec spawn...", err)

	cmd := exec.Command(executable, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if startErr := cmd.Start(); startErr != nil {
		return fmt.Errorf("failed to spawn process: %w (exec error was: %v)", startErr, err)
	}

	os.Exit(0)
	return nil
}
