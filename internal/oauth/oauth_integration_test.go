//go:build integration

package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestAuthorizationCodeAndRefreshRotationRevokeFamilyOnReuse(t *testing.T) {
	db, ctx := testdb.Open(t)
	client, err := db.PutClient(ctx, store.OAuthClient{ClientID: "test-client", Kind: "dcr", Name: "Test", RedirectURIs: []string{"http://127.0.0.1:8123/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	password, _ := HashPassword("secret")
	server := NewServer(Config{PublicURL: "https://ledger.example.com", PasswordHash: password}, db)
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	form := url.Values{
		"client_id": {client.ClientID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"code_challenge": {PKCEChallenge(verifier)}, "code_challenge_method": {"S256"}, "scope": {"ledger:read ledger:write"},
		"resource": {"https://ledger.example.com/mcp"}, "state": {"state-1"}, "password": {"secret"}, "action": {"approve"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("authorize = %d: %s", res.Code, res.Body.String())
	}
	location, _ := url.Parse(res.Header().Get("Location"))
	if location.Query().Get("state") != "state-1" || location.Query().Get("iss") != "https://ledger.example.com" {
		t.Fatalf("authorization redirect = %s", location)
	}
	pair1 := exchange(t, server, url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {location.Query().Get("code")}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {verifier}}, http.StatusOK)
	if pair1.Scope != "ledger:read ledger:write" {
		t.Fatalf("token scope = %q", pair1.Scope)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM oauth_code`); err != nil {
		t.Fatal(err)
	}
	exchange(t, server, url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {location.Query().Get("code")}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {verifier}}, http.StatusBadRequest)
	if _, _, err := db.LookupAccess(ctx, pair1.AccessToken); err == nil {
		t.Fatal("authorization code reuse did not revoke its deterministic family")
	}

	// Issue a fresh family to verify refresh rotation independently.
	res = httptest.NewRecorder()
	server.ServeHTTP(res, req)
	location, _ = url.Parse(res.Header().Get("Location"))
	pair1 = exchange(t, server, url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {location.Query().Get("code")}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {verifier}}, http.StatusOK)
	pair2 := exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair1.RefreshToken}}, http.StatusOK)
	exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair1.RefreshToken}}, http.StatusBadRequest)
	if _, _, err := db.LookupAccess(ctx, pair2.AccessToken); err == nil {
		t.Fatal("refresh token reuse did not revoke the token family")
	}
}

func TestConcurrentRefreshReplayRevokesNewFamilyCredentials(t *testing.T) {
	db, ctx := testdb.Open(t)
	const clientID = "concurrent-replay-client"
	const redirectURI = "http://127.0.0.1/callback"
	if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: clientID, Kind: "dcr", Name: "Concurrent replay", RedirectURIs: []string{redirectURI}}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateCode(ctx, "concurrent-code", clientID, redirectURI, "challenge", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	pair1, err := db.ExchangeCode(ctx, "concurrent-code", clientID, redirectURI, "verifier", func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	pair2, err := db.ExchangeRefresh(ctx, pair1.RefreshToken, clientID)
	if err != nil {
		t.Fatal(err)
	}

	blocker, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pair2Hash := sha256.Sum256([]byte(pair2.RefreshToken))
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM oauth_token WHERE hash=$1 FOR UPDATE`, pair2Hash[:]); err != nil {
		t.Fatal(err)
	}

	type result struct {
		pair store.TokenPair
		err  error
	}
	rotation := make(chan result, 1)
	go func() {
		pair, err := db.ExchangeRefresh(ctx, pair2.RefreshToken, clientID)
		rotation <- result{pair: pair, err: err}
	}()
	time.Sleep(100 * time.Millisecond)
	replay := make(chan error, 1)
	go func() {
		_, err := db.ExchangeRefresh(ctx, pair1.RefreshToken, clientID)
		replay <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	rotated := <-rotation
	if rotated.err != nil {
		t.Fatalf("concurrent rotation: %v", rotated.err)
	}
	if err := <-replay; err != store.ErrInvalidGrant {
		t.Fatalf("replay error = %v, want invalid_grant", err)
	}
	var live int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_token WHERE family=$1::uuid AND NOT revoked AND expires_at>now()`, pair1.Family).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("refresh replay left %d live credentials in family; newest access token still valid=%t", live, func() bool {
			_, _, err := db.LookupAccess(ctx, rotated.pair.AccessToken)
			return err == nil
		}())
	}
}

func TestGCPreservesAuthorizationCodeReplayTombstoneWhileFamilyIsLive(t *testing.T) {
	db, ctx := testdb.Open(t)
	client, err := db.PutClient(ctx, store.OAuthClient{ClientID: "gc-code-client", Kind: "dcr", Name: "GC code", RedirectURIs: []string{"http://127.0.0.1/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{PublicURL: "https://ledger.example.com"}, db)
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	code := "gc-code"
	if err := db.CreateCode(ctx, code, client.ClientID, client.RedirectURIs[0], PKCEChallenge(verifier), []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	codeGrant := url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {code}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {verifier}}
	pair1 := exchange(t, server, codeGrant, http.StatusOK)
	pair2 := exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair1.RefreshToken}}, http.StatusOK)
	if _, err := db.Pool.Exec(ctx, `UPDATE oauth_code SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GC(ctx); err != nil {
		t.Fatal(err)
	}
	var used bool
	if err := db.Pool.QueryRow(ctx, `SELECT used FROM oauth_code`).Scan(&used); err != nil || !used {
		t.Fatalf("used authorization-code tombstone removed while family is live: used=%v err=%v", used, err)
	}
	exchange(t, server, codeGrant, http.StatusBadRequest)
	if _, _, err := db.LookupAccess(ctx, pair2.AccessToken); err == nil {
		t.Fatal("authorization-code replay after GC did not revoke newest access token")
	}
	exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair2.RefreshToken}}, http.StatusBadRequest)
	if _, err := db.GC(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_code`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("authorization-code tombstone retained without live family credentials: count=%d err=%v", count, err)
	}
}

func TestGCPreservesRotatedRefreshReplayTombstoneWhileFamilyIsLive(t *testing.T) {
	db, ctx := testdb.Open(t)
	client, err := db.PutClient(ctx, store.OAuthClient{ClientID: "gc-refresh-client", Kind: "dcr", Name: "GC refresh", RedirectURIs: []string{"http://127.0.0.1/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(Config{PublicURL: "https://ledger.example.com"}, db)
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	if err := db.CreateCode(ctx, "gc-refresh-code", client.ClientID, client.RedirectURIs[0], PKCEChallenge(verifier), []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	pair1 := exchange(t, server, url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {"gc-refresh-code"}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {verifier}}, http.StatusOK)
	pair2 := exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair1.RefreshToken}}, http.StatusOK)
	if _, err := db.Pool.Exec(ctx, `UPDATE oauth_token SET expires_at=now()-interval '1 second' WHERE kind='refresh' AND revoked`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GC(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_token WHERE kind='refresh' AND revoked AND expires_at < now()`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rotated refresh tombstone removed while family is live: count=%d err=%v", count, err)
	}
	exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair1.RefreshToken}}, http.StatusBadRequest)
	if _, _, err := db.LookupAccess(ctx, pair2.AccessToken); err == nil {
		t.Fatal("rotated refresh replay after GC did not revoke newest access token")
	}
	exchange(t, server, url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ClientID}, "refresh_token": {pair2.RefreshToken}}, http.StatusBadRequest)
	if _, err := db.GC(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_token WHERE kind='refresh' AND expires_at < now()`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rotated refresh tombstone retained without live family credentials: count=%d err=%v", count, err)
	}
}

type tokenJSON struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func exchange(t *testing.T, server http.Handler, form url.Values, status int) tokenJSON {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != status {
		t.Fatalf("token = %d: %s", res.Code, res.Body.String())
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token Cache-Control = %q, want no-store", res.Header().Get("Cache-Control"))
	}
	var result tokenJSON
	_ = json.Unmarshal(res.Body.Bytes(), &result)
	return result
}

func TestDCRRFC7591Response(t *testing.T) {
	db, ctx := testdb.Open(t)
	password, _ := HashPassword("secret")
	server := NewServer(Config{PublicURL: "https://ledger.example.com", PasswordHash: password}, db)
	body := `{"redirect_uris":["http://127.0.0.1:8123/callback"],"client_name":"DCR Test"}`
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("register = %d: %s", res.Code, res.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"client_id":                  got["client_id"],
		"client_id_issued_at":        got["client_id_issued_at"],
		"client_name":                "DCR Test",
		"redirect_uris":              []any{"http://127.0.0.1:8123/callback"},
		"token_endpoint_auth_method": "none",
		"grant_types":                []any{"authorization_code", "refresh_token"},
		"response_types":             []any{"code"},
		"scope":                      "ledger:read ledger:write",
	}
	if got["client_id"] == "" || got["client_id_issued_at"] == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("registration response = %#v, want %#v", got, want)
	}
	if _, err := db.GetClient(ctx, got["client_id"].(string)); err != nil {
		t.Fatal(err)
	}
}

func TestCIMDFetchValidation(t *testing.T) {
	db, ctx := testdb.Open(t)
	var metadataURL string
	var redirectFollowed atomic.Bool
	metadata := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := metadataURL + r.URL.Path
		redirect := "https://app.example/callback"
		authMethod := "none"
		var authMethods []string
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, metadataURL+"/redirect-target", http.StatusFound)
			return
		case "/redirect-target":
			redirectFollowed.Store(true)
		case "/mismatch":
			clientID += "-wrong"
		case "/unsafe":
			redirect = "http://app.example/callback"
		case "/large":
			w.Write([]byte(strings.Repeat("x", (64<<10)+1)))
			return
		case "/ceiling":
			prefix := fmt.Sprintf(`{"client_id":%q,"client_name":"CIMD Test","redirect_uris":[%q],"padding":"`, clientID, redirect)
			suffix := `"}`
			_, _ = fmt.Fprint(w, prefix+strings.Repeat("x", (64<<10)-len(prefix)-len(suffix))+suffix)
			return
		case "/slow":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		case "/auth":
			authMethod = "client_secret_post"
		case "/auth-list":
			authMethod = "private_key_jwt"
			authMethods = []string{"private_key_jwt"}
		case "/empty-auth-list":
			authMethods = []string{}
		case "/inconsistent-auth":
			authMethod = "private_key_jwt"
			authMethods = []string{"none"}
		case "/chatgpt":
			authMethod = "private_key_jwt"
			authMethods = []string{"none", "private_key_jwt"}
		case "/absent":
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": clientID, "client_name": "CIMD Test", "redirect_uris": []string{redirect}})
			return
		}
		document := map[string]any{"client_id": clientID, "client_name": "CIMD Test", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": authMethod}
		if authMethods != nil {
			document["token_endpoint_auth_methods_supported"] = authMethods
		}
		_ = json.NewEncoder(w).Encode(document)
	}))
	defer metadata.Close()
	metadataURL = metadata.URL
	server := NewServer(Config{PublicURL: "https://ledger.example.com", HTTPClient: metadata.Client()}, db)
	for _, path := range []string{"/none", "/absent", "/ceiling", "/chatgpt"} {
		client, err := server.resolveClient(ctx, metadataURL+path)
		if err != nil || client.Kind != "cimd" {
			t.Errorf("valid CIMD %s = %#v, %v", path, client, err)
		}
	}
	for _, path := range []string{"/redirect", "/mismatch", "/unsafe", "/large", "/auth", "/auth-list", "/empty-auth-list", "/inconsistent-auth"} {
		if _, err := server.resolveClient(ctx, metadataURL+path); err == nil {
			t.Errorf("invalid CIMD %s accepted", path)
		}
	}
	if redirectFollowed.Load() {
		t.Fatal("CIMD redirect target was fetched")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := server.resolveClient(timeoutCtx, metadataURL+"/slow"); err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("CIMD timeout = %v", err)
	}

	defaultServer := NewServer(Config{PublicURL: "https://ledger.example.com"}, db)
	for _, destination := range []string{"https://127.0.0.1/client.json", "https://10.0.0.1/client.json"} {
		if _, err := defaultServer.resolveClient(ctx, destination); err == nil || !strings.Contains(err.Error(), "not public") {
			t.Errorf("default CIMD fetch accepted %s: %v", destination, err)
		}
	}
}

func TestPasswordRateLimitSeparatesProxyClientsAndRejectsDirectSpoofing(t *testing.T) {
	db, ctx := testdb.Open(t)
	client, err := db.PutClient(ctx, store.OAuthClient{ClientID: "rate-client", Kind: "dcr", Name: "Rate", RedirectURIs: []string{"http://127.0.0.1/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	password, _ := HashPassword("correct")
	server := NewServer(Config{PublicURL: "https://ledger.example.com", PasswordHash: password, InternalProxyCIDR: "172.31.255.2/32"}, db)
	form := url.Values{
		"client_id": {client.ClientID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"code_challenge": {PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")}, "code_challenge_method": {"S256"},
		"scope": {"ledger:read"}, "password": {"wrong"}, "action": {"approve"},
	}
	request := func(remote, authoritative string) int {
		req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Ledger-Client-IP", authoritative)
		req.RemoteAddr = remote
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		return res.Code
	}
	for i := 0; i < 4; i++ {
		if status := request("172.31.255.2:1234", "198.51.100.1"); status != http.StatusUnauthorized {
			t.Fatalf("proxied client A failure %d = %d", i+1, status)
		}
	}
	if status := request("172.31.255.2:1234", "198.51.100.2"); status != http.StatusUnauthorized {
		t.Fatalf("proxied client B inherited client A bucket: %d", status)
	}
	if status := request("172.31.255.2:1234", "198.51.100.1"); status != http.StatusTooManyRequests {
		t.Fatalf("proxied client A fifth failure = %d", status)
	}

	direct := NewServer(Config{PublicURL: "https://ledger.example.com", PasswordHash: password, InternalProxyCIDR: "172.31.255.2/32"}, db)
	server = direct
	for i := 0; i < 4; i++ {
		if status := request("198.51.100.8:1234", "203.0.113."+strconv.Itoa(i+1)); status != http.StatusUnauthorized {
			t.Fatalf("direct failure %d = %d", i+1, status)
		}
	}
	if status := request("198.51.100.8:1234", "203.0.113.99"); status != http.StatusTooManyRequests {
		t.Fatalf("direct authoritative-header spoof bypassed bucket: %d", status)
	}
}
