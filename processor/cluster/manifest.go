package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Input and outputs are relative to the entrypoint's own directory.
type ScriptExecution struct {
	Runtime    string         `json:"runtime,omitempty"`
	BinaryArch string         `json:"binary_arch,omitempty"`
	Policies   []OutputPolicy `json:"policies,omitempty"`
	Input      string         `json:"input"`
	Outputs    []string       `json:"outputs"`
}

type PinnedTarget struct {
	ProgramID string `json:"program_id"`
	Version   string `json:"version"`
	Limit     int    `json:"limit"`
	RetrySafe bool   `json:"retry_safe"`
	Export    bool   `json:"export"`
}
type OutputPolicy struct {
	Path       string        `json:"path"`
	MergedName string        `json:"merged_name,omitempty"`
	SendTo     string        `json:"send_to,omitempty"`
	Drive      bool          `json:"drive"`
	Target     *PinnedTarget `json:"target,omitempty"`
}

func (o *OutputPolicy) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		o.Drive = true
		return json.Unmarshal(data, &o.Path)
	}
	type policy OutputPolicy
	v := policy{Drive: true}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return err
	}
	if v.Target != nil {
		return fmt.Errorf("target is an internal field")
	}
	*o = OutputPolicy(v)
	return nil
}

// Internal policies containing pinned targets use ordinary decoding through this alias.
func decodeScript(data []byte, out *ScriptExecution) error {
	type policy OutputPolicy
	var v struct {
		Runtime    string
		BinaryArch string `json:"binary_arch"`
		Input      string
		Outputs    []string
		Policies   []policy
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	out.Policies = nil
	out.Runtime = v.Runtime
	out.BinaryArch = v.BinaryArch
	out.Input = v.Input
	out.Outputs = v.Outputs
	for _, p := range v.Policies {
		out.Policies = append(out.Policies, OutputPolicy(p))
	}
	return nil
}
func (s *ScriptExecution) UnmarshalJSON(data []byte) error { return decodeScript(data, s) }

type ToolManifest struct {
	Input            string                    `json:"input,omitempty"`
	Files            []string                  `json:"files,omitempty"`
	RequirementsLock string                    `json:"requirements_lock,omitempty"`
	Scripts          map[string]ManifestScript `json:"scripts"`
}

// Controller-owned definitions and legacy files follow the same strict schema.
func (m *ToolManifest) UnmarshalJSON(data []byte) error {
	type manifest ToolManifest
	var value manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("definition must contain one JSON object")
	}
	*m = ToolManifest(value)
	return nil
}

type ManifestScript struct {
	Path             string         `json:"path"`
	Runtime          string         `json:"runtime,omitempty"`
	Input            string         `json:"input,omitempty"`
	PackageLock      string         `json:"package_lock,omitempty"`
	Outputs          []OutputPolicy `json:"outputs"`
	Files            []string       `json:"files,omitempty"`
	RequirementsLock string         `json:"requirements_lock,omitempty"`
}

func visibleID(id string) bool {
	return SafeRelative(id) && !strings.Contains(id, "/") && !strings.HasPrefix(id, ".")
}

// Check every component without following links, including directory links.
func NoSymlinks(root, relative string) error {
	if !SafeRelative(relative) {
		return fmt.Errorf("invalid relative path %q", relative)
	}
	path := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not allowed: %s", relative)
		}
	}
	return nil
}

func (p Program) ValidateScript() error {
	if p.Script == nil {
		return nil
	}
	if !SafeRelative(p.Entrypoint) || !SafeRelative(p.Script.Input) {
		return fmt.Errorf("script and input must be relative paths")
	}
	if _, err := ResolveRuntime(p.Script.Runtime, p.Entrypoint); err != nil {
		return err
	}
	if len(p.Script.Outputs) == 0 {
		return fmt.Errorf("script must declare outputs")
	}
	reserved := map[string]bool{}
	for _, name := range append(append([]string{p.Entrypoint, p.RequirementsLock, p.PackageLock, "processor_publish.py", "processor_supervise.py"}, p.Files...), filepath.Join(filepath.Dir(p.Entrypoint), p.Script.Input)) {
		reserved[name] = true
	}
	seen := map[string]bool{}
	for _, policy := range p.Script.Policies {
		if policy.MergedName != "" && (!visibleID(policy.MergedName) || !strings.HasSuffix(policy.MergedName, ".txt")) {
			return fmt.Errorf("merged filename must be a visible .txt filename without folders")
		}
	}
	for _, name := range p.Script.Outputs {
		if !SafeRelative(name) || filepath.Ext(name) != ".txt" || strings.HasPrefix(filepath.Base(name), ".") || reserved[filepath.Join(filepath.Dir(p.Entrypoint), name)] || seen[filepath.Base(name)] {
			return fmt.Errorf("invalid or conflicting output %q", name)
		}
		seen[filepath.Base(name)] = true
	}
	input := filepath.Join(filepath.Dir(p.Entrypoint), p.Script.Input)
	for _, name := range append([]string{p.Entrypoint, p.RequirementsLock, p.PackageLock, "processor_publish.py", "processor_supervise.py"}, p.Files...) {
		if name == input {
			return fmt.Errorf("input conflicts with registered file %q", name)
		}
	}
	return nil
}

// Resolve manifests into ordinary program registrations and independent routes.
// ResolveManifestsRetained expands pinned manifest groups from registered
// settings. A core-only restart must not reopen their manifests or sources.
// An explicit editor Apply removes the selected group's pins before resolving.
func (c Config) ResolveManifestsRetained(root string, retained map[string]Program) (Config, error) {
	programs := map[string]Program{}
	groups := map[string][]string{}
	for id, p := range c.Programs {
		if p.Manifest != "" || p.Definition != nil {
			children := map[string]Program{}
			incoming := map[string]bool{}
			for child, old := range retained {
				if strings.HasPrefix(child, id+"--") {
					children[child] = old
					if old.Script != nil {
						for _, policy := range old.Script.Policies {
							if policy.SendTo != "" {
								incoming[policy.SendTo] = true
							}
						}
					}
				}
			}
			if len(children) > 0 {
				groups[id] = []string{}
				for child, old := range children {
					if _, exists := c.Programs[child]; exists {
						return Config{}, fmt.Errorf("retained program ID conflicts: %s", child)
					}
					programs[child] = old
					if !incoming[child] {
						groups[id] = append(groups[id], child)
					}
				}
				sort.Strings(groups[id])
				continue
			}
		}
		programs[id] = p
	}
	expand := func(ids []string) []string {
		var out []string
		for _, id := range ids {
			if children, ok := groups[id]; ok {
				out = append(out, children...)
			} else {
				out = append(out, id)
			}
		}
		return out
	}
	routes := map[string][]string{}
	for name, ids := range c.Routes {
		routes[name] = expand(ids)
	}
	c.Programs, c.Routes, c.ExportResults = programs, routes, expand(c.ExportResults)
	return c.ResolveManifests(root)
}

func (c Config) ResolveManifests(root string) (Config, error) {
	result := c
	result.Programs = map[string]Program{}
	result.Routes = map[string][]string{}
	groups := map[string][]string{}
	keys := make([]string, 0, len(c.Programs))
	for id := range c.Programs {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		p := c.Programs[id]
		if p.Manifest == "" && p.Definition == nil {
			if p.Folder != "" {
				return Config{}, fmt.Errorf("program %s folder requires a controller definition", id)
			}
			if _, ok := result.Programs[id]; ok {
				return Config{}, fmt.Errorf("duplicate program %s", id)
			}
			result.Programs[id] = p
			continue
		}
		if !visibleID(id) {
			return Config{}, fmt.Errorf("invalid manifest tool ID %q", id)
		}
		var m ToolManifest
		var err error
		base := p.Folder
		if p.Definition != nil {
			if p.Manifest != "" || p.Folder != "" && !SafeRelative(p.Folder) {
				return Config{}, fmt.Errorf("program %s requires one definition and a relative source folder", id)
			}
			m = *p.Definition
		} else {
			if p.Folder != "" {
				return Config{}, fmt.Errorf("program %s cannot combine a manifest path and source folder", id)
			}
			if err := NoSymlinks(root, p.Manifest); err != nil {
				return Config{}, err
			}
			f, err := os.Open(filepath.Join(root, p.Manifest))
			if err != nil {
				return Config{}, err
			}
			data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
			f.Close()
			if err != nil {
				return Config{}, err
			}
			if len(data) > 1<<20 {
				return Config{}, fmt.Errorf("manifest exceeds 1 MiB")
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&m); err != nil {
				return Config{}, fmt.Errorf("manifest %s: %w", p.Manifest, err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return Config{}, fmt.Errorf("manifest must contain one JSON object")
			}
			base = filepath.Dir(p.Manifest)
		}
		if m.Input == "" {
			m.Input = "input.txt"
		}
		if !SafeRelative(m.Input) || len(m.Scripts) == 0 {
			return Config{}, fmt.Errorf("manifest requires scripts and a relative input filename")
		}
		join := func(path string) (string, error) {
			if !SafeRelative(path) {
				return "", fmt.Errorf("invalid manifest file %q", path)
			}
			full := filepath.Join(base, path)
			if err := NoSymlinks(root, full); err != nil {
				return "", err
			}
			return full, nil
		}
		names := make([]string, 0, len(m.Scripts))
		for name := range m.Scripts {
			names = append(names, name)
		}
		sort.Strings(names)
		incoming := map[string]bool{}
		state := map[string]int{}
		var visit func(string) error
		visit = func(key string) error {
			if state[key] == 1 {
				return fmt.Errorf("script connection cycle at %s", key)
			}
			if state[key] == 2 {
				return nil
			}
			state[key] = 1
			for _, o := range m.Scripts[key].Outputs {
				if o.SendTo != "" {
					if _, ok := m.Scripts[o.SendTo]; !ok {
						return fmt.Errorf("unknown target %s", o.SendTo)
					}
					incoming[o.SendTo] = true
					if err := visit(o.SendTo); err != nil {
						return err
					}
				}
			}
			state[key] = 2
			return nil
		}
		for _, key := range names {
			if err := visit(key); err != nil {
				return Config{}, err
			}
		}
		for _, name := range names {
			if !visibleID(name) {
				return Config{}, fmt.Errorf("invalid script ID %q", name)
			}
			child := id + "--" + name
			if _, ok := c.Programs[child]; ok {
				return Config{}, fmt.Errorf("manifest program ID conflicts: %s", child)
			}
			if _, ok := result.Programs[child]; ok {
				return Config{}, fmt.Errorf("duplicate manifest program ID: %s", child)
			}
			s := m.Scripts[name]
			q := p
			q.Manifest = ""
			q.Folder = ""
			q.Definition = nil
			q.Files = nil
			q.Entrypoint, err = join(s.Path)
			if err != nil {
				return Config{}, err
			}
			for _, file := range append(append([]string{}, m.Files...), s.Files...) {
				full, e := join(file)
				if e != nil {
					return Config{}, e
				}
				q.Files = append(q.Files, full)
			}
			lock := s.RequirementsLock
			if lock == "" {
				lock = m.RequirementsLock
			}
			q.RequirementsLock = ""
			q.PackageLock = ""
			if lock != "" {
				q.RequirementsLock, err = join(lock)
				if err != nil {
					return Config{}, err
				}
			}
			input := s.Input
			if input == "" {
				input = m.Input
			}
			runtime, e := ResolveRuntime(s.Runtime, q.Entrypoint)
			if e != nil {
				return Config{}, e
			}
			q.Script = &ScriptExecution{Input: input, Runtime: runtime}
			if runtime == "binary" {
				q.Script.BinaryArch, err = BinaryArchitecture(filepath.Join(root, q.Entrypoint))
				if err != nil {
					return Config{}, err
				}
			}
			if s.PackageLock != "" {
				if runtime != "node" {
					return Config{}, fmt.Errorf("package_lock requires node")
				}
				q.PackageLock, err = join(s.PackageLock)
				if err != nil {
					return Config{}, err
				}
				if filepath.Base(q.PackageLock) != "package-lock.json" {
					return Config{}, fmt.Errorf("package_lock must be package-lock.json")
				}
				packageJSON, err := join(filepath.Join(filepath.Dir(s.PackageLock), "package.json"))
				if err != nil {
					return Config{}, err
				}
				q.Files = append(q.Files, packageJSON)
			}
			if lock != "" && runtime != "python" {
				return Config{}, fmt.Errorf("requirements_lock requires python")
			}
			for _, o := range s.Outputs {
				q.Script.Outputs = append(q.Script.Outputs, o.Path)
				if o.SendTo != "" {
					o.SendTo = id + "--" + o.SendTo
				}
				q.Script.Policies = append(q.Script.Policies, o)
			}
			q.AutoPublish = AutoPublish{Enabled: true, Directory: "work"}
			if err := q.ValidateScript(); err != nil {
				return Config{}, fmt.Errorf("script %s: %w", child, err)
			}
			result.Programs[child] = q
			if !incoming[name] {
				groups[id] = append(groups[id], child)
			}
		}
	}
	expand := func(ids []string) []string {
		out := []string{}
		seen := map[string]bool{}
		for _, id := range ids {
			children := groups[id]
			if children == nil {
				children = []string{id}
			}
			for _, child := range children {
				if !seen[child] {
					out = append(out, child)
					seen[child] = true
				}
			}
		}
		return out
	}
	for name, ids := range c.Routes {
		result.Routes[name] = expand(ids)
	}
	result.ExportResults = expand(c.ExportResults)
	return result, nil
}
