package cluster

import (
	"strings"
	"testing"
	"time"
)

func TestNetworkRatesAndInterfaceChanges(t *testing.T) {
	var s networkSampler
	at := time.Unix(100, 0)
	first := s.sample(at, []NetworkInterface{{Name: "eth0", RXBytes: 100, TXBytes: 200}})
	if first.Ready {
		t.Fatal("initial sample must warm up")
	}
	second := s.sample(at.Add(2*time.Second), []NetworkInterface{{Name: "eth0", RXBytes: 500, TXBytes: 800}, {Name: "eth1", RXBytes: 9000, TXBytes: 9000}})
	if !second.Ready || second.RXBytesPerSecond != 200 || second.TXBytesPerSecond != 300 {
		t.Fatalf("unexpected rates: %+v", second)
	}
	if first.Interfaces[0].RXBytes != 100 {
		t.Fatal("sampling mutated a published snapshot")
	}
	cached := s.sample(at.Add(2100*time.Millisecond), []NetworkInterface{{Name: "eth0", RXBytes: 9999999}})
	if cached != second {
		t.Fatal("frequent callers must not replace heartbeat rate with a tiny sampling window")
	}
	reset := s.sample(at.Add(4*time.Second), []NetworkInterface{{Name: "eth0", RXBytes: 1, TXBytes: 2}})
	if reset.RXBytesPerSecond != 0 || reset.TXBytesPerSecond != 0 {
		t.Fatal("counter reset underflow")
	}
	removed := s.sample(at.Add(6*time.Second), []NetworkInterface{{Name: "eth1", RXBytes: 999999, TXBytes: 999999}})
	if removed.RXBytesPerSecond != 0 {
		t.Fatal("reappearing interface needs a new baseline")
	}
}
func TestParseNetworkCounters(t *testing.T) {
	input := "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n lo: 999 0 0 0 0 0 0 0 999 0 0 0 0 0 0 0\n eth0: 123 0 0 0 0 0 0 0 456 0 0 0 0 0 0 0\n"
	interfaces, err := parseNetwork(strings.NewReader(input))
	if err != nil || len(interfaces) != 1 || interfaces[0].RXBytes != 123 || interfaces[0].TXBytes != 456 {
		t.Fatalf("invalid parse: %+v %v", interfaces, err)
	}
	for _, bad := range []string{"", "eth0: 1 2", "eth0: bad 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0"} {
		if _, err := parseNetwork(strings.NewReader(bad)); err == nil {
			t.Fatal("malformed counters must be unavailable, not zero")
		}
	}
}
