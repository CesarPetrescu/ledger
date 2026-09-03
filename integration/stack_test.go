//go:build stack

package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stack struct {
	base, publicURL, password string
	http                      *http.Client
}

type registration struct {
	ClientID     string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
}

type tokenPair struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Scope   string `json:"scope"`
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestStackConfigUsesExplicitPublicURL(t *testing.T) {
	t.Setenv("LEDGER_STACK_URL", "http://127.0.0.1:18081/")
	t.Setenv("LEDGER_STACK_PASSWORD", "test-only")
	t.Setenv("LEDGER_STACK_PUBLIC_URL", "https://deployment.example/")

	got := stackConfig(t)
	if got.base != "http://127.0.0.1:18081" || got.publicURL != "https://deployment.example" {
		t.Fatalf("stack config = base %q, public URL %q", got.base, got.publicURL)
	}
}

func stackConfig(t *testing.T) *stack {
	t.Helper()
	base := strings.TrimRight(os.Getenv("LEDGER_STACK_URL"), "/")
	publicURL := strings.TrimRight(os.Getenv("LEDGER_STACK_PUBLIC_URL"), "/")
	password := os.Getenv("LEDGER_STACK_PASSWORD")
	if base == "" || publicURL == "" || password == "" {
		t.Fatal("LEDGER_STACK_URL, LEDGER_STACK_PUBLIC_URL, and LEDGER_STACK_PASSWORD are required")
	}
	return &stack{base: base, publicURL: publicURL, password: password, http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (s *stack) do(t *testing.T, method, path, contentType, body, xff string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	res, err := s.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (s *stack) register(t *testing.T, xff string) registration {
	t.Helper()
	res := s.do(t, http.MethodPost, "/oauth/register", "application/json", `{"redirect_uris":["http://127.0.0.1:4567/callback"],"client_name":"Stack acceptance"}`, xff)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("register = %d: %s", res.StatusCode, body)
	}
	var client registration
	if err := json.NewDecoder(res.Body).Decode(&client); err != nil || client.ClientID == "" {
		t.Fatalf("registration = %#v, %v", client, err)
	}
	return client
}

func (s *stack) authorizationValues(client registration, scope, password string) url.Values {
	return url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {"http://127.0.0.1:5678/callback"},
		"response_type":         {"code"},
		"code_challenge":        {oauth.PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")},
		"code_challenge_method": {"S256"},
		"scope":                 {scope},
		"resource":              {s.publicURL + "/mcp"},
		"state":                 {"stack-state"},
		"password":              {password},
		"action":                {"approve"},
	}
}

func (s *stack) authorize(t *testing.T, client registration, scope, xff string) string {
	t.Helper()
	form := s.authorizationValues(client, scope, s.password)
	res := s.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode(), xff)
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("authorize = %d: %s", res.StatusCode, body)
	}
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil || location.Query().Get("code") == "" || location.Query().Get("state") != "stack-state" || location.Query().Get("iss") != s.publicURL {
		t.Fatalf("authorization redirect = %q, %v", res.Header.Get("Location"), err)
	}
	return location.Query().Get("code")
}

func (s *stack) token(t *testing.T, form url.Values, status int) tokenPair {
	t.Helper()
	res := s.do(t, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", form.Encode(), "198.51.100.60")
	defer res.Body.Close()
	if res.StatusCode != status || res.Header.Get("Cache-Control") != "no-store" {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("token = %d cache=%q: %s", res.StatusCode, res.Header.Get("Cache-Control"), body)
	}
	var pair tokenPair
	_ = json.NewDecoder(res.Body).Decode(&pair)
	return pair
}

func connectMCP(t *testing.T, base, token, name string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: name, Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token}}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestRealStackAcceptance(t *testing.T) {
	s := stackConfig(t)

	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		res := s.do(t, http.MethodGet, path, "", "", "198.51.100.10")
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		res.Body.Close()
		want := map[string]any{
			"resource": s.publicURL + "/mcp", "authorization_servers": []any{s.publicURL},
			"scopes_supported": []any{"ledger:read", "ledger:write"}, "bearer_methods_supported": []any{"header"},
		}
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "max-age=3600" || !reflect.DeepEqual(got, want) {
			t.Fatalf("metadata %s = %d %#v", path, res.StatusCode, got)
		}
	}
	res := s.do(t, http.MethodPost, "/mcp", "application/json", `{}`, "198.51.100.10")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != `Bearer resource_metadata="`+s.publicURL+`/.well-known/oauth-protected-resource"` {
		t.Fatalf("unauthenticated MCP = %d, %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	for _, path := range []string{"/search", "/reindex"} {
		res = s.do(t, http.MethodPost, path, "application/json", `{}`, "198.51.100.10")
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("internal route %s exposed: %d", path, res.StatusCode)
		}
	}

	readClient := s.register(t, "198.51.100.20")
	valid := s.authorizationValues(readClient, "ledger:read", "")
	invalid := []url.Values{
		func() url.Values {
			v := cloneValues(valid)
			v.Set("code_challenge_method", "plain")
			return v
		}(),
		func() url.Values {
			v := cloneValues(valid)
			v.Set("redirect_uri", "https://evil.example/callback")
			return v
		}(),
		func() url.Values { v := cloneValues(valid); v.Set("resource", "https://evil.example/mcp"); return v }(),
	}
	for _, form := range invalid {
		res = s.do(t, http.MethodGet, "/oauth/authorize?"+form.Encode(), "", "", "198.51.100.21")
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest || res.Header.Get("Location") != "" {
			t.Fatalf("unsafe authorization redirected: %d %q", res.StatusCode, res.Header.Get("Location"))
		}
	}

	wrong := s.authorizationValues(readClient, "ledger:read", "wrong")
	for i := 0; i < 4; i++ {
		res = s.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", wrong.Encode(), "198.51.100.30")
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("password failure %d = %d", i+1, res.StatusCode)
		}
	}
	res = s.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", wrong.Encode(), "198.51.100.31")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second XFF inherited first bucket: %d", res.StatusCode)
	}
	res = s.do(t, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", wrong.Encode(), "198.51.100.30")
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") != "900" {
		t.Fatalf("fifth password failure = %d retry=%q", res.StatusCode, res.Header.Get("Retry-After"))
	}

	code := s.authorize(t, readClient, "ledger:read", "198.51.100.30")
	readPair := s.token(t, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {readClient.ClientID}, "code": {code},
		"redirect_uri": {"http://127.0.0.1:5678/callback"}, "code_verifier": {"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
	}, http.StatusOK)
	if readPair.Scope != "ledger:read" {
		t.Fatalf("read token scope = %q", readPair.Scope)
	}
	ctx := context.Background()
	readSession := connectMCP(t, s.base, readPair.Access, "stack-read")
	tools, err := readSession.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 5 {
		t.Fatalf("tools = %#v, %v", tools, err)
	}
	denied, err := readSession.CallTool(ctx, &mcp.CallToolParams{Name: "append_entry", Arguments: map[string]any{"slug": "acceptance", "kind": "note", "body": "blocked"}})
	if err != nil || !denied.IsError || denied.Content[0].(*mcp.TextContent).Text != `{"error":"insufficient_scope"}` {
		t.Fatalf("read-only write = %#v, %v", denied, err)
	}

	rotated := s.token(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {readClient.ClientID}, "refresh_token": {readPair.Refresh}}, http.StatusOK)
	s.token(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {readClient.ClientID}, "refresh_token": {readPair.Refresh}}, http.StatusBadRequest)
	req, _ := http.NewRequest(http.MethodGet, s.base+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+rotated.Access)
	res, err = s.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh reuse did not revoke rotated family: %d", res.StatusCode)
	}

	writeClient := s.register(t, "198.51.100.40")
	writeCode := s.authorize(t, writeClient, "ledger:read ledger:write", "198.51.100.40")
	writePair := s.token(t, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {writeClient.ClientID}, "code": {writeCode},
		"redirect_uri": {"http://127.0.0.1:5678/callback"}, "code_verifier": {"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
	}, http.StatusOK)
	writeSession := connectMCP(t, s.base, writePair.Access, "stack-write")
	if result, err := writeSession.CallTool(ctx, &mcp.CallToolParams{Name: "upsert_project", Arguments: map[string]any{
		"slug": "acceptance", "name": "Acceptance", "tier": "focus", "hours_wk": 1, "goal": "Verify retrieval", "deadline": "today",
	}}); err != nil || result.IsError {
		t.Fatalf("upsert_project = %#v, %v", result, err)
	}
	if result, err := writeSession.CallTool(ctx, &mcp.CallToolParams{Name: "append_entry", Arguments: map[string]any{
		"slug": "acceptance", "kind": "decision", "body": "Decizie bilingvă: renovare bucătărie / kitchen renovation păstrează mobilierul.",
	}}); err != nil || result.IsError {
		t.Fatalf("append_entry = %#v, %v", result, err)
	}
	for _, query := range []string{"renovare bucatarie", "kitchen renovation"} {
		found := false
		for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
			result, err := writeSession.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"q": query, "limit": 3}})
			if err != nil || result.IsError {
				t.Fatalf("search %q = %#v, %v", query, result, err)
			}
			body, _ := json.Marshal(result.StructuredContent)
			var decoded struct {
				Hits []struct {
					Ref string `json:"ref"`
				} `json:"hits"`
			}
			_ = json.Unmarshal(body, &decoded)
			for _, hit := range decoded.Hits {
				found = found || strings.HasPrefix(hit.Ref, "entry:")
			}
			if found {
				break
			}
		}
		if !found {
			t.Fatalf("search %q never returned the indexed entry", query)
		}
	}

	for i := 0; i < 6; i++ {
		res = s.do(t, http.MethodPost, "/oauth/register", "application/json", `{"redirect_uris":["http://localhost/callback"],"client_name":"Rate"}`, "198.51.100.50")
		res.Body.Close()
		want := http.StatusCreated
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if res.StatusCode != want {
			t.Fatalf("registration %d = %d, want %d", i+1, res.StatusCode, want)
		}
	}
}

func cloneValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, items := range values {
		clone[key] = append([]string(nil), items...)
	}
	return clone
}
