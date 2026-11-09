package cnc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
)

type linodeConnection struct {
	ServerAddr string `json:"server_addr"`
	HTTPAddr   string `json:"http_addr"`
	Region     string `json:"region"`
	Network    string `json:"network,omitempty"`
}

// Match the actual controller's interfaces, never a label or browser-supplied host.
func (l *linodeIntegration) connection(ctx context.Context, token string) (linodeConnection, error) {
	if network, err := l.network(); err != nil {
		return linodeConnection{}, err
	} else if network != nil {
		return linodeConnection{ServerAddr: network.ServerAddr, HTTPAddr: network.HTTPAddr, Network: "WireGuard"}, nil
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return linodeConnection{}, fmt.Errorf("cannot detect controller network interfaces")
	}
	local := map[string]bool{}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			local[ip.String()] = true
		}
	}
	instances, err := l.list(ctx, token, "linode/instances")
	if err != nil {
		return linodeConnection{}, err
	}
	return resolveLinodeConnection(l.tcpAddr, l.httpAddr, local, instances)
}

func resolveLinodeConnection(tcpAddr, httpAddr string, local map[string]bool, instances []json.RawMessage) (linodeConnection, error) {
	tcpHost, tcpPort, tcpErr := net.SplitHostPort(tcpAddr)
	httpHost, httpPort, httpErr := net.SplitHostPort(httpAddr)
	if tcpErr != nil || httpErr != nil {
		return linodeConnection{}, fmt.Errorf("controller listener addresses are not configured")
	}
	accepts := func(bound, candidate string) bool {
		ip := net.ParseIP(bound)
		return bound == "" || (ip != nil && ip.IsUnspecified()) || bound == candidate
	}
	matches := []linodeConnection{}
	for _, raw := range instances {
		var instance struct {
			Region string   `json:"region"`
			IPv4   []string `json:"ipv4"`
		}
		if json.Unmarshal(raw, &instance) != nil {
			continue
		}
		own := false
		for _, address := range instance.IPv4 {
			if local[address] {
				own = true
			}
		}
		if !own {
			continue
		}
		for _, address := range instance.IPv4 {
			ip := net.ParseIP(address)
			if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || !local[address] || !accepts(tcpHost, address) || !accepts(httpHost, address) {
				continue
			}
			matches = append(matches, linodeConnection{ServerAddr: net.JoinHostPort(address, tcpPort), HTTPAddr: net.JoinHostPort(address, httpPort), Region: instance.Region})
		}
	}
	if len(matches) != 1 {
		return linodeConnection{}, fmt.Errorf("cannot automatically find a Linode controller reachable over private networking; a controller on AWS requires separately configured cross-cloud networking")
	}
	if _, err := linodeWorkerScript("validation-worker", matches[0].ServerAddr, matches[0].HTTPAddr); err != nil {
		return linodeConnection{}, err
	}
	return matches[0], nil
}

func (l *linodeIntegration) workerAddresses(ctx context.Context, token, serverAddr, httpAddr, region string) (string, string, error) {
	if serverAddr != "" && httpAddr != "" {
		return serverAddr, httpAddr, nil
	}
	if serverAddr != "" || httpAddr != "" {
		return "", "", fmt.Errorf("provide both advanced addresses or leave both automatic")
	}
	connection, err := l.connection(ctx, token)
	if err != nil {
		return "", "", err
	}
	if region != "" && connection.Region != "" && connection.Region != region {
		return "", "", fmt.Errorf("automatic private networking requires the controller's region: %s", connection.Region)
	}
	return connection.ServerAddr, connection.HTTPAddr, nil
}
