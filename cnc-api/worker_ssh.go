package cnc

import "net"

// Prefer a public interface for direct SSH. The controller's observed worker
// address remains available when a host only has private networking.
func publicWorkerIP() string {
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			return ip.String()
		}
	}
	return ""
}
