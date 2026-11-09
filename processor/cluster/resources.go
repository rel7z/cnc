package cluster

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var sampleMu sync.Mutex
var previousTotal, previousIdle uint64

// SampleResources uses host-wide counters, so admission includes non-processor work.
func SampleResources() ResourceSnapshot {
	sampleMu.Lock()
	defer sampleMu.Unlock()
	r := ResourceSnapshot{Network: sampleHostNetwork(), Capabilities: DetectRuntimeCapabilities(), CPUCores: runtime.NumCPU(), SampledAt: time.Now().UnixMilli(), Cgroups: CgroupAvailable()}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		r.SampledAt = 0
		return r
	}
	fields := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	var total, idle uint64
	for i, v := range fields[1:] {
		if i >= 8 {
			break
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	if total > previousTotal && previousTotal > 0 {
		r.CPUPercent = 100 * (1 - float64(idle-previousIdle)/float64(total-previousTotal))
	} else {
		r.CPUPercent = 100
	}
	previousTotal, previousIdle = total, idle
	r.TotalMemoryMB, r.AvailableMemoryMB, err = hostMemoryMB()
	r.JobMemoryMB = jobMemoryUsageMB()
	if err != nil {
		r.SampledAt = 0
	}
	return r
}

func hostMemoryMB() (total, available int, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		v := strings.Fields(scan.Text())
		if len(v) < 2 {
			continue
		}
		n, _ := strconv.Atoi(v[1])
		switch v[0] {
		case "MemTotal:":
			total = n / 1024
		case "MemAvailable:":
			available = n / 1024
		}
	}
	return total, available, scan.Err()
}

// Admission returns a reason rather than moving a waiting job out of the queue.
func Admission(w Worker, resources Resources, limits WorkerLimits, reserved Resources, processorLoad int) string {
	if w.MaxTasks <= 0 || w.CurrentLoad >= w.MaxTasks {
		return "waiting for a CNC task slot"
	}
	if w.Resources.SampledAt == 0 || time.Since(time.UnixMilli(w.Resources.SampledAt)) > 15*time.Second {
		return "waiting for fresh resource telemetry"
	}
	if w.Resources.JobMemoryMB > JobMemoryBudgetMB(w.Resources.TotalMemoryMB) && (w.CurrentLoad > 0 || reserved.MemoryMB > 0) {
		return "waiting for running job memory burst to finish"
	}
	max := limits.MaxParallel
	if max <= 0 {
		max = 2
	}
	if max > w.Resources.CPUCores {
		max = w.Resources.CPUCores
	}
	if max > w.MaxTasks {
		max = w.MaxTasks
	}
	if !w.Resources.Cgroups {
		max = 1
	}
	if processorLoad >= max {
		return "waiting for a processor slot"
	}
	threshold := limits.CPUPercent
	if threshold <= 0 {
		threshold = 80
	}
	if w.Resources.CPUPercent >= threshold {
		return "waiting for CPU usage below admission limit"
	}
	if reserved.CPUCores+resources.CPUCores > w.Resources.CPUCores {
		return "waiting for reserved CPU capacity"
	}
	reserve := WorkerMemoryReserveMB(w.Resources.TotalMemoryMB)
	if reserve < 512 {
		reserve = 512
	}
	if limits.ReserveMemoryMB > reserve {
		reserve = limits.ReserveMemoryMB
	}
	committed := reserved.MemoryMB
	if usage := activeMemoryMB(w, reserved); usage > committed {
		committed = usage
	}
	if committed+resources.MemoryMB > JobMemoryBudgetMB(w.Resources.TotalMemoryMB) {
		return "waiting for shared job memory budget"
	}
	if w.Resources.AvailableMemoryMB-UnusedMemoryReservationMB(w, reserved)-resources.MemoryMB < reserve {
		return "waiting for available memory"
	}
	return ""
}
