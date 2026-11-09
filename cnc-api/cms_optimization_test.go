package cnc

import (
	"strings"
	"testing"
)

func TestCMSAdaptiveCommand(t *testing.T) {
	cmd, err := GenerateToolCommand("cms-scan", "{input}", map[string]interface{}{"threads": 32, "timeout": 5, "auto_concurrency": false})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"-t 32", "-timeout 5s", "-auto-concurrency=false"} {
		if !strings.Contains(cmd, part) {
			t.Fatal(cmd)
		}
	}
	if _, err = GenerateToolCommand("cms-scan", "{input}", map[string]interface{}{"timeout": 0}); err == nil {
		t.Fatal("invalid timeout accepted")
	}
}
