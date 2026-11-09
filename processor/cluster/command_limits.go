package cluster

import (
	"fmt"
	"os/exec"
)

// ConfigureCommandLimits protects generic CNC shell jobs using the same
// per-process-tree limits as processor jobs. Linux must fail closed if
// enforcement is unavailable rather than launch an unlimited process.
func ConfigureCommandLimits(cmd *exec.Cmd, id string, resources Resources) (func(), error) {
	if resources.CPUCores <= 0 || resources.MemoryMB <= 0 {
		return nil, fmt.Errorf("CPU and memory limits must be positive")
	}
	return configureCgroupRequired(cmd, id, resources, true)
}
