package cluster

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRankIsConfigurationOnlyAndNeverChangesBundleVersion(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fixture.py"), []byte("print('dummy fixture')\n"), 0600)
	p := Program{Entrypoint: "fixture.py"}
	before, v1, err := BuildBundle(dir, "fixture", p)
	if err != nil {
		t.Fatal(err)
	}
	five := 5
	p.Rank = &five
	after, v2, err := BuildBundle(dir, "fixture", p)
	if err != nil {
		t.Fatal(err)
	}
	if v1 != v2 || string(before) != string(after) {
		t.Fatal("rank changed bundle contents/version")
	}
	zero := 0
	p.Rank = &zero
	if _, _, err = BuildBundle(dir, "fixture", p); err == nil {
		t.Fatal("explicit zero rank accepted")
	}
	six := 6
	p.Rank = &six
	if _, _, err = BuildBundle(dir, "fixture", p); err == nil {
		t.Fatal("rank over five accepted")
	}
}
