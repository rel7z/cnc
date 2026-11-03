package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestControllerDefinitionReplacesFolderManifestWithoutChangingBundles(t *testing.T) {
	root, legacy := manifestFixture(t, `{"input":"targets.txt","files":["shared.txt"],"scripts":{"scanner":{"path":"scanner.py","outputs":[{"path":"results.txt","send_to":"checker","drive":false}]},"checker":{"path":"nested/checker.py","input":"results.txt","outputs":["success.txt"]}}}`)
	before, err := legacy.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "tool.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definition ToolManifest
	if err = json.Unmarshal(manifestBytes, &definition); err != nil {
		t.Fatal(err)
	}
	central := legacy
	central.Programs = map[string]Program{"tool": {Definition: &definition}}
	// Persist and reload through the same JSON path used by the controller.
	raw, err := json.Marshal(central)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &central); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "tool.json")); err != nil {
		t.Fatal(err)
	}
	after, err := central.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("registration behavior changed\nbefore=%+v\nafter=%+v", before, after)
	}
	for id, program := range before.Programs {
		oldBundle, oldVersion, e := BuildBundle(root, id, program)
		if e != nil {
			t.Fatal(e)
		}
		newBundle, newVersion, e := BuildBundle(root, id, after.Programs[id])
		if e != nil {
			t.Fatal(e)
		}
		if oldVersion != newVersion || string(oldBundle) != string(newBundle) {
			t.Fatal("moving settings changed an immutable executable bundle")
		}
	}
}

func TestControllerDefinitionSourceFolderAndValidation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "program"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "program", "scanner.py"), []byte("pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	definition := &ToolManifest{Scripts: map[string]ManifestScript{"scanner": {Path: "scanner.py", Outputs: []OutputPolicy{{Path: "results.txt", Drive: true}}}}}
	c := Config{Programs: map[string]Program{"demo": {Folder: "program", Definition: definition}}, Routes: map[string][]string{"input.txt": {"demo"}}}
	resolved, err := c.ResolveManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Programs["demo--scanner"].Entrypoint != "program/scanner.py" {
		t.Fatal(resolved)
	}
	for _, program := range []Program{
		{Folder: "../escape", Definition: definition},
		{Folder: "program", Manifest: "absent.json", Definition: definition},
		{Folder: "program"},
	} {
		c.Programs["demo"] = program
		if _, err := c.ResolveManifests(root); err == nil {
			t.Fatal("ambiguous or escaping definition accepted")
		}
	}
}

func TestControllerDefinitionRejectsUnknownSettings(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"programs":{"demo":{"folder":"source","definition":{"unknown":true,"scripts":{}}}}}`), &config); err == nil {
		t.Fatal("unknown controller definition field accepted")
	}
}
