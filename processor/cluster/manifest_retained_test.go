package cluster

import "testing"

func TestRetainedBinaryManifestNeedsNoSourceAccess(t *testing.T) {
	root := t.TempDir()
	old := Program{Entrypoint: "absent/private/executable", Script: &ScriptExecution{
		Runtime: "binary", BinaryArch: "amd64", Input: "input.txt", Outputs: []string{"result.txt"},
	}}
	config := Config{Programs: map[string]Program{"owned": {Manifest: "absent/private/manifest.json"}},
		Routes: map[string][]string{"input.txt": {"owned"}}, ExportResults: []string{"owned"}}
	got, err := config.ResolveManifestsRetained(root, map[string]Program{"owned--run": old})
	if err != nil {
		t.Fatal(err)
	}
	if got.Programs["owned--run"].Script.BinaryArch != "amd64" ||
		len(got.Routes["input.txt"]) != 1 || got.Routes["input.txt"][0] != "owned--run" ||
		len(got.ExportResults) != 1 || got.ExportResults[0] != "owned--run" {
		t.Fatal("retained metadata or route expansion changed")
	}
}
