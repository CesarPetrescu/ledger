//go:build integration

package admin

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestOwnerAuthorizationAndApprovalPasswordReset(t *testing.T) {
	db, ctx := testdb.Open(t)
	client, err := db.PutClient(ctx, store.OAuthClient{ClientID: "desk-client", Kind: "dcr", Name: "Desk app", RedirectURIs: []string{"https://client.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	admin := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, owner := login(t, admin, "correct horse", "")
	verifier := strings.Repeat("v", 43)
	params := url.Values{
		"client_id": {client.ClientID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"code_challenge": {oauth.PKCEChallenge(verifier)}, "code_challenge_method": {"S256"},
		"scope": {"ledger:read calendar:write"}, "resource": {testPublicURL + "/mcp"}, "state": {"state & + private"},
	}
	oldHash, _ := oauth.HashPassword("forgotten approval")
	authConfig := oauth.Config{PublicURL: testPublicURL, PasswordHash: oldHash}
	auth := oauth.NewServer(authConfig, db)
	page := httptest.NewRecorder()
	auth.ServeHTTP(page, httptest.NewRequest("GET", "/oauth/authorize?"+params.Encode(), nil))
	link := regexp.MustCompile(`href="([^"]+)">Continue with Ledger owner login`).FindStringSubmatch(page.Body.String())
	if page.Code != 200 || len(link) != 2 {
		t.Fatalf("owner login link missing: %d", page.Code)
	}
	ownerURL, err := url.Parse(html.UnescapeString(link[1]))
	if err != nil || ownerURL.Path != "/admin/authorize" || ownerURL.Query().Get("state") != params.Get("state") || ownerURL.Query().Get("code_challenge") != params.Get("code_challenge") {
		t.Fatal("owner link lost the authorization request")
	}
	path := "/admin/api/oauth/authorize?" + ownerURL.RawQuery
	review := request(t, admin, "GET", path, "", authed(owner, false))
	if review.Code != 200 || !strings.Contains(review.Body.String(), `"client_name":"Desk app"`) || !strings.Contains(review.Body.String(), `"calendar:write"`) {
		t.Fatalf("review = %d %s", review.Code, review.Body.String())
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_code`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("review issued codes: %d, %v", count, err)
	}
	for _, headers := range []map[string]string{
		nil, authed(owner, false), {"Cookie": owner.cookie, "Origin": "https://evil.example", "X-CSRF-Token": owner.csrf},
		{"Cookie": owner.cookie, "Origin": testPublicURL, "X-CSRF-Token": "wrong"},
	} {
		for _, route := range []struct{ method, path, body string }{
			{"POST", path, `{"action":"approve"}`},
			{"PUT", "/admin/api/oauth/password", `{"current_password":"correct horse","new_password":"new approval secret"}`},
		} {
			res := request(t, admin, route.method, route.path, route.body, headers)
			if res.Code != 401 && res.Code != 403 {
				t.Fatalf("unguarded %s = %d", route.path, res.Code)
			}
		}
	}
	for key, value := range map[string]string{"client_id": "unknown", "redirect_uri": "https://evil.example/callback", "scope": "admin", "code_challenge_method": "plain", "resource": "https://evil.example/mcp"} {
		bad, _ := url.ParseQuery(params.Encode())
		bad.Set(key, value)
		for _, method := range []string{"GET", "POST"} {
			res := request(t, admin, method, "/admin/api/oauth/authorize?"+bad.Encode(), `{"action":"approve"}`, authed(owner, true))
			if res.Code != 400 {
				t.Fatalf("%s accepted invalid %s: %d", method, key, res.Code)
			}
		}
	}
	decide := func(action string) *url.URL {
		t.Helper()
		res := request(t, admin, "POST", path, `{"action":"`+action+`"}`, authed(owner, true))
		var body struct {
			Redirect string `json:"redirect_url"`
		}
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &body) != nil {
			t.Fatalf("decision = %d %s", res.Code, res.Body.String())
		}
		destination, err := url.Parse(body.Redirect)
		if err != nil || destination.Query().Get("state") != params.Get("state") || destination.Query().Get("iss") != testPublicURL {
			t.Fatalf("invalid redirect: %s %v", body.Redirect, err)
		}
		return destination
	}
	if denied := decide("deny"); denied.Query().Get("error") != "access_denied" || denied.Query().Get("code") != "" {
		t.Fatalf("denial = %s", denied)
	}
	approved := decide("approve")
	postForm := func(server http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		return res
	}
	token := postForm(auth, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ClientID}, "code": {approved.Query().Get("code")}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {verifier}})
	var pair struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if token.Code != 200 || json.Unmarshal(token.Body.Bytes(), &pair) != nil || pair.Scope != "calendar:write ledger:read" {
		t.Fatalf("owner token exchange = %d %s", token.Code, token.Body.String())
	}
	params.Set("action", "approve")
	params.Set("password", "forgotten approval")
	if res := postForm(auth, "/oauth/authorize", params); res.Code != 302 {
		t.Fatalf("setup password = %d", res.Code)
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"current_password":"correct horse","new_password":"short"}`, 400},
		{`{"current_password":"wrong","new_password":"new approval secret"}`, 403},
		{`{"current_password":"correct horse","new_password":"correct horse"}`, 400},
		{`{"current_password":"correct horse","new_password":"new approval secret"}`, 204},
	} {
		res := request(t, admin, "PUT", "/admin/api/oauth/password", test.body, authed(owner, true))
		if res.Code != test.status {
			t.Fatalf("reset = %d %s, want %d", res.Code, res.Body.String(), test.status)
		}
	}
	stored, err := db.OAuthPasswordHash(ctx, oldHash)
	if err != nil || stored == oldHash || stored == "new approval secret" || !oauth.VerifyPassword(stored, "new approval secret") {
		t.Fatal("reset did not store the new password hash")
	}
	if res := postForm(auth, "/oauth/authorize", params); res.Code != 401 {
		t.Fatalf("old password accepted after reset: %d", res.Code)
	}
	params.Set("password", "new approval secret")
	for _, server := range []http.Handler{auth, oauth.NewServer(authConfig, db)} {
		if res := postForm(server, "/oauth/authorize", params); res.Code != 302 {
			t.Fatalf("new password failed live or after restart: %d %s", res.Code, res.Body.String())
		}
	}
	if _, err := db.Pool.Exec(ctx, `DROP TABLE oauth_password`); err != nil {
		t.Fatal(err)
	}
	params.Set("password", "forgotten approval")
	if res := postForm(auth, "/oauth/authorize", params); res.Code != 500 {
		t.Fatalf("database failure fell back to setup password: %d", res.Code)
	}
	if _, err := db.RevokeAdminSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if res := request(t, admin, "POST", path, `{"action":"approve"}`, authed(owner, true)); res.Code != 401 {
		t.Fatalf("revoked owner session approved access: %d", res.Code)
	}
}
