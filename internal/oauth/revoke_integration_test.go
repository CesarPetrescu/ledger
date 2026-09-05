//go:build integration

package oauth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestRevokeInvalidatesPendingCodesAndTokens(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "client"
		if all {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx := testdb.Open(t)
			const redirect = "http://127.0.0.1/callback"
			const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
			for _, id := range []string{"agent", "other"} {
				if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: id, Kind: "dcr", RedirectURIs: []string{redirect}}); err != nil {
					t.Fatal(err)
				}
				for _, suffix := range []string{"-issued", "-pending"} {
					if err := db.CreateCode(ctx, id+suffix, id, redirect, PKCEChallenge(verifier), []string{ScopeRead}); err != nil {
						t.Fatal(err)
					}
				}
			}
			pair, err := db.ExchangeCode(ctx, "agent-issued", "agent", redirect, verifier, VerifyPKCE)
			if err != nil {
				t.Fatal(err)
			}
			if count, err := db.Revoke(ctx, "agent", all); err != nil || count != 2 {
				t.Fatalf("revoke = %d, %v", count, err)
			}
			if _, _, err := db.LookupAccess(ctx, pair.AccessToken); err == nil {
				t.Fatal("access survived revoke")
			}
			if _, err := db.ExchangeRefresh(ctx, pair.RefreshToken, "agent"); err != store.ErrInvalidGrant {
				t.Fatalf("refresh = %v", err)
			}
			if _, err := db.ExchangeCode(ctx, "agent-pending", "agent", redirect, verifier, VerifyPKCE); err != store.ErrInvalidGrant {
				t.Fatalf("pending code survived revoke: %v", err)
			}
			_, err = db.ExchangeCode(ctx, "other-pending", "other", redirect, verifier, VerifyPKCE)
			if all && err != store.ErrInvalidGrant || !all && err != nil {
				t.Fatalf("other client exchange = %v", err)
			}
			if _, err := db.GetClient(ctx, "agent"); err != nil {
				t.Fatal("registration removed", err)
			}
			if err := db.CreateCode(ctx, "fresh-approval", "agent", redirect, PKCEChallenge(verifier), []string{ScopeRead}); err != nil {
				t.Fatal(err)
			}
			fresh, err := db.ExchangeCode(ctx, "fresh-approval", "agent", redirect, verifier, VerifyPKCE)
			if err != nil {
				t.Fatal("fresh authorization failed", err)
			}
			if _, err := db.ExchangeRefresh(ctx, fresh.RefreshToken, "agent"); err != nil {
				t.Fatal("fresh refresh failed", err)
			}
		})
	}
}

func TestRevokeWaitsForConcurrentIssuance(t *testing.T) {
	for _, grant := range []string{"code", "refresh"} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/all=%t", grant, all), func(t *testing.T) {
				db, parent := testdb.Open(t)
				ctx, cancel := context.WithTimeout(parent, 15*time.Second)
				defer cancel()
				const id = "agent"
				const redirect = "http://127.0.0.1/callback"
				if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: id, Kind: "dcr", RedirectURIs: []string{redirect}}); err != nil {
					t.Fatal(err)
				}
				if err := db.CreateCode(ctx, "code", id, redirect, PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"), []string{ScopeRead}); err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256([]byte("code"))
				raw := hash[:16]
				raw[6] = raw[6]&0x0f | 0x40
				raw[8] = raw[8]&0x3f | 0x80
				family := fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
				var refresh string
				if grant == "refresh" {
					pair, err := db.ExchangeCode(ctx, "code", id, redirect, "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", VerifyPKCE)
					if err != nil {
						t.Fatal(err)
					}
					family, refresh = pair.Family, pair.RefreshToken
				}
				blocker, err := db.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback(parent)
				if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, family); err != nil {
					t.Fatal(err)
				}
				issued := make(chan error, 1)
				go func() {
					if grant == "code" {
						_, err := db.ExchangeCode(ctx, "code", id, redirect, "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", VerifyPKCE)
						issued <- err
					} else {
						_, err := db.ExchangeRefresh(ctx, refresh, id)
						issued <- err
					}
				}()
				waitForLock := func(query string) {
					t.Helper()
					for {
						var waiting bool
						err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query=$1)`, query).Scan(&waiting)
						if err != nil {
							t.Fatal(err)
						}
						if waiting {
							return
						}
						select {
						case <-ctx.Done():
							t.Fatal("operation did not wait for lock")
						case <-time.After(10 * time.Millisecond):
						}
					}
				}
				waitForLock(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`)
				revoked := make(chan error, 1)
				go func() { _, err := db.Revoke(ctx, id, all); revoked <- err }()
				waitForLock(`SELECT pg_advisory_xact_lock(hashtext('ledger:oauth'), 0)`)
				if err := blocker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-issued; err != nil {
					t.Fatal(err)
				}
				if err := <-revoked; err != nil {
					t.Fatal(err)
				}
				var live int
				if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM oauth_token WHERE NOT revoked`).Scan(&live); err != nil || live != 0 {
					t.Fatalf("concurrent issuance left %d tokens: %v", live, err)
				}
			})
		}
	}
}
