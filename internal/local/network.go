package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const launcherUsage = "Usage: ./reef-local.sh [up [--lan|--local]|stop|status|password|recover-admin USERNAME]"

func networkMode(action string, options []string) (string, error) {
	mode := ""
	for _, option := range options {
		if action != "up" || (option != "--lan" && option != "--local") {
			return "", errors.New(launcherUsage)
		}
		next := strings.TrimPrefix(option, "--")
		if mode != "" && mode != next {
			return "", errors.New("Choose either --lan or --local, not both")
		}
		mode = next
	}
	return mode, nil
}

func (l *Launcher) ensureNetworkMode(mode string) error {
	if mode == "" {
		return nil
	}
	address := "127.0.0.1"
	if mode == "lan" {
		address = "0.0.0.0"
	}
	path := filepath.Join(l.runtime(), "compose.json")
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Reef compose.json must be a regular file, not a symlink")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var compose map[string]any
	if err = json.Unmarshal(original, &compose); err != nil {
		return err
	}
	services, ok := compose["services"].(map[string]any)
	if !ok {
		return errors.New("Cannot find Reef services in compose.json")
	}
	service, ok := services["reef"].(map[string]any)
	if !ok {
		return errors.New("Cannot find the Reef service in compose.json")
	}
	ports, ok := service["ports"].([]any)
	if !ok {
		return errors.New("Configure one explicit TCP host port for Reef port 8099 before changing network access")
	}
	found, changed := 0, false
	for index, port := range ports {
		switch value := port.(type) {
		case string:
			mapping, protocol, explicitProtocol := strings.Cut(value, "/")
			if explicitProtocol && protocol != "tcp" {
				continue
			}
			separator := strings.LastIndex(mapping, ":")
			if separator < 0 || mapping[separator+1:] != "8099" {
				continue
			}
			prefix := mapping[:separator]
			host, published, e := net.SplitHostPort(prefix)
			if e != nil {
				if strings.Contains(prefix, ":") {
					return errors.New("Unsupported Reef TCP port publication; review compose.json")
				}
				host, published = "", prefix
			}
			if !validPublishedPort(published) {
				return errors.New("Configure a fixed TCP host port for Reef before changing network access")
			}
			found++
			if host == address {
				continue
			}
			next := net.JoinHostPort(address, published) + ":8099"
			if explicitProtocol {
				next += "/tcp"
			}
			ports[index] = next
			changed = true
		case map[string]any:
			if fmt.Sprint(value["target"]) != "8099" {
				continue
			}
			if protocol, exists := value["protocol"]; exists && protocol != "tcp" {
				continue
			}
			if !validPublishedPort(fmt.Sprint(value["published"])) {
				return errors.New("Configure a fixed TCP host port for Reef before changing network access")
			}
			found++
			if value["host_ip"] != address {
				value["host_ip"] = address
				changed = true
			}
		}
	}
	if found != 1 {
		return errors.New("Configure exactly one explicit TCP host port for Reef port 8099 before changing network access")
	}
	if !changed {
		return nil
	}
	return l.writeCompose(original, compose, info)
}
func validPublishedPort(value string) bool {
	n, err := strconv.Atoi(value)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == value
}
