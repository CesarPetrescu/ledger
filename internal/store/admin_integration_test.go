//go:build integration

package store_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestAdminSessionStoresOnlyHashesAndExpires(t *testing.T) {
	db, ctx := testdb.Open(t)
	session, err := db.CreateAdminSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.ID) < 40 || len(session.CSRFToken) < 40 || session.ID == session.CSRFToken {
		t.Fatalf("session material too short or shared: id=%d csrf=%d", len(session.ID), len(session.CSRFToken))
	}
	if remaining := time.Until(session.ExpiresAt); remaining < 11*time.Hour || remaining > 13*time.Hour {
		t.Fatalf("session TTL = %v, want about 12h", remaining)
	}
	hash := sha256.Sum256([]byte(session.ID))
	var stored int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_session WHERE hash=$1`, hash[:]).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("hashed session row count = %d, %v", stored, err)
	}
	var leaked bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_session WHERE hash=$1::bytea OR encode(hash,'hex')=$2 OR csrf_token=$2 OR csrf_token=$3)`, []byte(session.ID), session.ID, session.ID).Scan(&leaked); err != nil || leaked {
		t.Fatalf("raw session material stored at rest: leaked=%v err=%v", leaked, err)
	}

	before, err := db.LookupAdminSession(ctx, session.ID)
	if err != nil || before.CSRFToken != session.CSRFToken || !before.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("lookup = %#v, %v", before, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE admin_session SET last_seen_at=now()-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LookupAdminSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	var lastSeenAge time.Duration
	if err := db.Pool.QueryRow(ctx, `SELECT now()-last_seen_at FROM admin_session`).Scan(&lastSeenAge); err != nil || lastSeenAge > time.Minute {
		t.Fatalf("last_seen_at not refreshed: age=%v err=%v", lastSeenAge, err)
	}
	if _, err := db.LookupAdminSession(ctx, "not-a-session"); !store.IsNotFound(err) {
		t.Fatalf("unknown session error = %v", err)
	}

	if count, err := db.CountActiveAdminSessions(ctx); err != nil || count != 1 {
		t.Fatalf("active sessions = %d, %v", count, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE admin_session SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LookupAdminSession(ctx, session.ID); !store.IsNotFound(err) {
		t.Fatalf("expired session error = %v", err)
	}
	if count, err := db.CountActiveAdminSessions(ctx); err != nil || count != 0 {
		t.Fatalf("active sessions after expiry = %d, %v", count, err)
	}
}

func TestAdminSessionRevocation(t *testing.T) {
	db, ctx := testdb.Open(t)
	first, err := db.CreateAdminSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateAdminSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAdminSession(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LookupAdminSession(ctx, first.ID); !store.IsNotFound(err) {
		t.Fatalf("deleted session error = %v", err)
	}
	if _, err := db.LookupAdminSession(ctx, second.ID); err != nil {
		t.Fatalf("unrelated session revoked: %v", err)
	}
	if err := db.DeleteAdminSession(ctx, "missing"); err != nil {
		t.Fatalf("deleting a missing session must be idempotent: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO admin_session(hash,csrf_token,expires_at) VALUES(decode(repeat('ab',32),'hex'),'expired',now()-interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	if revoked, err := db.RevokeAdminSessions(ctx); err != nil || revoked != 2 {
		t.Fatalf("revoke all = %d, %v", revoked, err)
	}
	if _, err := db.LookupAdminSession(ctx, second.ID); !store.IsNotFound(err) {
		t.Fatalf("session survived revoke-all: %v", err)
	}
	if count, err := db.CountActiveAdminSessions(ctx); err != nil || count != 0 {
		t.Fatalf("active sessions after revoke-all = %d, %v", count, err)
	}
}

func TestAdminOverviewCountsAreTruthful(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "beacon", Name: "Beacon", Tier: "park"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.AppendEntry(ctx, "atlas", "note", "Entry", "ledger-admin", "admin-session-000000000000"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: "client-a", Kind: "dcr", Name: "A", RedirectURIs: []string{"http://127.0.0.1/cb"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO oauth_token(hash,kind,client_id,scope,family,expires_at) VALUES
(decode(repeat('01',32),'hex'),'access','client-a','ledger:read','00000000-0000-4000-8000-000000000001',now()+interval '10 minutes'),
(decode(repeat('02',32),'hex'),'refresh','client-a','ledger:read','00000000-0000-4000-8000-000000000001',now()+interval '10 days'),
(decode(repeat('03',32),'hex'),'access','client-a','ledger:read','00000000-0000-4000-8000-000000000002',now()-interval '1 minute')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAdminSession(ctx); err != nil {
		t.Fatal(err)
	}
	counts, err := db.AdminCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := store.AdminCounts{Projects: 2, Entries: 3, Clients: 1, ActiveTokens: 1, ActiveSessions: 1}
	if counts != want {
		t.Fatalf("counts = %#v, want %#v", counts, want)
	}
	recent, err := db.RecentEntries(ctx, 2)
	if err != nil || len(recent) != 2 || recent[0].ProjectName != "Atlas" || recent[0].ID < recent[1].ID {
		t.Fatalf("recent entries = %#v, %v", recent, err)
	}
	byID, err := db.EntriesByID(ctx, []int64{recent[0].ID, 999999})
	if err != nil || len(byID) != 1 || byID[recent[0].ID].Slug != "atlas" {
		t.Fatalf("entries by id = %#v, %v", byID, err)
	}
	tokens, err := db.ActiveTokenCounts(ctx)
	if err != nil || tokens["client-a"] != 1 {
		t.Fatalf("active token counts = %#v, %v", tokens, err)
	}
}
