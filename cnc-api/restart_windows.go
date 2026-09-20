//go:build windows

package cnc

import "errors"

func syscallExec(argv0 string, argv []string, envv []string) error {
	return errors.New("syscall.Exec is not supported on windows")
}
