package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeScopeConfig lays down a minimal but valid config dir: server.toml plus
// one scope (profiles/<scope>/auth.toml) with the given bearer token.
func writeScopeConfig(t *testing.T, scope, token string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.toml"),
		[]byte("[server]\nport = 9997\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scopeDir := filepath.Join(dir, "profiles", scope)
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scopeDir, "auth.toml"),
		[]byte("bearer_token = \""+token+"\"\ndescription = \"test scope\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func startTestDaemon(t *testing.T, configDir string) *Daemon {
	t.Helper()
	d, err := Start(StartOpts{ConfigDir: configDir, DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Shutdown(nil) }) //nolint:errcheck // best-effort in test cleanup
	return d
}

func TestRegistryLoad(t *testing.T) {
	dir := writeScopeConfig(t, "personal", "sk-abc123")
	reg := NewRegistry()
	n, err := reg.Load(dir, ScopeDefaults{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 scope, got %d", n)
	}
	b, ok := reg.LookupByToken("sk-abc123")
	if !ok {
		t.Fatal("token not found")
	}
	if b.Key.Profile != "personal" || b.Key.Skillset != "default" {
		t.Fatalf("unexpected scope key: %s", b.Key)
	}
	if _, ok := reg.LookupByToken("nope"); ok {
		t.Fatal("unknown token should not resolve")
	}
}

func TestRegistryDuplicateTokenRejected(t *testing.T) {
	dir := writeScopeConfig(t, "personal", "sk-dup")
	// Second scope reusing the same token must fail the load.
	other := filepath.Join(dir, "profiles", "work")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "auth.toml"),
		[]byte("bearer_token = \"sk-dup\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	if _, err := reg.Load(dir, ScopeDefaults{}); err == nil {
		t.Fatal("expected duplicate-token error")
	}
}

func TestHealthOK(t *testing.T) {
	d := startTestDaemon(t, writeScopeConfig(t, "personal", "sk-h"))
	srv := httptest.NewServer(d.Router())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/skills/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("health body = %v", body)
	}
}

func TestAuthRejectsMissingAndUnknownToken(t *testing.T) {
	d := startTestDaemon(t, writeScopeConfig(t, "personal", "sk-secret"))
	srv := httptest.NewServer(d.Router())
	defer srv.Close()

	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"malformed", "Token sk-secret"},
		{"unknown token", "Bearer sk-wrong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/whoami", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", resp.StatusCode)
			}
			var env ErrorEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				t.Fatal(err)
			}
			if env.Error.Code != ErrCodeInvalidToken {
				t.Fatalf("want %s, got %s", ErrCodeInvalidToken, env.Error.Code)
			}
		})
	}
}

func TestAuthAcceptsValidToken(t *testing.T) {
	d := startTestDaemon(t, writeScopeConfig(t, "personal", "sk-good"))
	srv := httptest.NewServer(d.Router())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/whoami", nil)
	req.Header.Set("Authorization", "Bearer sk-good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["profile"] != "personal" || body["skillset"] != "default" {
		t.Fatalf("whoami body = %v", body)
	}
}
