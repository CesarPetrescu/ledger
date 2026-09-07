// Ledger's headless client supports Linux and Windows; the server binaries remain portable.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

const deviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ledger:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ledger connect codex --server URL | auth headers|status|logout [--profile NAME] | update | version")
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if args[0] == "update" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "--automatic") {
			return errors.New("usage: ledger update")
		}
		return update(ctx, len(args) == 2 && args[1] == "--automatic")
	}
	if len(args) < 2 {
		return errors.New("missing subcommand")
	}
	flags := flag.NewFlagSet("ledger "+strings.Join(args[:2], " "), flag.ContinueOnError)
	profile := flags.String("profile", "codex", "local credential profile")
	server := flags.String("server", "", "Ledger HTTPS origin")
	label := flags.String("name", "Codex machine", "machine label shown during approval")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	path, err := credentialPath(*profile)
	if err != nil {
		return err
	}
	switch strings.Join(args[:2], " ") {
	case "connect codex":
		if err = connect(ctx, path, *server, *label); err != nil {
			return err
		}
		if err = configureCodex(*server, *profile); err != nil {
			return fmt.Errorf("credentials saved; Codex setup failed: %w", err)
		}
		fmt.Fprintln(os.Stderr, "Connected to Ledger. Configured Codex MCP server: ledger.")
	case "auth headers":
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		var headers map[string]string
		headers, err = authHeaders(ctx, path)
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(headers)
		}
	case "auth status":
		c, e := loadCredentials(path)
		if e != nil {
			return e
		}
		fmt.Printf("Server: %s\nScope: %s\nAccess expires: %s\nVersion: %s\n", c.Server, c.Scope, c.ExpiresAt.Format(time.RFC3339), version)
	case "auth logout":
		err = withLock(ctx, path+".lock", func() error {
			c, err := loadCredentials(path)
			if err != nil {
				return err
			}
			if err = post(ctx, c.Server+"/oauth/revoke", url.Values{"client_id": {c.ClientID}, "token": {c.RefreshToken}}, nil); err != nil {
				return fmt.Errorf("revocation failed; credentials retained for retry: %w", err)
			}
			return os.Remove(path)
		})
	default:
		return errors.New("unknown command")
	}
	if err == nil {
		startAutoUpdate()
	}
	return err
}

type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

type Credentials struct {
	Server   string `json:"server"`
	ClientID string `json:"client_id"`
	Token
	ExpiresAt time.Time `json:"expires_at"`
}

type oauthFailure struct {
	Code string `json:"error"`
}

func (e *oauthFailure) Error() string { return "OAuth error: " + e.Code }

var authHTTP = &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func serverURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("--server must be an HTTPS origin, such as https://ledger.example.com")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func post(ctx context.Context, destination string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", destination, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := authHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Ledger: %w", err)
	}
	defer res.Body.Close()
	return decodeResponse(res, out)
}

func token(ctx context.Context, server string, form url.Values) (Token, error) {
	var t Token
	err := post(ctx, server+"/oauth/token", form, &t)
	if err == nil && (t.AccessToken == "" || t.RefreshToken == "" || !strings.EqualFold(t.TokenType, "Bearer") || t.ExpiresIn <= 0 || t.ExpiresIn > 86400) {
		err = errors.New("invalid token response")
	}
	return t, err
}

func connect(ctx context.Context, path, rawServer, label string) error {
	server, err := serverURL(rawServer)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
	defer cancel()
	return withLock(ctx, path+".lock", func() error {
		if _, err := os.Stat(path); err == nil {
			existing, e := loadCredentials(path)
			if e != nil {
				return e
			}
			if existing.Server != server {
				return errors.New("profile belongs to another server; choose --profile NAME")
			}
			pair, e := token(ctx, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {existing.ClientID}, "refresh_token": {existing.RefreshToken}})
			if e == nil {
				existing.Token = pair
				existing.ExpiresAt = time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
				return saveCredentials(path, existing)
			}
			var failure *oauthFailure
			if !errors.As(e, &failure) || failure.Code != "invalid_grant" {
				return e
			}
			if e = os.Remove(path); e != nil {
				return e
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		body, _ := json.Marshal(map[string]any{"client_name": label, "grant_types": []string{deviceGrant, "refresh_token"}})
		req, err := http.NewRequestWithContext(ctx, "POST", server+"/oauth/register", strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := authHTTP.Do(req)
		if err != nil {
			return errors.New("could not register machine")
		}
		var client struct {
			ID string `json:"client_id"`
		}
		err = decodeResponse(res, &client)
		res.Body.Close()
		if err != nil {
			return err
		}
		if client.ID == "" {
			return errors.New("registration returned no client ID")
		}
		var d struct {
			Code     string `json:"device_code"`
			UserCode string `json:"user_code"`
			URI      string `json:"verification_uri"`
			Expires  int    `json:"expires_in"`
			Interval int    `json:"interval"`
		}
		err = post(ctx, server+"/oauth/device", url.Values{"client_id": {client.ID}, "scope": {"ledger:read ledger:write"}, "resource": {server + "/mcp"}}, &d)
		if err != nil {
			return err
		}
		if d.Code == "" || d.URI != server+"/admin/connect" || len(d.UserCode) != 9 || strings.Trim(d.UserCode, "0123456789ABCDEFGHJKMNPQRSTVWXYZ-") != "" || d.Expires <= 0 || d.Expires > 600 || d.Interval < 0 || d.Interval > 60 {
			return errors.New("invalid device authorization response")
		}
		if d.Interval == 0 {
			d.Interval = 5
		}
		fmt.Fprintf(os.Stderr, "Open on your phone or laptop:\n%s\n\nEnter code: %s\nRequested access: read and write project memory\nWaiting for approval…\n", d.URI, d.UserCode)
		deadline, cancel := context.WithTimeout(ctx, time.Duration(d.Expires)*time.Second)
		defer cancel()
		for {
			if err = waitFor(deadline, time.Duration(d.Interval)*time.Second); err != nil {
				return errors.New("device approval expired or cancelled")
			}
			pair, err := token(deadline, server, url.Values{"grant_type": {deviceGrant}, "client_id": {client.ID}, "device_code": {d.Code}})
			if err == nil {
				return saveCredentials(path, Credentials{Server: server, ClientID: client.ID, Token: pair, ExpiresAt: time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)})
			}
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				d.Interval *= 2
				continue
			}
			var failure *oauthFailure
			if !errors.As(err, &failure) {
				return err
			}
			switch failure.Code {
			case "authorization_pending":
			case "slow_down":
				d.Interval += 5
			default:
				return err
			}
		}
	})
}

func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func credentialPath(profile string) (string, error) {
	if profile == "" || len(profile) > 64 || strings.Trim(profile, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return "", errors.New("profile must contain only letters, digits, hyphens or underscores")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "ledger")
	if err = privateDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, profile+".json"), nil
}

func authHeaders(ctx context.Context, path string) (map[string]string, error) {
	var headers map[string]string
	err := withLock(ctx, path+".lock", func() error {
		c, err := loadCredentials(path)
		if err != nil {
			return err
		}
		if time.Now().Add(time.Minute).After(c.ExpiresAt) {
			pair, err := token(ctx, c.Server, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {c.RefreshToken}})
			if err != nil {
				return fmt.Errorf("refresh failed; run ledger connect codex if authorization expired or was revoked: %w", err)
			}
			c.Token = pair
			c.ExpiresAt = time.Now().Add(time.Duration(pair.ExpiresIn) * time.Second)
			if err = saveCredentials(path, c); err != nil {
				return err
			}
		}
		headers = map[string]string{"Authorization": "Bearer " + c.AccessToken}
		return nil
	})
	return headers, err
}
