package networking

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
)

// PrivateIPv4 returns the most likely LAN address on this machine. Physical
// RFC1918 interfaces are preferred over VPN, container and virtual adapters.
func PrivateIPv4() (netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return netip.Addr{}, err
	}
	type candidate struct {
		addr  netip.Addr
		score int
	}
	var candidates []candidate
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		virtual := isVirtualInterface(iface.Name)
		addrs, addrErr := iface.Addrs()
		if addrErr != nil {
			continue
		}
		for _, raw := range addrs {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr != nil {
				continue
			}
			addr := prefix.Addr().Unmap()
			if !addr.Is4() || addr.IsLoopback() || !addr.IsPrivate() {
				continue
			}
			score := 100
			if virtual {
				score -= 50
			}
			if addr.Is4() && strings.HasPrefix(addr.String(), "192.168.") {
				score += 10
			}
			candidates = append(candidates, candidate{addr: addr, score: score})
		}
	}
	if len(candidates) == 0 {
		return netip.Addr{}, fmt.Errorf("no private IPv4 LAN address found")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].addr.Less(candidates[j].addr)
	})
	return candidates[0].addr, nil
}

func PublicEndpoint(listenAddr string) (string, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("invalid host listen address %q: %w", listenAddr, err)
	}
	addr, err := PrivateIPv4()
	if err != nil {
		return "", err
	}
	return "http://" + net.JoinHostPort(addr.String(), port), nil
}

func isVirtualInterface(name string) bool {
	name = strings.ToLower(name)
	for _, marker := range []string{"docker", "wsl", "vethernet", "hyper-v", "vmware", "virtualbox", "loopback", "tailscale", "zerotier"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}
