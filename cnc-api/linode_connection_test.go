package cnc

import (
	"encoding/json"
	"testing"
)

func TestLinodeAutomaticConnection(t *testing.T) {
	instances := []json.RawMessage{json.RawMessage(`{"label":"renamed-controller","region":"sg-sin-2","ipv4":["203.0.113.4","192.168.145.81"]}`)}
	local := map[string]bool{"203.0.113.4": true, "192.168.145.81": true}
	for _, bound := range []string{"192.168.145.81", "0.0.0.0", ""} {
		got, err := resolveLinodeConnection(bound+":9190", bound+":8180", local, instances)
		if err != nil || got.ServerAddr != "192.168.145.81:9190" || got.HTTPAddr != "192.168.145.81:8180" || got.Region != "sg-sin-2" {
			t.Fatalf("bound=%q got=%+v err=%v", bound, got, err)
		}
	}
}

func TestLinodeAutomaticConnectionRejectsUnreachableOrAmbiguous(t *testing.T) {
	instance := json.RawMessage(`{"region":"sg-sin-2","ipv4":["192.168.145.81"]}`)
	for _, tc := range []struct {
		name, tcp, http string
		local           map[string]bool
		instances       []json.RawMessage
	}{
		{"another cloud", "172.31.4.149:9090", "172.31.4.149:8080", map[string]bool{"172.31.4.149": true}, []json.RawMessage{instance}},
		{"loopback listener", "127.0.0.1:9090", "127.0.0.1:8080", map[string]bool{"192.168.145.81": true}, []json.RawMessage{instance}},
		{"different listeners", "192.168.145.81:9090", "203.0.113.4:8080", map[string]bool{"192.168.145.81": true}, []json.RawMessage{instance}},
		{"ambiguous controller", ":9090", ":8080", map[string]bool{"192.168.145.81": true}, []json.RawMessage{instance, instance}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolveLinodeConnection(tc.tcp, tc.http, tc.local, tc.instances); err == nil {
				t.Fatal("accepted unreachable or ambiguous connection")
			}
		})
	}
}

func TestLinodeAddressesRequireCompleteOverride(t *testing.T) {
	l := newLinodeIntegration(t.TempDir())
	if _, _, err := l.workerAddresses(t.Context(), "unused", "192.168.1.2:9090", "", ""); err == nil {
		t.Fatal("accepted partial override")
	}
	a, b, err := l.workerAddresses(t.Context(), "unused", "192.168.1.2:9090", "192.168.1.2:8080", "")
	if err != nil || a != "192.168.1.2:9090" || b != "192.168.1.2:8080" {
		t.Fatal(a, b, err)
	}
}
