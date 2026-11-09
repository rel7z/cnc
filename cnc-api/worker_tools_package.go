package cnc

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

//go:embed worker_tools_package.py
var workerToolsPackageBuild string
var workerToolsPackageMu sync.Mutex

// Build from installed controller tools before charging for a new worker.
// Never trust an old enum-only archive to represent the complete installation.
func refreshWorkerToolsPackage(ctx context.Context) error {
	workerToolsPackageMu.Lock()
	defer workerToolsPackageMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "python3", "-c", workerToolsPackageBuild).Run(); err != nil {
		return fmt.Errorf("controller worker tools package is incomplete or cannot be built; no new workers were created")
	}
	return nil
}
