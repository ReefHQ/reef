package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func registryErrorCode(e error) string {
	var classified *registryTagError
	if errors.As(e, &classified) {
		return classified.Code
	}
	return ""
}
func TestRegistryV2AnonymousPaginationAndSearchBounds(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "quay.io" || r.URL.Path != "/v2/team/app/tags/list" || r.Header.Get("Authorization") != "" {
			t.Fatal(r)
		}
		tags := []string{}
		start := 0
		if r.URL.Query().Get("last") != "" {
			fmt.Sscanf(r.URL.Query().Get("last"), "tag-%04d", &start)
			start++
		}
		n := 50
		if r.URL.Query().Get("n") == "1000" {
			n = 1000
		}
		for i := start; i < start+n; i++ {
			tags = append(tags, fmt.Sprintf("tag-%04d", i))
		}
		raw, _ := json.Marshal(map[string]any{"name": "team/app", "tags": tags})
		response := reefTagResponse(200, string(raw))
		response.Header.Set("Link", `<https://untrusted.invalid/ignored>; rel="next"`)
		return response, nil
	})}
	cred := func(context.Context, string) (tagCredential, error) {
		t.Fatal("Anonymous repository requested credentials")
		return tagCredential{}, nil
	}
	first, e := genericRegistryTagPage(context.Background(), Config{}, "quay.io", "team/app", "", "", 1, client, cred)
	if e != nil || len(first.Tags) != 50 || first.NextCursor != "tag-0049" || first.Total != -1 || !first.More {
		t.Fatal(first, e)
	}
	next, e := genericRegistryTagPage(context.Background(), Config{}, "quay.io", "team/app", "", first.NextCursor, 2, client, cred)
	if e != nil || next.Tags[0] != "tag-0050" {
		t.Fatal(next, e)
	}
	calls = 0
	result, e := genericRegistryTagPage(context.Background(), Config{}, "quay.io", "team/app", "latest", "", 1, client, cred)
	if e != nil || calls != 4 || !result.More || result.NextCursor != "tag-3999" || len(result.Tags) != 0 {
		t.Fatal(result, e, calls)
	}
}
func TestRegistryV2BasicAndBearerCredentialsStayOnTrustedEndpoints(t *testing.T) {
	for _, scheme := range []string{"Basic", "Bearer"} {
		t.Run(scheme, func(t *testing.T) {
			credentialCalls, tokenCalls := 0, 0
			client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "registry.example" {
					t.Fatal("Unexpected destination", r.URL.Host)
				}
				if r.URL.Path == "/jwt/auth" {
					tokenCalls++
					if r.URL.Query().Get("scope") != "repository:group/app:pull" {
						t.Fatal("Scope not constrained")
					}
					if u, p, ok := r.BasicAuth(); !ok {
						resp := reefTagResponse(401, "")
						return resp, nil
					} else if u != "fixture-user" || p != "fixture-secret" {
						t.Fatal("Wrong synthetic credentials")
					}
					return reefTagResponse(200, `{"access_token":"fixture-token"}`), nil
				}
				auth := r.Header.Get("Authorization")
				valid := scheme == "Bearer" && auth == "Bearer fixture-token"
				if scheme == "Basic" {
					u, p, ok := r.BasicAuth()
					valid = ok && u == "fixture-user" && p == "fixture-secret"
				}
				if !valid {
					resp := reefTagResponse(401, "")
					challenge := `Basic realm="registry"`
					if scheme == "Bearer" {
						challenge = `Bearer realm="https://registry.example/jwt/auth",service="registry",scope="repository:wrong:push,pull"`
					}
					resp.Header.Set("WWW-Authenticate", challenge)
					return resp, nil
				}
				return reefTagResponse(200, `{"name":"group/app","tags":["latest","v2"]}`), nil
			})}
			credential := func(_ context.Context, host string) (tagCredential, error) {
				credentialCalls++
				if host != "registry.example" {
					t.Fatal(host)
				}
				return tagCredential{"fixture-user", "fixture-secret"}, nil
			}
			result, e := genericRegistryTagPage(context.Background(), Config{}, "registry.example", "group/app", "", "", 1, client, credential)
			if e != nil || len(result.Tags) != 2 || credentialCalls != 1 {
				t.Fatal(result, e, credentialCalls)
			}
			if scheme == "Bearer" && tokenCalls != 2 {
				t.Fatal(tokenCalls)
			}
		})
	}
}
func TestRegistryV2TokenRealmAndStatusFailures(t *testing.T) {
	for _, tc := range []struct {
		status          int
		challenge, code string
	}{{401, `Bearer realm="https://evil.invalid/token"`, "registry_not_authorized"}, {401, `Bearer realm="http://registry.example/token"`, "registry_not_authorized"}, {401, `Bearer realm="https://registry.example/token?secret=x"`, "registry_not_authorized"}, {401, `Digest realm="private"`, "authentication_required"}, {403, "", "authentication_required"}, {429, "", "rate_limited"}, {404, "", "unsupported_registry"}, {503, "", "registry_unavailable"}, {302, "", "registry_unavailable"}} {
		calls := 0
		client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			response := reefTagResponse(tc.status, "private response must not be exposed")
			response.Header.Set("WWW-Authenticate", tc.challenge)
			response.Header.Set("Location", "https://evil.invalid")
			return response, nil
		})}
		_, e := genericRegistryTagPage(context.Background(), Config{}, "registry.example", "team/app", "", "", 1, client, func(context.Context, string) (tagCredential, error) {
			t.Fatal("Untrusted realm read credentials")
			return tagCredential{}, nil
		})
		if registryErrorCode(e) != tc.code || calls != 1 || strings.Contains(e.Error(), "private response") {
			t.Fatal(tc, e, calls)
		}
	}
}
func TestRegistryV2MetadataValidationAndNonLexicalOrder(t *testing.T) {
	for _, raw := range []string{`{"name":"team/app","tags":["z","a","latest"]}`, `{"name":"team/app","tags":null}`, `{"name":"wrong","tags":["latest"]}`, `{"name":"team/app","tags":["same","same"]}`, `{"name":"team/app"}`, `{"name":"team/app","tags":["../bad"]}`, strings.Repeat("x", (2<<20)+1)} {
		client := &http.Client{Transport: reefUpdateTransport(func(*http.Request) (*http.Response, error) { return reefTagResponse(200, raw), nil })}
		result, e := genericRegistryTagPage(context.Background(), Config{}, "registry.example", "team/app", "", "", 1, client, nil)
		valid := strings.Contains(raw, `["z","a","latest"]`) || strings.Contains(raw, `"tags":null`)
		if (e == nil) != valid {
			t.Fatal(raw[:min(len(raw), 80)], e)
		}
		if valid && len(result.Tags) > 0 && strings.Join(result.Tags, ",") != "z,a,latest" {
			t.Fatal(result)
		}
	}
}
func TestRegistryTagNetworkAndRealmPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.2.200", "169.254.169.254", "100.64.0.1", "0.1.2.3", "240.1.2.3", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "2001:db8::1"} {
		if publicRegistryAddress(netip.MustParseAddr(ip)) {
			t.Fatal(ip)
		}
	}
	if !publicRegistryAddress(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("Public address rejected")
	}
	for _, raw := range []string{"http://example.com/token", "https://user:password@example.com/token", "https://example.com/token?x=y", "https://example.com/token#secret"} {
		if _, e := registryRealm(raw); e == nil {
			t.Fatal(raw)
		}
	}
	cfg := Config{RegistryTagHosts: []string{"registry.example:5443"}, RegistryTagAuthRealms: map[string]string{"registry.example:5443": "https://auth.example/token"}}
	realm, _ := registryRealm("https://auth.example/token")
	if validateRegistryTagConfig(cfg) != nil || !trustedTagRealm(cfg, "registry.example:5443", realm) || trustedTagRealm(cfg, "another.example", realm) {
		t.Fatal("Cross-origin mapping not exact")
	}
	client, e := registryTagHTTPClient(Config{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.Get("https://127.0.0.1/v2/team/app/tags/list")
	if registryErrorCode(e) != "registry_not_authorized" {
		t.Fatal(e)
	}
}
func TestRegistryTagCredentialsUseOnlySyntheticMatchingPrivateFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONFIG", filepath.Join(home, "ignored"))
	dir := filepath.Join(home, ".docker")
	os.Mkdir(dir, 0700)
	file := filepath.Join(dir, "config.json")
	auth := base64.StdEncoding.EncodeToString([]byte("fixture-user:fixture-secret"))
	raw := `{"auths":{"https://registry.example":{"auth":"` + auth + `"}}}`
	os.WriteFile(file, []byte(raw), 0600)
	found, e := loadRegistryTagCredential(context.Background(), "registry.example")
	if e != nil || found.Username != "fixture-user" || found.Password != "fixture-secret" {
		t.Fatal("Synthetic credentials not resolved", e)
	}
	if _, e = loadRegistryTagCredential(context.Background(), "another.example"); registryErrorCode(e) != "authentication_required" {
		t.Fatal(e)
	}
	os.Chmod(file, 0644)
	if _, e = loadRegistryTagCredential(context.Background(), "registry.example"); e == nil {
		t.Fatal("Nonprivate credentials accepted")
	}
	os.Remove(file)
	os.Symlink(filepath.Join(home, "missing"), file)
	if _, e = loadRegistryTagCredential(context.Background(), "registry.example"); e == nil {
		t.Fatal("Symlink accepted")
	}
}
func TestRegistryCredentialHelperIsBoundedAndUsesGetOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", home)
	os.Mkdir(filepath.Join(home, ".docker"), 0700)
	file := filepath.Join(home, ".docker", "config.json")
	os.WriteFile(file, []byte(`{"credHelpers":{"registry.example":"fixture"}}`), 0600)
	script := filepath.Join(home, "docker-credential-fixture")
	os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = get ] || exit 1\nread -r host\n[ \"$host\" = registry.example ] || exit 1\nprintf '%s' '{\"Username\":\"fixture-user\",\"Secret\":\"fixture-secret\"}'\n"), 0700)
	found, e := loadRegistryTagCredential(context.Background(), "registry.example")
	if e != nil || found.Username != "fixture-user" || found.Password != "fixture-secret" {
		t.Fatal("Helper fixture failed", e)
	}
	var output tagCredentialOutput
	if _, e = output.Write(make([]byte, (16<<10)+1)); e == nil || output.Len() != 0 {
		t.Fatal("Unbounded helper output")
	}
}
func TestRegistryV2ExactCrossOriginRealmAndAnonymousBearerRetry(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			cfg := Config{}
			if allowed {
				cfg.RegistryTagAuthRealms = map[string]string{"registry.example": "https://auth.example/token"}
			}
			credentialCalls, tokenCalls := 0, 0
			client := &http.Client{Transport: reefUpdateTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "auth.example" {
					tokenCalls++
					user, password, ok := r.BasicAuth()
					if ok && (user != "fixture-user" || password != "fixture-secret") {
						t.Fatal("Wrong credentials")
					}
					token := "anonymous"
					if ok {
						token = "private"
					}
					return reefTagResponse(200, `{"token":"`+token+`"}`), nil
				}
				if r.URL.Host != "registry.example" {
					t.Fatal(r.URL.Host)
				}
				if r.Header.Get("Authorization") != "Bearer private" {
					response := reefTagResponse(401, "")
					response.Header.Set("WWW-Authenticate", `Bearer realm="https://auth.example/token",service="registry"`)
					return response, nil
				}
				return reefTagResponse(200, `{"name":"team/app","tags":["latest"]}`), nil
			})}
			credential := func(context.Context, string) (tagCredential, error) {
				credentialCalls++
				return tagCredential{"fixture-user", "fixture-secret"}, nil
			}
			result, e := genericRegistryTagPage(context.Background(), cfg, "registry.example", "team/app", "", "", 1, client, credential)
			if allowed {
				if e != nil || len(result.Tags) != 1 || credentialCalls != 1 || tokenCalls != 2 {
					t.Fatal(result, e, credentialCalls, tokenCalls)
				}
			} else if registryErrorCode(e) != "registry_not_authorized" || credentialCalls != 0 || tokenCalls != 0 {
				t.Fatal(e, credentialCalls, tokenCalls)
			}
		})
	}
}

func TestRegistryPrivateOptInDoesNotDisableHTTPSVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Untrusted TLS server reached application handler")
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	client, e := registryTagHTTPClient(Config{RegistryTagHosts: []string{u.Host}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Get(server.URL + "/v2/team/app/tags/list"); e == nil {
		t.Fatal("Untrusted certificate accepted")
	}
}
