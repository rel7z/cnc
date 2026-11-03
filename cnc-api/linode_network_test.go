package cnc

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLinodeConfiguredPrivateNetwork(t *testing.T) {
	l := newLinodeIntegration(t.TempDir())
	l.tcpAddr = "172.31.4.149:9090"
	l.httpAddr = "172.31.4.149:8080"
	n := linodeNetwork{Interface: "cncln0", Endpoint: "203.0.113.2:51820", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), ServerAddr: l.tcpAddr, HTTPAddr: l.httpAddr, AWSRegion: "us-east-2", SecurityGroup: "sg-test"}
	if err := os.MkdirAll(l.dir, 0700); err != nil {
		t.Fatal(err)
	}
	save := func() {
		data, _ := json.Marshal(n)
		if err := os.WriteFile(filepath.Join(l.dir, "network.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	// A configured encrypted cross-cloud connection does not require a same-region Linode controller.
	a, b, err := l.workerAddresses(t.Context(), "unused", "", "", "sg-sin-2")
	if err != nil || a != l.tcpAddr || b != l.httpAddr {
		t.Fatal(a, b, err)
	}
	connection, err := l.connection(t.Context(), "unused")
	if err != nil || connection.Network != "WireGuard" {
		t.Fatal(connection, err)
	}
	for _, kind := range []string{"listener", "key", "interface", "port"} {
		original := n
		switch kind {
		case "listener":
			n.ServerAddr = "172.31.4.150:9090"
		case "key":
			n.PublicKey = "invalid"
		case "interface":
			n.Interface = "cncln0;false"
		case "port":
			n.Endpoint = "203.0.113.2:9090"
		}
		save()
		if _, err := l.network(); err == nil {
			t.Fatalf("accepted invalid %s", kind)
		}
		n = original
	}
}
