package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type tagCredentialOutput struct{ bytes.Buffer }

func (b *tagCredentialOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16<<10 {
		return 0, errors.New("Credential helper output limit")
	}
	return b.Buffer.Write(p)
}

type tagCredential struct{ Username, Password string }

var registryHelperName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func dockerCredentialHost(key string) string {
	if key == "https://index.docker.io/v1/" || key == "https://index.docker.io/v1" || key == "docker.io" || key == "index.docker.io" {
		return "registry-1.docker.io"
	}
	key = strings.TrimPrefix(strings.TrimPrefix(key, "https://"), "http://")
	if strings.Contains(key, "/") {
		return ""
	}
	host, _ := canonicalRegistryHost(key)
	return host
}
func loadRegistryTagCredential(ctx context.Context, registry string) (tagCredential, error) {
	var empty tagCredential
	// Match DockerExecutor's sanitized environment: HOME, not inherited DOCKER_CONFIG.
	home, e := os.UserHomeDir()
	if e != nil {
		return empty, tagError("authentication_required", "Registry authentication required; Docker credentials are unavailable on this host")
	}
	path := filepath.Join(home, ".docker", "config.json")
	info, e := os.Lstat(path)
	if e != nil {
		return empty, tagError("authentication_required", "Registry authentication required; mount Docker credentials on this host")
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return empty, tagError("authentication_required", "Docker credential configuration is unavailable or unsafe")
	}
	file, e := os.Open(path)
	if e != nil {
		return empty, tagError("authentication_required", "Docker credential configuration cannot be read")
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return empty, tagError("authentication_required", "Docker credential configuration changed")
	}
	raw, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return empty, tagError("authentication_required", "Docker credential configuration is too large")
	}
	var cfg struct {
		Auths map[string]struct {
			Auth          string `json:"auth"`
			IdentityToken string `json:"identitytoken"`
		}
		CredHelpers map[string]string `json:"credHelpers"`
		CredsStore  string            `json:"credsStore"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return empty, tagError("authentication_required", "Docker credential configuration is invalid")
	}
	helper, key := cfg.CredsStore, registry
	for name, value := range cfg.CredHelpers {
		if dockerCredentialHost(name) == registry {
			helper = value
			key = name
			break
		}
	}
	if registry == "registry-1.docker.io" && key == registry {
		key = "https://index.docker.io/v1/"
	}
	if helper != "" {
		if !registryHelperName.MatchString(helper) {
			return empty, tagError("authentication_required", "Docker credential helper name is invalid")
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker-credential-"+helper, "get")
		cmd.WaitDelay = time.Second
		cmd.Stdin = strings.NewReader(key + "\n")
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C"}
		var output tagCredentialOutput
		cmd.Stdout = &output
		cmd.Stderr = io.Discard
		if cmd.Run() != nil || output.Len() > 16<<10 {
			return empty, tagError("authentication_required", "Registry authentication required; the configured Docker credential helper is unavailable")
		}
		var found struct{ Username, Secret string }
		if json.Unmarshal(output.Bytes(), &found) != nil || found.Username == "" || found.Secret == "" || found.Username == "<token>" || len(found.Username) > 16384 || len(found.Secret) > 16384 || strings.Contains(found.Username, ":") {
			return empty, tagError("authentication_required", "Registry authentication requires a supported Docker username/token credential")
		}
		return tagCredential{found.Username, found.Secret}, nil
	}
	for name, entry := range cfg.Auths {
		if dockerCredentialHost(name) != registry {
			continue
		}
		if opened.Mode().Perm()&0077 != 0 {
			return empty, tagError("authentication_required", "Docker credentials must be stored in a private file (0600)")
		}
		if entry.IdentityToken != "" {
			return empty, tagError("authentication_required", "Identity-token login is not supported for tag browsing; use a scoped Docker username/token credential")
		}
		decoded, e := base64.StdEncoding.DecodeString(entry.Auth)
		user, password, ok := bytes.Cut(decoded, []byte(":"))
		if e != nil || !ok || len(user) == 0 || len(password) == 0 || len(user) > 16384 || len(password) > 16384 {
			return empty, tagError("authentication_required", "Docker registry credentials are invalid")
		}
		return tagCredential{string(user), string(password)}, nil
	}
	return empty, tagError("authentication_required", "Registry authentication required; no Docker login for this registry is available on this host")
}
