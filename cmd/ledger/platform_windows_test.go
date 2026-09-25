package main

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func makeCredentialsPublic(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsReplacesRunningExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ledger.exe")
	candidate := filepath.Join(dir, "next.exe")
	if err := os.WriteFile(exe, fixtureBinary(t, "v1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, fixtureBinary(t, "v1.2.3"), 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := filepath.Join(dir, "ready")
	process := exec.CommandContext(ctx, exe, "hold", ready)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = process.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := replaceExecutable(candidate, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "v1.2.3" {
		t.Fatalf("new executable failed: %v", err)
	}
	if _, err := os.Stat(exe + ".old"); err != nil {
		t.Fatal("rollback image missing")
	}
}

// helperCommand runs a recorded helper through cmd.exe, as Codex does on Windows.
func helperCommand(t *testing.T, helper string) *exec.Cmd {
	t.Helper()
	if windows.NewLazySystemDLL("ntdll.dll").NewProc("wine_get_version").Find() == nil {
		t.Skip("Wine provides a PowerShell stub; native Windows CI exercises PowerShell and npm")
	}
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("Wine lacks PowerShell; native Windows CI runs this test")
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /Q /D /C "` + helper + `"`}
	return cmd
}

func TestWindowsPackagedLoginResolvesRedirectedAppData(t *testing.T) {
	if family, ok := packageFamilyName(); ok {
		t.Fatalf("test process unexpectedly carries package identity %q", family)
	}
	isolateConfig(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	fakeCodex(t)
	fakeApproval(t)
	// Inside a packaged app such as the Codex desktop app, Windows redirects %APPDATA%
	// writes into the package's LocalCache; the login must record that directory.
	localAppData := os.Getenv("LOCALAPPDATA")
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	original := packageFamilyName
	packageFamilyName = func() (string, bool) { return "Atlas.Codex_fixture", true }
	t.Cleanup(func() { packageFamilyName = original })
	ctx := context.Background()
	if err := run(ctx, []string{"connect", "codex", "--server", "https://ledger.example.com"}); err != nil {
		t.Fatal(err)
	}
	redirected := filepath.Join(local, "Packages", "Atlas.Codex_fixture", "LocalCache", "Roaming", "ledger")
	if _, err := loadCredentials(filepath.Join(redirected, "codex.json")); err != nil {
		t.Fatalf("credentials missing from the redirected directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("APPDATA"), "ledger", "codex.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("login wrote outside the redirected directory: %v", err)
	}
	helper := codexHelper(t)
	// Terminal Codex runs the helper without package identity and sees the real %APPDATA%.
	packageFamilyName = original
	os.Setenv("LOCALAPPDATA", localAppData)
	isolateConfig(t)
	path, err := credentialPath("", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loadCredentials(path); err == nil {
		t.Fatal("terminal environment found credentials without the recorded directory")
	}
	if path, err = credentialPath(redirected, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err = loadCredentials(path); err != nil {
		t.Fatal(err)
	}
	if headers := helperHeaders(t, helper); headers["Authorization"] != "Bearer fixture-access" {
		t.Fatalf("helper headers=%v", headers)
	}
}

func TestWindowsHeaderHelperAndNpmCodex(t *testing.T) {
	isolateConfig(t)
	t.Setenv("LEDGER_TEST_EXPAND", "must-not-expand")
	dir := filepath.Join(t.TempDir(), "Atlas's %LEDGER_TEST_EXPAND% & work")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "ledger.exe")
	if err := os.WriteFile(exe, fixtureBinary(t, "v1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	path, err := credentialPath("", "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCredentials(path, Credentials{Server: "https://ledger.example.com", ClientID: "fixture", Token: Token{AccessToken: "fixture-access", RefreshToken: "fixture-refresh"}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if headers := helperHeaders(t, headerHelper(exe, "atlas", filepath.Dir(path))); headers["Authorization"] != "Bearer fixture-access" {
		t.Fatalf("header helper returned invalid headers: %v", headers)
	}

	bin := t.TempDir()
	if err = os.WriteFile(filepath.Join(bin, "codex-real.exe"), fixtureBinary(t, "v1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "codex.cmd"), []byte("@echo off\r\n\"%~dp0codex-real.exe\" %*\r\nexit /b %errorlevel%\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LEDGER_TEST_CODEX_FAILURE", "keyring")
	if err = clearCodexOAuth(context.Background(), "auto"); err != nil {
		t.Fatal(err)
	}
}
