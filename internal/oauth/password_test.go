package oauth

import "testing"

func TestArgon2idPHCRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") || VerifyPassword(hash, "wrong") || VerifyPassword("malformed", "anything") {
		t.Fatal("password verification result is incorrect")
	}
}
