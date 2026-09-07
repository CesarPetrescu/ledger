//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/oauth"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestCLIConnectRefreshAndReconnectAgainstLedger(t *testing.T) {
	db, ctx := testdb.Open(t)
	isolateConfig(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("LEDGER_AUTO_UPDATE", "0")
	var auth http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/device" {
			auth.ServeHTTP(w, r)
			return
		}
		res := httptest.NewRecorder()
		auth.ServeHTTP(res, r)
		var d struct {
			User string `json:"user_code"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &d); err != nil || res.Code != 200 {
			t.Error("device issuance failed")
		} else if err = db.DecideDevice(r.Context(), d.User, true); err != nil {
			t.Error(err)
		}
		for k, v := range res.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(res.Code)
		_, _ = w.Write(res.Body.Bytes())
	}))
	defer server.Close()
	auth = oauth.NewServer(oauth.Config{PublicURL: server.URL}, db)
	old := authHTTP
	authHTTP = server.Client()
	t.Cleanup(func() { authHTTP = old })
	path, err := credentialPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = connect(ctx, path, server.URL, "Atlas machine"); err != nil {
		t.Fatal(err)
	}
	original, err := loadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.LookupAccess(ctx, original.AccessToken); err != nil {
		t.Fatal(err)
	}
	original.ExpiresAt = time.Now().Add(-time.Second)
	if err = saveCredentials(path, original); err != nil {
		t.Fatal(err)
	}
	if _, err = authHeaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	refreshed, err := loadCredentials(path)
	if err != nil || refreshed.RefreshToken == original.RefreshToken {
		t.Fatal("CLI failed to rotate refresh token")
	}
	if _, err = db.Revoke(ctx, original.ClientID, false); err != nil {
		t.Fatal(err)
	}
	if err = connect(ctx, path, server.URL, "Atlas replacement"); err != nil {
		t.Fatal(err)
	}
	replacement, err := loadCredentials(path)
	if err != nil || replacement.ClientID == original.ClientID {
		t.Fatal("revoked profile was not reauthorized")
	}
	// Setup writes only into an isolated Codex home and delegates OAuth cleanup to Codex.
	fakeCodex(t)
	if err = configureCodex(server.URL, "codex"); err != nil {
		t.Fatal(err)
	}
	if err = configureCodex(server.URL, "codex"); err != nil {
		t.Fatal(err)
	}
	if err = run(context.Background(), []string{"auth", "logout"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.LookupAccess(ctx, replacement.AccessToken); err == nil {
		t.Fatal("CLI logout did not revoke authorization")
	}
}
