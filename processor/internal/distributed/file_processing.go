package distributed

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/user/cnc-processor/cluster"
)

func validateFileProcessing(policies map[string]cluster.FileProcessingPolicy) error {
	for name, p := range policies {
		if !cluster.SafeRelative(name) || filepath.Base(name) != name {
			return fmt.Errorf("invalid processing filename")
		}
		if p.Distribution != "" && p.Distribution != "spread" {
			return fmt.Errorf("unsupported distribution for %s", name)
		}
	}
	return nil
}

// CaptureSpreadWorkers returns identities, never a program or input payload.
// It deliberately includes busy workers: their share remains queued for them.
func (c *Controller) CaptureSpreadWorkers(name string) ([]string, bool) {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	if c.config.FileProcessing[name].Distribution != "spread" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for _, w := range c.workers {
		if (w.Status == "online" || w.Status == "busy") && w.Protocol == cluster.ProtocolVersion && w.MaxTasks > 0 && freshCPU(w) {
			ids = append(ids, w.ID)
		}
	}
	sort.Strings(ids)
	return ids, true
}

var processedSummary = regexp.MustCompile(`(?im)^processed[\t ]*=[\t ]*([0-9]+)[\t ]*/[\t ]*([0-9]+)[\t ]*\r?$`)

func validateCompletionSummary(log []byte, records int) error {
	matches := processedSummary.FindAllSubmatch(log, -1)
	if len(matches) == 0 {
		return fmt.Errorf("completion summary missing: expected processed = N/%d in saved log", records)
	}
	m := matches[len(matches)-1]
	n, nErr := strconv.ParseUint(string(m[1]), 10, 64)
	total, tErr := strconv.ParseUint(string(m[2]), 10, 64)
	if nErr != nil || tErr != nil || total != uint64(records) || n > total {
		return fmt.Errorf("completion summary invalid: counts do not match %d input records", records)
	}
	return nil
}
