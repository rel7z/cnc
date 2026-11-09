package cluster

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHashPinnedDependencyAndPermanentEnvironment(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	var wheel bytes.Buffer
	archive := zip.NewWriter(&wheel)
	for name, content := range map[string]string{
		"fixture_dep.py":                       "VALUE = 'dependency-ready'\n",
		"fixture_dep-0.0.1.dist-info/METADATA": "Metadata-Version: 2.1\nName: fixture-dep\nVersion: 0.0.1\n",
		"fixture_dep-0.0.1.dist-info/WHEEL":    "Wheel-Version: 1.0\nGenerator: processor-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		"fixture_dep-0.0.1.dist-info/RECORD":   "",
	} {
		f, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	archive.Close()
	source := t.TempDir()
	root := t.TempDir()
	wheelName := "fixture_dep-0.0.1-py3-none-any.whl"
	os.WriteFile(filepath.Join(source, wheelName), wheel.Bytes(), 0600)
	os.WriteFile(filepath.Join(source, "fixture.py"), []byte("import fixture_dep\nprint(fixture_dep.VALUE)\n"), 0600)
	install := func(hash string) (string, string, error) {
		os.WriteFile(filepath.Join(source, "requirements.lock"), []byte("--no-index\n./"+wheelName+" --hash=sha256:"+hash+"\n"), 0600)
		data, version, err := BuildBundle(source, "fixture", Program{Entrypoint: "fixture.py", Files: []string{wheelName}, RequirementsLock: "requirements.lock"})
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
		defer srv.Close()
		dir, err := InstallBundle(context.Background(), root, Install{Version: version, BundleURL: srv.URL})
		return dir, version, err
	}
	dir, _, err := install(Digest(wheel.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(filepath.Join(dir, "venv", "bin", "python"), filepath.Join(dir, "fixture.py")).CombinedOutput()
	if err != nil || string(output) != "dependency-ready\n" {
		t.Fatalf("installed dependency did not execute: %q %v", output, err)
	}
	_, badVersion, err := install(Digest([]byte("wrong wheel")))
	if err == nil {
		t.Fatal("incorrect dependency checksum accepted")
	}
	if _, err = os.Stat(filepath.Join(root, "bundles", badVersion, "ready")); !os.IsNotExist(err) {
		t.Fatal("failed dependency advertised as ready")
	}
}
