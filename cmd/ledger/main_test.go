package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestConcurrentHeadersRefreshOnceAndLogoutRetainsOnFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LEDGER_AUTO_UPDATE", "0")
	var calls atomic.Int32
	var fail atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/revoke" {
			if fail.Load() {
				w.WriteHeader(503)
			}
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != "refresh-old" {
			t.Error("rotating refresh token reused")
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(Token{AccessToken: "access-new", RefreshToken: "refresh-new", TokenType: "Bearer", ExpiresIn: 900, Scope: "ledger:read"})
	}))
	defer server.Close()
	old := authHTTP
	authHTTP = server.Client()
	t.Cleanup(func() { authHTTP = old })
	path, err := credentialPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	c := Credentials{Server: server.URL, ClientID: "machine", Token: Token{AccessToken: "access-old", RefreshToken: "refresh-old"}, ExpiresAt: time.Now().Add(-time.Minute)}
	if err = saveCredentials(path, c); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			headers, err := authHeaders(context.Background(), path)
			if err != nil || headers["Authorization"] != "Bearer access-new" {
				t.Errorf("headers=%v err=%v", headers, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("refreshes=%d", calls.Load())
	}
	loaded, err := loadCredentials(path)
	if err != nil || loaded.RefreshToken != "refresh-new" {
		t.Fatalf("rotation not saved: %v", err)
	}
	fail.Store(true)
	if err = run(context.Background(), []string{"auth", "logout"}); err == nil {
		t.Fatal("failed logout reported success")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("failed logout deleted credentials")
	}
	fail.Store(false)
	if err = run(context.Background(), []string{"auth", "logout"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("logout retained credentials")
	}
}

func TestCredentialProtectionAndLockDeadline(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, bad := range []string{"../x", "", "a/b", "x;command"} {
		if _, err := credentialPath(bad); err == nil {
			t.Errorf("accepted profile %q", bad)
		}
	}
	path, err := credentialPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	c := Credentials{Server: "https://ledger.example.com", ClientID: "machine", Token: Token{AccessToken: "access", RefreshToken: "refresh"}}
	if err = saveCredentials(path, c); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadCredentials(path); err == nil {
		t.Fatal("read public credentials")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "secret")
	if err = os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err = saveCredentials(path, c); err == nil {
		t.Fatal("overwrote symlink")
	}
	if _, err = loadCredentials(path); err == nil {
		t.Fatal("read symlink")
	}
	if err = withLock(context.Background(), path+".lock", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		if err := withLock(ctx, path+".lock", func() error { t.Error("lock was not exclusive"); return nil }); err == nil {
			t.Error("lock did not time out")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCodexConfigPreservesSettingsAndRemovesConflicts(t *testing.T) {
	original := []byte("model='example'\n[mcp_servers.other]\ncommand='tool'\n[mcp_servers.ledger]\nurl='https://ledger.example.com/mcp'\nbearer_token_env_var='OLD'\nenabled_tools=['read']\nhttp_headers={Authorization='secret', Region='eu'}\n[mcp_servers.ledger.oauth]\nclient_id='old'\n")
	body, exists, err := codexConfig(original, "https://ledger.example.com", "'/path with space/ledger' auth headers")
	if err != nil || !exists {
		t.Fatal(err)
	}
	var c map[string]any
	if err = toml.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	servers := c["mcp_servers"].(map[string]any)
	entry := servers["ledger"].(map[string]any)
	if c["model"] != "example" || servers["other"].(map[string]any)["command"] != "tool" || entry["enabled_tools"] == nil || entry["http_headers"].(map[string]any)["Region"] != "eu" {
		t.Fatal("unrelated settings lost")
	}
	if strings.Contains(string(body), "secret") || entry["oauth"] != nil || entry["bearer_token_env_var"] != nil {
		t.Fatal("conflicting credentials retained")
	}
	if _, _, err = codexConfig(original, "https://another.example.com", "helper"); err == nil {
		t.Fatal("overwrote another server")
	}
	if _, _, err = codexConfig([]byte("[bad"), "https://ledger.example.com", "helper"); err == nil {
		t.Fatal("accepted malformed TOML")
	}
}

func TestUpdaterChecksIntegrityVersionAndAtomicReplacement(t *testing.T) {
	binary := []byte("#!/bin/sh\nprintf 'v1.2.3\\n'\n")
	sum := sha256.Sum256(binary)
	var r release
	metadata := fmt.Sprintf(`{"tag_name":"v1.2.3","assets":[{"name":"ledger_%s_%s","browser_download_url":"%sv1.2.3/ledger_%s_%s","digest":"sha256:%x","size":%d}]}`, runtime.GOOS, runtime.GOARCH, releasePrefix, runtime.GOOS, runtime.GOARCH, sum, len(binary))
	if err := json.Unmarshal([]byte(metadata), &r); err != nil {
		t.Fatal(err)
	}
	for _, current := range []string{"v1.2.3", "v2.0.0"} {
		if u, _, _, err := selectAsset(r, current, runtime.GOOS, runtime.GOARCH); err != nil || u != "" {
			t.Fatalf("downgrade/current allowed: %s %v", u, err)
		}
	}
	bad := r
	bad.Prerelease = true
	if _, _, _, err := selectAsset(bad, "dev", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("accepted prerelease")
	}
	bad = r
	bad.Assets = append(bad.Assets[:0:0], r.Assets...)
	bad.Assets[0].URL = "https://evil.example/binary"
	if _, _, _, err := selectAsset(bad, "dev", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("accepted foreign asset")
	}
	exe := filepath.Join(t.TempDir(), "ledger")
	original := []byte("#!/bin/sh\nprintf 'v1.0.0\\n'\n")
	if err := os.WriteFile(exe, original, 0755); err != nil {
		t.Fatal(err)
	}
	var corrupt atomic.Bool
	corrupt.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if corrupt.Load() {
			_, _ = w.Write([]byte("corrupt"))
			return
		}
		_, _ = w.Write(binary)
	}))
	defer server.Close()
	// Route the fixed, validated GitHub URL to a local TLS fixture.
	client := server.Client()
	transport := client.Transport
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Scheme = "https"
		req.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return transport.RoundTrip(req)
	})
	if err := installRelease(context.Background(), client, exe, r); err == nil {
		t.Fatal("installed corrupt asset")
	}
	body, _ := os.ReadFile(exe)
	if string(body) != string(original) {
		t.Fatal("failed download replaced working executable")
	}
	corrupt.Store(false)
	if err := installRelease(context.Background(), client, exe, r); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(exe)
	if string(body) != string(binary) {
		t.Fatal("update not installed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDevicePollingBacksOffOnTimeout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var polls int
	var previous time.Time
	old := authHTTP
	authHTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/oauth/register":
			body = `{"client_id":"machine"}`
		case "/oauth/device":
			body = `{"device_code":"private-code","user_code":"ABCD-2345","verification_uri":"https://ledger.example.com/admin/connect","expires_in":20,"interval":1}`
		case "/oauth/token":
			polls++
			if polls == 1 {
				previous = time.Now()
				return nil, context.DeadlineExceeded
			}
			if time.Since(previous) < 2*time.Second {
				t.Error("timeout did not increase the polling interval")
			}
			body = `{"access_token":"access","refresh_token":"refresh","token_type":"Bearer","expires_in":900}`
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { authHTTP = old })
	path, err := credentialPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = connect(context.Background(), path, "https://ledger.example.com", "Atlas"); err != nil {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Fatalf("polls=%d", polls)
	}
	marker, err := updateMarker()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := credentialPath("update-check")
	if err != nil {
		t.Fatal(err)
	}
	if marker == profile {
		t.Fatal("update marker overwrites a credential profile")
	}
}

func TestCodexCleanupFallsBackOnlyWhenKeyringIsUnavailable(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "codex")
	script := "#!/bin/sh\nif [ \"$1\" = '-c' ]; then exit 0; fi\necho 'failed to delete OAuth tokens from keyring' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := clearCodexOAuth(context.Background(), "auto"); err != nil {
		t.Fatal(err)
	}
	if err := clearCodexOAuth(context.Background(), "keyring"); err == nil {
		t.Fatal("ignored explicit keyring policy")
	}
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'invalid configuration' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := clearCodexOAuth(context.Background(), "auto"); err == nil {
		t.Fatal("masked a non-keyring failure")
	}
}
