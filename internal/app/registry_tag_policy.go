package app

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type registryTagError struct{ Code, Message string }

func (e *registryTagError) Error() string { return e.Message }
func tagError(code, message string) error { return &registryTagError{code, message} }

var registryHostname = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)

func canonicalRegistryHost(host string) (string, error) {
	u, e := url.Parse("https://" + host)
	if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return "", errors.New("Invalid registry host")
	}
	name := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if name == "" || !registryHostname.MatchString(name) {
		return "", errors.New("Invalid registry hostname")
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("Invalid registry port")
		}
	}
	if port != "" && port != "443" {
		return net.JoinHostPort(name, port), nil
	}
	return name, nil
}
func registryRealm(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return nil, errors.New("Invalid registry authentication endpoint")
	}
	host, e := canonicalRegistryHost(u.Host)
	if e != nil {
		return nil, e
	}
	u.Host = host
	return u, nil
}
func validateRegistryTagConfig(c Config) error {
	if len(c.RegistryTagHosts) > 64 || len(c.RegistryTagAuthRealms) > 64 {
		return errors.New("Too many registry tag settings")
	}
	for _, host := range c.RegistryTagHosts {
		if _, e := canonicalRegistryHost(host); e != nil {
			return errors.New("registry_tag_hosts must contain exact host[:port] names")
		}
	}
	for host, realm := range c.RegistryTagAuthRealms {
		if _, e := canonicalRegistryHost(host); e != nil {
			return errors.New("Invalid registry_tag_auth_realms host")
		}
		if _, e := registryRealm(realm); e != nil {
			return errors.New("registry_tag_auth_realms values must be HTTPS endpoints without credentials, query or fragment")
		}
	}
	return nil
}
func publicRegistryAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "240.0.0.0/4", "fec0::/10", "::/96", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		if netip.MustParsePrefix(s).Contains(addr) {
			return false
		}
	}
	return true
}
func registryTagHTTPClient(c Config) (*http.Client, error) {
	if e := validateRegistryTagConfig(c); e != nil {
		return nil, e
	}
	allowed := map[string]bool{}
	for _, h := range c.RegistryTagHosts {
		key, _ := canonicalRegistryHost(h)
		allowed[key] = true
	}
	for _, raw := range c.RegistryTagAuthRealms {
		u, _ := registryRealm(raw)
		allowed[u.Host] = true
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		key, _ := canonicalRegistryHost(net.JoinHostPort(host, port))
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, tagError("registry_unavailable", "Registry DNS lookup failed")
		}
		if !allowed[key] {
			for _, ip := range ips {
				if !publicRegistryAddress(ip) {
					return nil, tagError("registry_not_authorized", "Private registry addresses require an exact registry_tag_hosts entry on this host")
				}
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func trustedTagRealm(c Config, registry string, u *url.URL) bool {
	host, e := canonicalRegistryHost(u.Host)
	if e != nil {
		return false
	}
	if host == registry {
		return true
	}
	if registry == "registry-1.docker.io" && u.String() == "https://auth.docker.io/token" {
		return true
	}
	if registry == "lscr.io" && u.String() == "https://ghcr.io/token" {
		return true
	}
	for name, raw := range c.RegistryTagAuthRealms {
		key, _ := canonicalRegistryHost(name)
		configured, e := registryRealm(raw)
		if e == nil && key == registry && configured.String() == u.String() {
			return true
		}
	}
	return false
}
