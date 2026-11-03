package cnc

import (
	"fmt"
	"strings"
)

// Return a bounded explanation, never raw stderr: a command may echo secrets.
func taskFailureSummary(result *TaskResult) string {
	if result == nil {
		return "worker returned no result"
	}
	s := strings.ToLower(result.Stderr)
	reason := "command failed; see stderr for details"
	switch {
	case strings.Contains(s, "[diagnostic] execution timed out"):
		reason = "execution timed out; increase the task timeout or inspect the stalled command"
	case strings.Contains(s, "[diagnostic] execution cancelled"):
		reason = "execution was cancelled"
	case strings.Contains(s, "concurrent actor runs"):
		reason = "Apify concurrent Actor limit reached; wait for an available slot"
	case strings.Contains(s, "modulenotfounderror"), strings.Contains(s, "cannot find module"), strings.Contains(s, "err_module_not_found"):
		reason = "required Python or Node dependency is missing on this worker"
	case strings.Contains(s, "error while loading shared libraries"), strings.Contains(s, "library not loaded:"):
		reason = "required shared library is missing on this worker"
	case result.ExitCode == 127 && (strings.Contains(s, "bad interpreter") || strings.Contains(s, "env:") && (strings.Contains(s, "no such file") || strings.Contains(s, "not found"))):
		reason = "required interpreter is missing or its executable path is invalid"
	case result.ExitCode == 127:
		reason = "command or required interpreter was not found; check the worker tool installation and PATH"
	case result.ExitCode == 126 && strings.Contains(s, "permission denied"):
		reason = "command exists but execution permission was denied"
	case result.ExitCode == 126:
		reason = "command could not be executed; check permissions, interpreter and binary architecture"
	case strings.Contains(s, "invalid token"), strings.Contains(s, "invalid_token"), strings.Contains(s, "unauthorized"), strings.Contains(s, "status code: 401"):
		reason = "provider authentication failed; check the configured API credential"
	case strings.Contains(s, "insufficient credits"), strings.Contains(s, "payment required"), strings.Contains(s, "monthly usage hard limit"):
		reason = "provider credit or spending limit reached"
	case strings.Contains(s, "too many requests"), strings.Contains(s, "status code: 429"):
		reason = "provider rate limit reached; retry after the provider's delay"
	case result.ExitCode == -1:
		reason = "process was terminated by a signal; inspect timeout, cancellation and worker memory events"
	}
	return fmt.Sprintf("%s (exit code %d)", reason, result.ExitCode)
}
