package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func atomicWrite(path string, body []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("refusing to replace a non-regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := privateTemp(filepath.Dir(path), ".ledger-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(body); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceFile(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func saveCredentials(path string, c Credentials) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return atomicWrite(path, body, 0600)
}

func loadCredentials(path string) (Credentials, error) {
	var c Credentials
	f, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return c, errors.New("no readable credentials; run ledger connect codex --server URL")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return c, errors.New("credentials must be a regular file with mode 0600")
	}
	if err = checkPrivate(f); err != nil {
		return c, err
	}
	if err = json.NewDecoder(f).Decode(&c); err != nil {
		return c, errors.New("invalid credentials file")
	}
	if _, err = serverURL(c.Server); err != nil {
		return c, err
	}
	if c.ClientID == "" || c.AccessToken == "" || c.RefreshToken == "" {
		return c, errors.New("incomplete credentials; reconnect")
	}
	return c, nil
}

func decodeResponse(res *http.Response, out any) error {
	body, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(body) > 65536 {
		return errors.New("invalid server response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var failure oauthFailure
		if json.Unmarshal(body, &failure) == nil {
			switch failure.Code {
			case "authorization_pending", "slow_down", "access_denied", "expired_token", "invalid_grant", "invalid_client", "invalid_scope", "temporarily_unavailable":
				return &failure
			}
		}
		return fmt.Errorf("server returned HTTP %d", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err = json.Unmarshal(body, out); err != nil {
		return errors.New("invalid server JSON")
	}
	return nil
}

func configureCodex(rawServer, profile string) error {
	server, err := serverURL(rawServer)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(userHome, ".codex")
	}
	if err = os.MkdirAll(home, 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return withLock(ctx, filepath.Join(home, "ledger-config.lock"), func() error {
		path := filepath.Join(home, "config.toml")
		original, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		helper := headerHelper(exe, profile)
		body, _, err := codexConfig(original, server, helper)
		if err != nil {
			return err
		}
		if _, err = exec.LookPath("codex"); err != nil {
			return errors.New("install Codex CLI, then rerun ledger connect codex with the same profile")
		}

		if len(original) > 0 {
			if err = atomicWrite(path+".ledger-backup", original, 0600); err != nil {
				return err
			}
		}
		// Refuse to overwrite an edit made by another program while setup was running.
		current, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if string(current) != string(original) {
			return errors.New("Codex configuration changed during setup; retry")
		}
		if err = atomicWrite(path, body, 0600); err != nil {
			return err
		}
		// Codex owns OAuth storage, including credentials left behind by removed entries.
		var config struct {
			Store string `toml:"mcp_oauth_credentials_store"`
		}
		if err = toml.Unmarshal(body, &config); err != nil {
			return err
		}
		return clearCodexOAuth(ctx, config.Store)
	})
}

func codexConfig(original []byte, server, helper string) ([]byte, bool, error) {
	config := map[string]any{}
	if err := toml.Unmarshal(original, &config); err != nil {
		return nil, false, errors.New("could not parse Codex TOML; original file preserved")
	}
	servers, ok := config["mcp_servers"].(map[string]any)
	if !ok {
		if _, exists := config["mcp_servers"]; exists {
			return nil, false, errors.New("invalid mcp_servers configuration")
		}
		servers = map[string]any{}
		config["mcp_servers"] = servers
	}
	entry, exists := servers["ledger"].(map[string]any)
	if !exists {
		if _, present := servers["ledger"]; present {
			return nil, false, errors.New("invalid ledger server configuration")
		}
		entry = map[string]any{}
		servers["ledger"] = entry
	}
	if old, ok := entry["url"].(string); ok && old != server+"/mcp" {
		return nil, false, errors.New("Codex ledger entry points to another server; rename that entry first")
	}
	if _, present := entry["command"]; present {
		return nil, false, errors.New("Codex ledger entry uses stdio; rename that entry first")
	}
	for _, key := range []string{"bearer_token_env_var", "bearer_token", "oauth", "auth"} {
		delete(entry, key)
	}
	for _, key := range []string{"http_headers", "env_http_headers"} {
		if headers, ok := entry[key].(map[string]any); ok {
			for name := range headers {
				if strings.EqualFold(name, "Authorization") {
					delete(headers, name)
				}
			}
		}
	}
	entry["url"] = server + "/mcp"
	entry["http_headers_helper"] = helper
	entry["enabled"] = true
	body, err := toml.Marshal(config)
	return body, exists, err
}

func clearCodexOAuth(ctx context.Context, store string) error {
	cmd := codexCommand(ctx, "mcp", "logout", "ledger")
	_, err := cmd.Output()
	if err == nil {
		return nil
	}
	var failure *exec.ExitError
	if store != "keyring" && errors.As(err, &failure) && strings.Contains(string(failure.Stderr), "keyring") {
		// In auto mode Codex falls back to file storage when a keyring is unavailable.
		if codexCommand(ctx, "-c", `mcp_oauth_credentials_store='file'`, "mcp", "logout", "ledger").Run() == nil {
			fmt.Fprintln(os.Stderr, "Codex keyring unavailable; cleared file-backed Ledger OAuth credentials.")
			return nil
		}
	}
	return errors.New("configuration saved, but OAuth cleanup failed; run codex mcp logout ledger before using the helper")
}
