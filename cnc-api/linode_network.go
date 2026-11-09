package cnc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type linodeNetwork struct {
	Interface     string `json:"interface"`
	Endpoint      string `json:"endpoint"`
	PublicKey     string `json:"public_key"`
	ServerAddr    string `json:"server_addr"`
	HTTPAddr      string `json:"http_addr"`
	AWSRegion     string `json:"aws_region"`
	SecurityGroup string `json:"security_group"`
}
type linodePeer struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

func (l *linodeIntegration) network() (*linodeNetwork, error) {
	data, err := os.ReadFile(filepath.Join(l.dir, "network.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	var n linodeNetwork
	if err != nil || json.Unmarshal(data, &n) != nil {
		return nil, fmt.Errorf("private worker network configuration unavailable")
	}
	key, keyErr := base64.StdEncoding.DecodeString(n.PublicKey)
	if n.Interface != "cncln0" || keyErr != nil || len(key) != 32 || n.AWSRegion != "us-east-2" || !strings.HasPrefix(n.SecurityGroup, "sg-") {
		return nil, fmt.Errorf("invalid private worker network configuration")
	}
	h, p, e := net.SplitHostPort(n.Endpoint)
	if e != nil || net.ParseIP(h) == nil || p != "51820" {
		return nil, fmt.Errorf("invalid private network endpoint")
	}
	if _, e = linodeWorkerScript("validation-worker", n.ServerAddr, n.HTTPAddr); e != nil {
		return nil, e
	}
	if n.ServerAddr != l.tcpAddr || n.HTTPAddr != l.httpAddr {
		return nil, fmt.Errorf("private network listeners do not match controller configuration")
	}
	return &n, nil
}

func networkCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("private network operation failed; inspect network service status")
	}
	return out, nil
}

// Called only while creation holds the integration mutex. Peers and keys survive retries.
func (l *linodeIntegration) privateWorkerScript(ctx context.Context, label, serverAddr, httpAddr string) (string, error) {
	script, err := linodeWorkerScript(label, serverAddr, httpAddr)
	if err != nil {
		return "", err
	}
	network, err := l.network()
	if err != nil || network == nil {
		return script, err
	}
	if serverAddr != network.ServerAddr || httpAddr != network.HTTPAddr {
		return "", fmt.Errorf("address overrides do not match the private worker network")
	}
	dir := filepath.Join(l.dir, "peers")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot persist private network peer")
	}
	path := filepath.Join(dir, label+".json")
	var peer linodePeer
	if data, e := os.ReadFile(path); e == nil {
		if json.Unmarshal(data, &peer) != nil {
			return "", fmt.Errorf("invalid saved network peer")
		}
	} else if !os.IsNotExist(e) {
		return "", fmt.Errorf("cannot read saved network peer")
	} else {
		used := map[string]bool{}
		entries, e := os.ReadDir(dir)
		if e != nil {
			return "", e
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				var existing linodePeer
				b, e := os.ReadFile(filepath.Join(dir, entry.Name()))
				if e != nil || json.Unmarshal(b, &existing) != nil {
					return "", fmt.Errorf("cannot inspect reserved peer addresses")
				}
				used[existing.Address] = true
			}
		}
		for i := 2; i < 255; i++ {
			candidate := fmt.Sprintf("10.203.88.%d", i)
			if !used[candidate] {
				peer.Address = candidate
				break
			}
		}
		if peer.Address == "" {
			return "", fmt.Errorf("private worker network is full")
		}
		key, e := networkCommand(ctx, "wg", "genkey")
		if e != nil {
			return "", e
		}
		peer.PrivateKey = strings.TrimSpace(string(key))
		command := exec.CommandContext(ctx, "wg", "pubkey")
		command.Stdin = strings.NewReader(peer.PrivateKey)
		public, e := command.Output()
		if e != nil {
			return "", fmt.Errorf("cannot generate network peer")
		}
		peer.PublicKey = strings.TrimSpace(string(public))
		data, _ := json.Marshal(peer)
		if os.WriteFile(path, data, 0600) != nil {
			return "", fmt.Errorf("cannot save network peer")
		}
	}
	if _, err = networkCommand(ctx, "wg", "set", network.Interface, "peer", peer.PublicKey, "allowed-ips", peer.Address+"/32"); err != nil {
		return "", err
	}
	persisted, err := networkCommand(ctx, "wg", "showconf", network.Interface)
	if err != nil {
		return "", err
	}
	conf := strings.Replace(string(persisted), "[Interface]\n", "[Interface]\nAddress = 10.203.88.1/24\n", 1)
	if os.WriteFile("/etc/wireguard/"+network.Interface+".conf", []byte(conf), 0600) != nil {
		return "", fmt.Errorf("cannot persist controller network peers")
	}
	host, _, _ := net.SplitHostPort(serverAddr)
	wg := fmt.Sprintf("apt-get install -y -qq wireguard-tools\ninstall -d -m 700 /etc/wireguard\ncat > /etc/wireguard/cncln0.conf <<'WIREGUARD'\n[Interface]\nAddress = %s/32\nPrivateKey = %s\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = %s/32\nPersistentKeepalive = 25\nWIREGUARD\nchmod 600 /etc/wireguard/cncln0.conf\nsystemctl enable --now wg-quick@cncln0\n", peer.Address, peer.PrivateKey, network.PublicKey, network.Endpoint, host)
	needle := "install -d -m 700 /opt/cnc/data\n"
	return strings.Replace(script, needle, wg+needle, 1), nil
}

func (l *linodeIntegration) allowWorkerNetwork(ctx context.Context, raw json.RawMessage) error {
	n, err := l.network()
	if err != nil || n == nil {
		return err
	}
	var instance struct {
		IPv4 []string `json:"ipv4"`
	}
	if json.Unmarshal(raw, &instance) != nil {
		return fmt.Errorf("cannot read created worker IP")
	}
	ip := ""
	for _, value := range instance.IPv4 {
		parsed := net.ParseIP(value)
		if parsed != nil && !parsed.IsPrivate() && !parsed.IsLoopback() && !parsed.IsUnspecified() {
			ip = value
			break
		}
	}
	if ip == "" {
		return fmt.Errorf("created worker has no public network address")
	}
	// Rule applies only to the encrypted UDP endpoint and this worker's exact IP.
	var group struct {
		SecurityGroups []struct {
			Permissions []struct {
				Protocol string `json:"IpProtocol"`
				From     int    `json:"FromPort"`
				To       int    `json:"ToPort"`
				Ranges   []struct {
					CIDR string `json:"CidrIp"`
				} `json:"IpRanges"`
			} `json:"IpPermissions"`
		} `json:"SecurityGroups"`
	}
	out, err := awsCLI(ctx, "ec2", "describe-security-groups", "--group-ids", n.SecurityGroup, "--region", n.AWSRegion, "--output", "json")
	if err != nil || json.Unmarshal(out, &group) != nil {
		return fmt.Errorf("worker created; private network firewall check failed")
	}
	for _, g := range group.SecurityGroups {
		for _, p := range g.Permissions {
			if p.Protocol == "udp" && p.From == 51820 && p.To == 51820 {
				for _, r := range p.Ranges {
					if r.CIDR == ip+"/32" {
						return nil
					}
				}
			}
		}
	}
	permission, _ := json.Marshal([]any{map[string]any{"IpProtocol": "udp", "FromPort": 51820, "ToPort": 51820, "IpRanges": []any{map[string]string{"CidrIp": ip + "/32", "Description": "CNC Linode private worker"}}}})
	if _, err = awsCLI(ctx, "ec2", "authorize-security-group-ingress", "--group-id", n.SecurityGroup, "--ip-permissions", string(permission), "--region", n.AWSRegion, "--output", "json"); err != nil {
		return fmt.Errorf("worker created; private network firewall update pending")
	}
	return nil
}
