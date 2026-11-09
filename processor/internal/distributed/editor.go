package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/user/cnc-processor/cluster"
)

type EditorState struct {
	Revision    string                     `json:"revision"`
	Definitions []cluster.EditorDefinition `json:"definitions"`
	External    []string                   `json:"external"`
}

func (c *Controller) ConfigurationStatus() (int, int) {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	return len(c.config.Programs), len(c.config.Routes)
}

func (c *Controller) editorStateLocked() (EditorState, error) {
	data, err := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if err != nil {
		return EditorState{}, err
	}
	var config cluster.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return EditorState{}, err
	}
	state := EditorState{Revision: cluster.Digest(data), Definitions: []cluster.EditorDefinition{}, External: []string{}}
	for id, p := range config.Programs {
		if p.Definition != nil {
			var incoming []string
			for name, ids := range config.Routes {
				for _, target := range ids {
					if target == id {
						incoming = append(incoming, name)
						break
					}
				}
			}
			// A one-input form must not silently replace a multi-route setup.
			if len(incoming) != 1 {
				state.External = append(state.External, id)
				continue
			}
			state.Definitions = append(state.Definitions, cluster.EditorDefinition{ToolID: id, Incoming: incoming[0], Folder: p.Folder, Manifest: *p.Definition})
			continue
		}
		if !strings.HasPrefix(p.Manifest, "ui-programs/active/") {
			state.External = append(state.External, id)
			continue
		}
		if !cluster.SafeRelative(p.Manifest) {
			return state, fmt.Errorf("invalid managed manifest")
		}
		path := filepath.Join(filepath.Dir(p.Manifest), "definition.json")
		if err := cluster.NoSymlinks(c.AppDir, path); err != nil {
			return state, err
		}
		raw, err := os.ReadFile(filepath.Join(c.AppDir, path))
		if err != nil {
			return state, err
		}
		var d cluster.EditorDefinition
		if err := json.Unmarshal(raw, &d); err != nil {
			return state, err
		}
		state.Definitions = append(state.Definitions, d)
	}
	sort.Strings(state.External)
	sort.Slice(state.Definitions, func(i, j int) bool { return state.Definitions[i].ToolID < state.Definitions[j].ToolID })
	return state, nil
}
func (c *Controller) EditorList(ctx context.Context) (any, error) {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	return c.editorStateLocked()
}
func (c *Controller) EditorRoutes() map[string][]string {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	return c.config.Routes
}
func (c *Controller) EditorApply(ctx context.Context, d cluster.EditorDefinition, revision string) (any, error) {
	if err := cluster.ValidateEditorDefinition(d); err != nil {
		return nil, err
	}
	c.configMu.Lock()
	defer c.configMu.Unlock()
	original, err := os.ReadFile(filepath.Join(c.AppDir, "config.json"))
	if err != nil {
		return nil, err
	}
	if revision == "" || cluster.Digest(original) != revision {
		return nil, fmt.Errorf("configuration changed; refresh saved programs before applying")
	}
	var config cluster.Config
	if err := json.Unmarshal(original, &config); err != nil {
		return nil, err
	}
	if old, exists := config.Programs[d.ToolID]; exists && old.Definition == nil && !strings.HasPrefix(old.Manifest, "ui-programs/active/") {
		return nil, fmt.Errorf("tool ID is already registered outside this form; choose a new tool ID")
	}
	relative := filepath.Join("ui-programs", "active", d.ToolID, uuid.NewString())
	root := filepath.Join(c.AppDir, relative)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(root)
		}
	}()
	size := int64(0)
	for _, name := range cluster.EditorFiles(d) {
		if name == "manifest.cnc.json" || name == "definition.json" {
			return nil, fmt.Errorf("%s is reserved by the editor", name)
		}
		source := filepath.Join(d.Folder, name)
		if err := cluster.NoSymlinks(c.AppDir, source); err != nil {
			return nil, err
		}
		info, err := os.Stat(filepath.Join(c.AppDir, source))
		if err != nil {
			return nil, fmt.Errorf("file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() > cluster.MaxArtifactBytes-size {
			return nil, fmt.Errorf("registered files must be regular files totaling at most 64 MiB")
		}
		bytes, err := os.ReadFile(filepath.Join(c.AppDir, source))
		if err != nil {
			return nil, err
		}
		size += int64(len(bytes))
		if size > cluster.MaxArtifactBytes {
			return nil, fmt.Errorf("registered files exceed 64 MiB")
		}
		if err := writeAtomic(filepath.Join(root, name), bytes); err != nil {
			return nil, err
		}
	}
	if config.Programs == nil {
		config.Programs = map[string]cluster.Program{}
	}
	if config.Routes == nil {
		config.Routes = map[string][]string{}
	}
	for filename, ids := range config.Routes {
		next := []string{}
		for _, id := range ids {
			if id != d.ToolID {
				next = append(next, id)
			}
		}
		if len(next) == 0 {
			delete(config.Routes, filename)
		} else {
			config.Routes[filename] = next
		}
	}
	// Settings belong to the controller. The snapshot contains only the files
	// the program itself needs, with no generated per-tool JSON manifest.
	program := config.Programs[d.ToolID]
	program.Manifest, program.Folder, program.Definition = "", relative, &d.Manifest
	config.Programs[d.ToolID] = program
	config.Routes[d.Incoming] = append(config.Routes[d.Incoming], d.ToolID)
	// Validate and bundle the managed snapshot before replacing live configuration.
	probe := cluster.Config{Programs: map[string]cluster.Program{d.ToolID: config.Programs[d.ToolID]}}
	resolved, err := probe.ResolveManifests(c.AppDir)
	if err != nil {
		return nil, err
	}
	for id, p := range resolved.Programs {
		if _, _, err := cluster.BuildBundle(c.AppDir, id, p); err != nil {
			return nil, err
		}
	}
	raw, _ := json.MarshalIndent(config, "", "  ")
	if err := writeAtomic(filepath.Join(c.AppDir, "config.json"), raw); err != nil {
		return nil, err
	}
	// Retaining bundles during a core-only restart must not prevent an owner's
	// explicit Apply from rebuilding this selected tool's managed snapshot.
	pins := map[string]bool{}
	for id, value := range c.PinnedPrograms {
		if strings.HasPrefix(id, d.ToolID+"--") {
			pins[id] = value
			delete(c.PinnedPrograms, id)
		}
	}
	defer func() {
		for id, value := range pins {
			c.PinnedPrograms[id] = value
		}
	}()
	if err := c.prepareLocked(ctx); err != nil {
		restore := writeAtomic(filepath.Join(c.AppDir, "config.json"), original)
		reload := c.prepareLocked(context.Background())
		if restore != nil || reload != nil {
			return nil, fmt.Errorf("configuration activation failed (%v); restore failed (%v, %v)", err, restore, reload)
		}
		return nil, err
	}
	committed = true
	return c.editorStateLocked()
}
