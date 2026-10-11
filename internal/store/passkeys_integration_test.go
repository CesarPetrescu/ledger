//go:build integration

package store_test

import (
	"bytes"
	"testing"

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
		if _, _, err := db.AddOwnerPasskey(ctx, store.OwnerPasskey{CredentialID: []byte(id), PublicKey: []byte("cose"), AAGUID: []byte{}, Name: "Passkey"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	session, err := db.CreateAdminSession(ctx, store.NewAdminSession{})
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
