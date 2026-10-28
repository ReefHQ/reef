package local

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

func hostAddresses() ([]net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Docker/VM bridges are not addresses of the server on its client network.
		virtual := false
		for _, prefix := range []string{"docker", "br-", "veth", "virbr", "vmnet"} {
			if strings.HasPrefix(iface.Name, prefix) {
				virtual = true
				break
			}
		}
		if virtual {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				ips = append(ips, ip)
			}
		}
	}
	return ips, nil
}

func accessURLs(published string, addresses []net.IP) []string {
	seen := map[string]bool{}
	var network, local []string
	add := func(ip net.IP, port string) {
		address := "http://" + net.JoinHostPort(ip.String(), port)
		if seen[address] {
			return
		}
		seen[address] = true
		if ip.IsLoopback() {
			local = append(local, address)
		} else {
			network = append(network, address)
		}
	}
	for _, binding := range strings.Fields(published) {
		host, port, err := net.SplitHostPort(binding)
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil {
			continue
		}
		if !ip.IsUnspecified() {
			add(ip, port)
			continue
		}
		ipv4 := ip.To4() != nil
		for _, candidate := range addresses {
			if candidate != nil && candidate.IsGlobalUnicast() && !candidate.IsLoopback() && !candidate.IsLinkLocalUnicast() && (candidate.To4() != nil) == ipv4 {
				add(candidate, port)
			}
		}
		if ipv4 {
			add(net.ParseIP("127.0.0.1"), port)
		} else {
			add(net.ParseIP("::1"), port)
		}
	}
	sort.Strings(network)
	sort.Strings(local)
	return append(network, local...)
}

func (l *Launcher) printAccess(compose []string) {
	args := append(append([]string{}, compose...), "port", "reef", "8099")
	published, err := l.run(args...)
	if err != nil || len(strings.TrimSpace(string(published))) == 0 {
		fmt.Fprintln(l.out, "Reef URL unavailable: Docker is not reporting a published port for Reef.")
	} else {
		addresses, _ := l.addresses()
		urls := accessURLs(string(published), addresses)
		wildcard := strings.Contains(string(published), "0.0.0.0:") || strings.Contains(string(published), "[::]:")
		if len(urls) > 0 {
			if wildcard || !onlyLoopback(urls) {
				fmt.Fprintln(l.out, "Access: network.")
				fmt.Fprintln(l.out, "To restrict access to this machine, run: ./reef-local.sh up --local")
			} else {
				fmt.Fprintln(l.out, "Access: this machine only.")
				fmt.Fprintln(l.out, "To allow access from other computers, run: ./reef-local.sh up --lan")
			}
		}
		for i, address := range urls {
			label := "Reef"
			if i > 0 {
				label = "Reef (additional address)"
			}
			fmt.Fprintf(l.out, "%s: %s\n", label, address)
		}
		if len(urls) == 0 {
			fmt.Fprintln(l.out, "Reef URL unavailable: Docker returned an unsupported port binding.")
		}
		if wildcard && onlyLoopback(urls) {
			fmt.Fprintln(l.out, "Network address unavailable: use this server's network IP with the published port.")
		}
	}
	fmt.Fprintln(l.out, "Password: ./reef-local.sh password")
}
func onlyLoopback(urls []string) bool {
	for _, address := range urls {
		if !strings.HasPrefix(address, "http://127.") && !strings.HasPrefix(address, "http://[::1]:") {
			return false
		}
	}
	return true
}
