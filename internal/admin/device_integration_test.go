//go:build integration

package admin

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestDeviceLoginApprovalRevocationAndProtocol(t *testing.T) {
	db, ctx := testdb.Open(t)
	auth := oauth.NewServer(oauth.Config{PublicURL: testPublicURL}, db)
	admin := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, session := login(t, admin, "correct horse", "")
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		auth.ServeHTTP(res, req)
		return res
	}
	registration := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(`{"client_name":"Atlas machine","grant_types":["urn:ietf:params:oauth:grant-type:device_code","refresh_token"]}`))
	reg := httptest.NewRecorder()
	auth.ServeHTTP(reg, registration)
	var client store.OAuthClient
	if err := json.Unmarshal(reg.Body.Bytes(), &client); err != nil || reg.Code != 201 || client.ClientID == "" {
		t.Fatalf("registration: %d %s", reg.Code, reg.Body.String())
	}
	create := func() (string, string) {
		t.Helper()
		res := post("/oauth/device", url.Values{"client_id": {client.ClientID}, "scope": {"ledger:read ledger:write"}})
		var d struct {
			Code string `json:"device_code"`
			User string `json:"user_code"`
			URI  string `json:"verification_uri"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &d); err != nil || res.Code != 200 || d.Code == "" || d.URI != testPublicURL+"/admin/connect" {
			t.Fatalf("device: %d %s", res.Code, res.Body.String())
		}
		return d.Code, d.User
	}
	ready := func() {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `UPDATE oauth_device SET poll_after=now()-interval '1 second'`); err != nil {
			t.Fatal(err)
		}
	}
	poll := func(code, clientID string) *httptest.ResponseRecorder {
		return post("/oauth/token", url.Values{"grant_type": {oauth.DeviceGrant}, "client_id": {clientID}, "device_code": {code}})
	}
	wantError := func(res *httptest.ResponseRecorder, want string) {
		t.Helper()
		var body map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &body)
		if res.Code != 400 || body["error"] != want {
			t.Fatalf("want %s: %d %s", want, res.Code, res.Body.String())
		}
	}
	code, user := create()
	wantError(poll(code, client.ClientID), "slow_down")
	var interval int
	if err := db.Pool.QueryRow(ctx, `SELECT poll_interval FROM oauth_device`).Scan(&interval); err != nil || interval != 10 {
		t.Fatalf("slow down interval=%d: %v", interval, err)
	}
	ready()
	wantError(poll(code, client.ClientID), "authorization_pending")
	wantError(poll(code, "another-client"), "invalid_grant")
	body := `{"user_code":"` + user + `","action":"approve"}`
	if res := request(t, admin, "POST", "/admin/api/oauth/device", body, nil); res.Code != 401 {
		t.Fatalf("anonymous approval=%d", res.Code)
	}
	if res := request(t, admin, "POST", "/admin/api/oauth/device", body, authed(session, false)); res.Code != 403 {
		t.Fatalf("csrf-less approval=%d", res.Code)
	}
	lookup := request(t, admin, "POST", "/admin/api/oauth/device", `{"user_code":"`+strings.ToLower(user)+`","action":"lookup"}`, authed(session, true))
	if lookup.Code != 200 || strings.Contains(lookup.Body.String(), code) || !strings.Contains(lookup.Body.String(), "ledger:read ledger:write") {
		t.Fatalf("lookup=%d %s", lookup.Code, lookup.Body.String())
	}
	ready()
	wantError(poll(code, client.ClientID), "authorization_pending") // Lookup never approves.
	if res := request(t, admin, "POST", "/admin/api/oauth/device", body, authed(session, true)); res.Code != 204 {
		t.Fatalf("approval=%d %s", res.Code, res.Body.String())
	}
	ready()
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wg.Go(func() { results <- poll(code, client.ClientID) })
	}
	wg.Wait()
	close(results)
	var pair struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	successes := 0
	for res := range results {
		if res.Code == 200 {
			successes++
			if err := json.Unmarshal(res.Body.Bytes(), &pair); err != nil {
				t.Fatal(err)
			}
		} else {
			wantError(res, "invalid_grant")
		}
	}
	if successes != 1 || pair.Scope != "ledger:read ledger:write" {
		t.Fatalf("redemption successes=%d scope=%s", successes, pair.Scope)
	}
	if _, _, err := db.LookupAccess(ctx, pair.Access); err != nil {
		t.Fatal(err)
	}
	// A different client cannot revoke this machine's credentials.
	if res := post("/oauth/revoke", url.Values{"client_id": {"another-client"}, "token": {pair.Refresh}}); res.Code != 200 {
		t.Fatal(res.Code)
	}
	if _, _, err := db.LookupAccess(ctx, pair.Access); err != nil {
		t.Fatal("foreign revocation succeeded")
	}
	rotated, err := db.ExchangeRefresh(ctx, pair.Refresh, client.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if res := post("/oauth/revoke", url.Values{"client_id": {client.ClientID}, "token": {pair.Refresh}}); res.Code != 200 {
		t.Fatal(res.Code)
	}
	if _, _, err := db.LookupAccess(ctx, rotated.AccessToken); err == nil {
		t.Fatal("logout failed to revoke rotated family")
	}
	code, user = create()
	if err := db.DecideDevice(ctx, user, false); err != nil {
		t.Fatal(err)
	}
	wantError(poll(code, client.ClientID), "access_denied")
	code, _ = create()
	if _, err := db.Pool.Exec(ctx, `UPDATE oauth_device SET expires_at=now()-interval '1 second' WHERE status='pending'`); err != nil {
		t.Fatal(err)
	}
	wantError(poll(code, client.ClientID), "expired_token")
	code, user = create()
	if _, err := db.Revoke(ctx, client.ClientID, false); err != nil {
		t.Fatal(err)
	}
	if err := db.DecideDevice(ctx, user, true); err != store.ErrInvalidGrant {
		t.Fatalf("revoked request approved: %v", err)
	}
	wantError(poll(code, client.ClientID), "invalid_grant")
	if _, err := db.GC(ctx); err != nil {
		t.Fatal(err)
	}
}
