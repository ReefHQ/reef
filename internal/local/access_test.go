package local

import (
	"bytes"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestAccessURLsUsePublishedAddressPortAndIPFamily(t *testing.T) {
	addresses := []net.IP{net.ParseIP("192.168.2.200"), net.ParseIP("10.0.0.5"), net.ParseIP("192.168.2.200"), net.ParseIP("127.0.0.1"), net.ParseIP("169.254.1.1"), net.ParseIP("2001:db8::10"), nil}
	for _, tc := range []struct {
		name, binding string
		want          []string
	}{
		{"loopback", "127.0.0.1:53704", []string{"http://127.0.0.1:53704"}},
		{"explicit network", "192.168.2.200:8099", []string{"http://192.168.2.200:8099"}},
		{"wildcard IPv4", "0.0.0.0:9000", []string{"http://10.0.0.5:9000", "http://192.168.2.200:9000", "http://127.0.0.1:9000"}},
		{"explicit IPv6", "[2001:db8::20]:8099", []string{"http://[2001:db8::20]:8099"}},
		{"wildcard IPv6", "[::]:8099", []string{"http://[2001:db8::10]:8099", "http://[::1]:8099"}},
		{"deduplicated bindings", "127.0.0.1:8099\n127.0.0.1:8099", []string{"http://127.0.0.1:8099"}},
		{"invalid port", "0.0.0.0:99999", nil},
		{"invalid binding", "not a binding", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessURLs(tc.binding, addresses); !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, tc.want)
			}
		})
	}
}
func TestPrintedAccessUsesDockerBindingRatherThanSavedPort(t *testing.T) {
	l := fixture(t)
	var out bytes.Buffer
	l.out = &out
	l.addresses = func() ([]net.IP, error) { return []net.IP{net.ParseIP("192.168.2.200")}, nil }
	l.run = func(args ...string) ([]byte, error) {
		want := []string{"--context", "local", "compose", "--project-name", "fixture-ui", "port", "reef", "8099"}
		if !reflect.DeepEqual(args, want) {
			t.Fatal(args)
		}
		return []byte("0.0.0.0:9000\n"), nil
	}
	l.printAccess([]string{"--context", "local", "compose", "--project-name", "fixture-ui"})
	text := out.String()
	if !strings.Contains(text, "Access: network.\n") || !strings.Contains(text, "Reef: http://192.168.2.200:9000\n") || !strings.Contains(text, "http://127.0.0.1:9000") || strings.Contains(text, "http://0.0.0.0") {
		t.Fatal(text)
	}
}
func TestUnavailablePublicationNeverPrintsInventedAccessURL(t *testing.T) {
	l := fixture(t)
	var out bytes.Buffer
	l.out = &out
	l.run = func(...string) ([]byte, error) { return nil, errors.New("No published port") }
	l.printAccess(nil)
	if strings.Contains(out.String(), "http://") || !strings.Contains(out.String(), "URL unavailable") {
		t.Fatal(out.String())
	}
}
func TestWildcardWithoutHostAddressReportsUncertainty(t *testing.T) {
	l := fixture(t)
	var out bytes.Buffer
	l.out = &out
	l.run = func(...string) ([]byte, error) { return []byte("0.0.0.0:8099"), nil }
	l.addresses = func() ([]net.IP, error) { return nil, errors.New("No interfaces") }
	l.printAccess(nil)
	if !strings.Contains(out.String(), "http://127.0.0.1:8099") || !strings.Contains(out.String(), "Network address unavailable") {
		t.Fatal(out.String())
	}
}

func TestLocalAccessExplainsHowToEnableLAN(t *testing.T) {
	l := fixture(t)
	var out bytes.Buffer
	l.out = &out
	l.run = func(...string) ([]byte, error) { return []byte("127.0.0.1:53704"), nil }
	l.printAccess(nil)
	if !strings.Contains(out.String(), "Access: this machine only.") || !strings.Contains(out.String(), "./reef-local.sh up --lan") || !strings.Contains(out.String(), "http://127.0.0.1:53704") || strings.Contains(out.String(), "Access: network.") {
		t.Fatal(out.String())
	}
}
