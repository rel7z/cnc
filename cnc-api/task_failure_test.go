package cnc

import (
	"strings"
	"testing"
)

func TestTaskFailureSummaryExplainsFailuresWithoutEchoingSecrets(t *testing.T) {
	for _, tt := range []struct {
		name         string
		code         int
		stderr, want string
	}{
		{"missing command", 127, "sh: 1: no-such-command: not found", "command or required interpreter was not found"},
		{"missing interpreter", 127, "/usr/bin/env: node: No such file or directory", "required interpreter is missing"},
		{"shared library", 127, "error while loading shared libraries: libfixture.so", "shared library is missing"},
		{"permission", 126, "sh: tool: Permission denied", "execution permission was denied"},
		{"architecture", 126, "cannot execute binary file", "binary architecture"},
		{"python module", 1, "ModuleNotFoundError: No module named fixture", "dependency is missing"},
		{"node module", 1, "Error: Cannot find module fixture", "dependency is missing"},
		{"provider capacity", 1, "exceed your limit of 5 concurrent Actor runs", "concurrent Actor limit"},
		{"provider auth", 1, "Invalid token", "authentication failed"},
		{"timeout", -1, "[diagnostic] Execution timed out", "execution timed out"},
		{"cancel", -1, "[diagnostic] Execution cancelled", "execution was cancelled"},
		{"signal", -1, "", "terminated by a signal"},
		{"unknown", 9, "unclassified provider error", "see stderr"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := taskFailureSummary(&TaskResult{ExitCode: tt.code, Stderr: tt.stderr + " token=DO_NOT_ECHO password=DO_NOT_ECHO"})
			if !strings.Contains(got, tt.want) || strings.Contains(got, "DO_NOT_ECHO") {
				t.Fatalf("unsafe or incorrect diagnostic: %s", got)
			}
		})
	}
}

func TestTaskFailureIsPublishedInTaskError(t *testing.T) {
	s := processorTestServer(t)
	task := &Task{ID: "missing-tool", JobID: "job", Status: TaskStatusRunning, AssignedTo: "fixture-worker"}
	s.tasks[task.ID] = task
	message, err := NewMessage(MsgTypeTaskResult, TaskResultPayload{TaskID: task.ID, WorkerID: task.AssignedTo, Result: &TaskResult{ExitCode: 127, Stderr: "sh: missing: not found token=DO_NOT_ECHO"}})
	if err != nil {
		t.Fatal(err)
	}
	s.handleTaskResult(message)
	if task.Status != TaskStatusFailed || !strings.Contains(task.Error, "was not found") || strings.Contains(task.Error, "DO_NOT_ECHO") {
		t.Fatalf("task failure was not published safely: %s", task.Error)
	}
}
