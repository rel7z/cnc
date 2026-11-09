package cluster

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Interface counters are host-wide, not limited to CNC jobs.
type NetworkInterface struct {
	Name             string  `json:"name"`
	RXBytes          uint64  `json:"rx_bytes"`
	TXBytes          uint64  `json:"tx_bytes"`
	RXBytesPerSecond float64 `json:"rx_bytes_per_second"`
	TXBytesPerSecond float64 `json:"tx_bytes_per_second"`
}
type NetworkSnapshot struct {
	SampledAt        int64              `json:"sampled_at"`
	Ready            bool               `json:"ready"`
	RXBytesPerSecond float64            `json:"rx_bytes_per_second"`
	TXBytesPerSecond float64            `json:"tx_bytes_per_second"`
	Interfaces       []NetworkInterface `json:"interfaces"`
}
type networkSampler struct {
	at       time.Time
	previous map[string]NetworkInterface
	snapshot *NetworkSnapshot
}

var hostNetwork networkSampler // guarded by sampleMu alongside CPU sampling

func parseNetwork(r io.Reader) ([]NetworkInterface, error) {
	var result []NetworkInterface
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		name, values, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" {
			continue
		}
		fields := strings.Fields(values)
		if name == "" || len(fields) < 16 {
			return nil, fmt.Errorf("invalid network counters")
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, err
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, err
		}
		result = append(result, NetworkInterface{Name: name, RXBytes: rx, TXBytes: tx})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("no non-loopback network interfaces")
	}
	return result, nil
}
func (s *networkSampler) sample(now time.Time, interfaces []NetworkInterface) *NetworkSnapshot {
	if s.snapshot != nil && now.Sub(s.at) >= 0 && now.Sub(s.at) < time.Second {
		return s.snapshot
	}
	snapshot := &NetworkSnapshot{SampledAt: now.UnixMilli(), Ready: !s.at.IsZero() && now.After(s.at), Interfaces: append([]NetworkInterface(nil), interfaces...)}
	elapsed := now.Sub(s.at).Seconds()
	next := make(map[string]NetworkInterface, len(interfaces))
	for i := range snapshot.Interfaces {
		current := &snapshot.Interfaces[i]
		if old, ok := s.previous[current.Name]; ok && snapshot.Ready {
			// Interface resets/recreation must never underflow into huge rates.
			if current.RXBytes >= old.RXBytes {
				current.RXBytesPerSecond = float64(current.RXBytes-old.RXBytes) / elapsed
			}
			if current.TXBytes >= old.TXBytes {
				current.TXBytesPerSecond = float64(current.TXBytes-old.TXBytes) / elapsed
			}
		}
		snapshot.RXBytesPerSecond += current.RXBytesPerSecond
		snapshot.TXBytesPerSecond += current.TXBytesPerSecond
		next[current.Name] = *current
	}
	s.at, s.previous, s.snapshot = now, next, snapshot
	return snapshot
}
func sampleHostNetwork() *NetworkSnapshot {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		hostNetwork = networkSampler{}
		return nil
	}
	defer f.Close()
	interfaces, err := parseNetwork(f)
	if err != nil {
		hostNetwork = networkSampler{}
		return nil
	}
	return hostNetwork.sample(time.Now(), interfaces)
}
