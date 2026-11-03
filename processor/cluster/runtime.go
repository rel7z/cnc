package cluster

import (
	"debug/elf"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

type RuntimeCapabilities struct {
	OS       string          `json:"os"`
	Arch     string          `json:"arch"`
	Runtimes map[string]bool `json:"runtimes"`
	Features []string        `json:"features"`
}

func DetectRuntimeCapabilities() *RuntimeCapabilities {
	c := &RuntimeCapabilities{OS: runtime.GOOS, Arch: runtime.GOARCH, Runtimes: map[string]bool{"binary": runtime.GOOS == "linux"}, Features: []string{"runtime-manifest-v1"}}
	for key, command := range map[string]string{"python": "python3", "node": "node", "bash": "bash", "npm": "npm"} {
		_, err := exec.LookPath(command)
		c.Runtimes[key] = err == nil
	}
	return c
}
func ResolveRuntime(value, path string) (string, error) {
	if value == "" {
		switch filepath.Ext(path) {
		case ".py":
			value = "python"
		case ".js", ".mjs", ".cjs":
			value = "node"
		case ".sh":
			value = "bash"
		default:
			return "", fmt.Errorf("explicit runtime required for %s", path)
		}
	}
	switch value {
	case "python", "binary", "node", "bash":
		return value, nil
	}
	return "", fmt.Errorf("unsupported runtime %s", value)
}
func BinaryArchitecture(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", fmt.Errorf("binary must be a Linux ELF executable: %w", err)
	}
	defer f.Close()
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return "", fmt.Errorf("binary must be executable")
	}
	switch f.Machine {
	case elf.EM_X86_64:
		return "amd64", nil
	case elf.EM_AARCH64:
		return "arm64", nil
	case elf.EM_386:
		return "386", nil
	case elf.EM_ARM:
		return "arm", nil
	}
	return "", fmt.Errorf("unsupported binary architecture")
}
func RuntimeAdmission(p Program, c *RuntimeCapabilities) string {
	if p.Script == nil {
		return ""
	}
	if c == nil {
		return "waiting for runtime-capable worker"
	}
	found := false
	for _, f := range c.Features {
		if f == "runtime-manifest-v1" {
			found = true
		}
	}
	if !found {
		return "waiting for runtime-manifest-v1 worker"
	}
	r, err := ResolveRuntime(p.Script.Runtime, p.Entrypoint)
	if err != nil {
		return err.Error()
	}
	if !c.Runtimes["python"] || !c.Runtimes[r] {
		return "waiting for worker runtime: " + r
	}
	if p.PackageLock != "" && !c.Runtimes["npm"] {
		return "waiting for worker runtime: npm"
	}
	if r == "binary" && (c.OS != "linux" || c.Arch != p.Script.BinaryArch) {
		return "waiting for compatible Linux binary architecture"
	}
	return ""
}
