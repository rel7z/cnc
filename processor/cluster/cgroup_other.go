//go:build !linux

package cluster

import "os/exec"

func jobMemoryUsageMB() int { return 0 }

func CgroupAvailable() bool                                                 { return false }
func configureCgroup(cmd *exec.Cmd, id string, r Resources) (func(), error) { return func() {}, nil }
func configureCgroupRequired(cmd *exec.Cmd, id string, r Resources, required bool) (func(), error) {
	return configureCgroup(cmd, id, r)
}
