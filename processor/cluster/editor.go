package cluster

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// The editor stores declarative settings only; uploaded source lives separately.
type EditorDefinition struct {
	ToolID   string       `json:"tool_id"`
	Incoming string       `json:"incoming"`
	Folder   string       `json:"folder"`
	Manifest ToolManifest `json:"manifest"`
}

var editorID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidateEditorDefinition(d EditorDefinition) error {
	if !editorID.MatchString(d.ToolID) {
		return fmt.Errorf("tool ID: use 1–64 letters, numbers, underscores or hyphens")
	}
	if !SafeRelative(d.Incoming) || filepath.Base(d.Incoming) != d.Incoming || !strings.HasSuffix(d.Incoming, ".txt") || strings.HasPrefix(d.Incoming, ".") {
		return fmt.Errorf("incoming filename: enter a .txt filename without folders")
	}
	if d.Folder != "" && !SafeRelative(d.Folder) {
		return fmt.Errorf("source folder: enter a relative folder without ..")
	}
	m := d.Manifest
	if m.Input == "" {
		m.Input = "input.txt"
	}
	if !SafeRelative(m.Input) {
		return fmt.Errorf("input filename: use a relative path")
	}
	if len(m.Scripts) == 0 || len(m.Scripts) > 32 {
		return fmt.Errorf("add between 1 and 32 programs")
	}
	for _, file := range m.Files {
		if !SafeRelative(file) {
			return fmt.Errorf("invalid supporting file %s", file)
		}
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("connections form a cycle at %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		s, ok := m.Scripts[id]
		if !ok {
			return fmt.Errorf("unknown next program %s", id)
		}
		if !editorID.MatchString(id) {
			return fmt.Errorf("invalid program key %s", id)
		}
		runtime, err := ResolveRuntime(s.Runtime, s.Path)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		input := s.Input
		if input == "" {
			input = m.Input
		}
		lock := s.RequirementsLock
		if lock == "" {
			lock = m.RequirementsLock
		}
		if lock != "" && (!SafeRelative(lock) || runtime != "python") {
			return fmt.Errorf("%s: requirements lock needs a Python program and relative filename", id)
		}
		if s.PackageLock != "" && (!SafeRelative(s.PackageLock) || runtime != "node" || filepath.Base(s.PackageLock) != "package-lock.json") {
			return fmt.Errorf("%s: Node package lock must be package-lock.json", id)
		}
		p := Program{Entrypoint: s.Path, Files: append(append([]string{}, m.Files...), s.Files...), RequirementsLock: lock, PackageLock: s.PackageLock, Script: &ScriptExecution{Runtime: runtime, Input: input}}
		if s.PackageLock != "" {
			p.Files = append(p.Files, filepath.Join(filepath.Dir(s.PackageLock), "package.json"))
		}
		for _, f := range p.Files {
			if !SafeRelative(f) {
				return fmt.Errorf("%s: invalid supporting file %s", id, f)
			}
		}
		for _, o := range s.Outputs {
			p.Script.Outputs = append(p.Script.Outputs, o.Path)
			p.Script.Policies = append(p.Script.Policies, o)
			if o.SendTo != "" {
				if err := visit(o.SendTo); err != nil {
					return err
				}
			}
		}
		if err := p.ValidateScript(); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		state[id] = 2
		return nil
	}
	for id := range m.Scripts {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
func EditorFiles(d EditorDefinition) []string {
	set := map[string]bool{}
	for _, p := range d.Manifest.Files {
		set[p] = true
	}
	if d.Manifest.RequirementsLock != "" {
		set[d.Manifest.RequirementsLock] = true
	}
	for _, s := range d.Manifest.Scripts {
		set[s.Path] = true
		for _, p := range s.Files {
			set[p] = true
		}
		if s.RequirementsLock != "" {
			set[s.RequirementsLock] = true
		}
		if s.PackageLock != "" {
			set[s.PackageLock] = true
			set[filepath.Join(filepath.Dir(s.PackageLock), "package.json")] = true
		}
	}
	out := []string{}
	for p := range set {
		out = append(out, p)
	}
	return out
}
