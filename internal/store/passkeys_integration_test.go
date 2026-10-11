//go:build integration

package store_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestResetPasskeysDeletesPasskeysAndPendingLoginsButKeepsTheOwnerHandle(t *testing.T) {
	db, ctx := testdb.Open(t)
	handle, err := db.OwnerHandle(ctx)
	if err != nil || len(handle) != 32 || bytes.Equal(handle, make([]byte, 32)) {
		t.Fatalf("owner handle = %x, %v", handle, err)
	}
	for _, id := range []string{"one", "two"} {
		if _, _, _, err := db.AddOwnerPasskey(ctx, store.OwnerPasskey{CredentialID: []byte(id), PublicKey: []byte("cose"), AAGUID: []byte{}, Name: "Passkey"}, store.Registration{}); err != nil {
			t.Fatal(err)
		}
	}
	session, err := db.CreateAdminSession(ctx, store.NewAdminSession{Method: store.MethodPasskey})
	if err != nil {
		t.Fatal(err)
	}
	login, err := db.CreateWebAuthnChallenge(ctx, store.ChallengeLogin, []byte(`{"challenge":"a"}`), "", false)
	if err != nil {
		t.Fatal(err)
	}
	register, err := db.CreateWebAuthnChallenge(ctx, store.ChallengeRegister, []byte(`{"challenge":"b"}`), session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	// A challenge is only for its purpose and, for a registration, its session; a mismatch leaves it unused.
	if _, _, err := db.ConsumeWebAuthnChallenge(ctx, login, store.ChallengePasswordLogin, ""); !store.IsNotFound(err) {
		t.Fatalf("wrong purpose = %v", err)
	}
	if _, _, err := db.ConsumeWebAuthnChallenge(ctx, register, store.ChallengeRegister, ""); !store.IsNotFound(err) {
		t.Fatalf("registration without its session = %v", err)
	}
	if _, err := db.CreateWebAuthnChallenge(ctx, store.ChallengeRegister, []byte(`{}`), "", false); err == nil {
		t.Fatal("registration challenge without a session was stored")
	}
	count, err := db.ResetOwnerPasskeys(ctx)
	if err != nil || count != 2 {
		t.Fatalf("reset = %d, %v", count, err)
	}
	if has, err := db.OwnerHasPasskeys(ctx); err != nil || has {
		t.Fatalf("passkeys after reset = %v, %v", has, err)
	}
	if _, _, err := db.ConsumeWebAuthnChallenge(ctx, login, store.ChallengeLogin, ""); !store.IsNotFound(err) {
		t.Fatalf("pending login survived the reset: %v", err)
	}
	if again, err := db.OwnerHandle(ctx); err != nil || !bytes.Equal(again, handle) {
		t.Fatal("reset changed the owner handle")
	}
}

// A password-only sign-in or approval racing the first passkey's registration must not slip through:
// while registration holds PasskeyLock, both wait, and once it commits they are refused.
func TestPasswordOnlyPathsWaitForAPasskeyBeingRegistered(t *testing.T) {
	db, ctx := testdb.Open(t)
	const clientID, redirect = "race-client", "http://127.0.0.1/callback"
	if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: clientID, Kind: "dcr", Name: "Race", RedirectURIs: []string{redirect}}); err != nil {
		t.Fatal(err)
	}
	// With no passkey the password alone still works.
	if _, err := db.CreateAdminSession(ctx, store.NewAdminSession{Method: store.MethodPassword}); err != nil {
		t.Fatalf("password session before any passkey: %v", err)
	}
	if err := db.CreateCode(ctx, "before", clientID, redirect, "challenge", []string{"ledger:read"}, true); err != nil {
		t.Fatalf("password approval before any passkey: %v", err)
	}
	// A registration in progress: the lock is held and the passkey written but not committed.
	registering, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer registering.Rollback(ctx)
	if _, err := registering.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, store.PasskeyLock); err != nil {
		t.Fatal(err)
	}
	if _, err := registering.Exec(ctx, `INSERT INTO owner_passkey(credential_id,public_key,aaguid,name,backup_eligible,backup_state) VALUES('race','cose','','Passkey',true,true)`); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := db.CreateAdminSession(ctx, store.NewAdminSession{Method: store.MethodPassword})
		results <- err
	}()
	go func() {
		results <- db.CreateCode(ctx, "during", clientID, redirect, "challenge", []string{"ledger:read"}, true)
	}()
	select {
	case err := <-results:
		t.Fatalf("a password-only path finished while a passkey was being registered: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := registering.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-results:
			if !errors.Is(err, store.ErrPasskeyRequired) {
				t.Fatalf("after the passkey committed = %v, want ErrPasskeyRequired", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a password-only path never finished")
		}
	}
	// A passkey sign-in is not affected.
	if _, err := db.CreateAdminSession(ctx, store.NewAdminSession{Method: store.MethodPasskey}); err != nil {
		t.Fatalf("passkey session = %v", err)
	}
}

// reset-passkeys cancels a registration that has been checked but not yet saved: the passkey is
// saved under the same lock reset takes, and only while its challenge still exists.
func TestResetPasskeysCancelsARegistrationInFlight(t *testing.T) {
	db, ctx := testdb.Open(t)
	session, err := db.CreateAdminSession(ctx, store.NewAdminSession{Method: store.MethodPassword})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := db.CreateWebAuthnChallenge(ctx, store.ChallengeRegister, []byte(`{"challenge":"c"}`), session.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	// The handler has read the challenge and verified the credential; reset runs before the save.
	if _, err := db.PeekWebAuthnChallenge(ctx, challenge, store.ChallengeRegister, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResetOwnerPasskeys(ctx); err != nil {
		t.Fatal(err)
	}
	passkey := store.OwnerPasskey{CredentialID: []byte("late"), PublicKey: []byte("cose"), AAGUID: []byte{}, Name: "Passkey"}
	if _, _, _, err := db.AddOwnerPasskey(ctx, passkey, store.Registration{ChallengeID: challenge, Session: session.ID}); !errors.Is(err, store.ErrRegistrationGone) {
		t.Fatalf("registration after reset = %v, want ErrRegistrationGone", err)
	}
	if has, err := db.OwnerHasPasskeys(ctx); err != nil || has {
		t.Fatalf("passkey saved after reset: %v %v", has, err)
	}
	// And reset waits for a registration holding the lock instead of running beside it.
	registering, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer registering.Rollback(ctx)
	if _, err := registering.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, store.PasskeyLock); err != nil {
		t.Fatal(err)
	}
	if _, err := registering.Exec(ctx, `INSERT INTO owner_passkey(credential_id,public_key,aaguid,name,backup_eligible,backup_state) VALUES('inflight','cose','','Passkey',true,true)`); err != nil {
		t.Fatal(err)
	}
	reset := make(chan int64, 1)
	go func() {
		count, _ := db.ResetOwnerPasskeys(ctx)
		reset <- count
	}()
	select {
	case <-reset:
		t.Fatal("reset ran while a registration held the lock")
	case <-time.After(300 * time.Millisecond):
	}
	if err := registering.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case count := <-reset:
		if count != 1 {
			t.Fatalf("reset after the registration committed removed %d passkeys, want 1", count)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reset never finished")
	}
}
