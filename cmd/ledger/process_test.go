package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Real executables make updater and Codex-process tests portable to Windows and Wine.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		exe, _ := os.Executable()
		body, _ := os.ReadFile(exe)
		_, v, ok := strings.Cut(string(body[len(body)-128:]), "\nledger-fixture-version:")
		if !ok {
			os.Exit(2)
		}
		fmt.Println(strings.TrimSpace(v))
		os.Exit(0)
	}
	exe, _ := os.Executable()
	if filepath.Base(exe) == "codex"+executableSuffix || filepath.Base(exe) == "codex-real"+executableSuffix {
		failure := os.Getenv("LEDGER_TEST_CODEX_FAILURE")
		if failure != "" && !(failure == "keyring" && len(os.Args) > 2 && os.Args[1] == "-c" && os.Args[2] == `mcp_oauth_credentials_store='file'`) {
			fmt.Fprintln(os.Stderr, "failed to delete OAuth tokens: "+failure)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "hold" {
		if err := os.WriteFile(os.Args[2], []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "auth" && os.Args[2] == "headers" {
		if err := run(context.Background(), os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixtureBinary(t *testing.T, version string) []byte {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return append(body, []byte("\nledger-fixture-version:"+version+"\n")...)
}

func fakeCodex(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"+executableSuffix), fixtureBinary(t, "v1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func isolateConfig(t *testing.T) {
	t.Helper()
	variable := "XDG_CONFIG_HOME"
	if runtime.GOOS == "windows" {
		variable = "APPDATA"
	}
	t.Setenv(variable, t.TempDir())
	t.Setenv("LEDGER_AUTO_UPDATE", "0")
}

func TestConfigureCodexWithNativePaths(t *testing.T) {
	isolateConfig(t)
	home := filepath.Join(t.TempDir(), "Atlas user's configuration")
	t.Setenv("CODEX_HOME", home)
	fakeCodex(t)
	path, err := credentialPath("", "atlas")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := configureCodex("https://ledger.example.com", "atlas", filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || !strings.Contains(string(body), "http_headers_helper") {
		t.Fatalf("configuration missing: %v", err)
	}
	// The token remains on disk only in the protected credential file, never in TOML.
	c := Credentials{Server: "https://ledger.example.com", ClientID: "fixture-client", Token: Token{AccessToken: "fixture-access", RefreshToken: "fixture-refresh"}}
	if err = saveCredentials(path, c); err != nil {
		t.Fatal(err)
	}
	data, err := loadCredentials(path)
	if err != nil || data.AccessToken != c.AccessToken {
		t.Fatal("credentials did not round trip")
	}
	if strings.Contains(string(body), c.AccessToken) {
		t.Fatal("credential leaked to TOML")
	}
}

func TestWindowsReleaseAsset(t *testing.T) {
	var r release
	if err := json.Unmarshal([]byte(`{"tag_name":"v1.2.3","assets":[{"name":"ledger_windows_amd64.exe","browser_download_url":"https://github.com/CesarPetrescu/ledger/releases/download/v1.2.3/ledger_windows_amd64.exe","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","size":100}]}`), &r); err != nil {
		t.Fatal(err)
	}
	url, _, _, err := selectAsset(r, "v1.0.0", "windows", "amd64")
	if err != nil || !strings.HasSuffix(url, ".exe") {
		t.Fatalf("Windows asset missing: %v", err)
	}
	if _, _, _, err = selectAsset(r, "v1.0.0", "windows", "arm64"); err == nil {
		t.Fatal("wrong architecture accepted")
	}
}

func TestUpdateRollback(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "ledger"+executableSuffix)
	original := fixtureBinary(t, "v1.0.0")
	if err := os.WriteFile(exe, original, 0755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(exe+".missing", exe); err == nil {
		t.Fatal("missing candidate accepted")
	}
	body, err := os.ReadFile(exe)
	if err != nil || string(body) != string(original) {
		t.Fatal("failed update lost original executable")
	}
}
