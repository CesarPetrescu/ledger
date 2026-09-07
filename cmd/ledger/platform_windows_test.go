package main

import (
	"context"
	"encoding/json"
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

func TestWindowsHeaderHelperAndNpmCodex(t *testing.T) {
	if windows.NewLazySystemDLL("ntdll.dll").NewProc("wine_get_version").Find() == nil {
		t.Skip("Wine provides a PowerShell stub; native Windows CI exercises PowerShell and npm")
	}
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("Wine lacks PowerShell; native Windows CI runs this test")
	}
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
	path, err := credentialPath("atlas")
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCredentials(path, Credentials{Server: "https://ledger.example.com", ClientID: "fixture", Token: Token{AccessToken: "fixture-access", RefreshToken: "fixture-refresh"}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /Q /D /C "` + headerHelper(exe, "atlas") + `"`}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("header helper failed: %v", err)
	}
	var headers map[string]string
	if err = json.Unmarshal(out, &headers); err != nil || headers["Authorization"] != "Bearer fixture-access" {
		t.Fatal("header helper returned invalid headers")
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
