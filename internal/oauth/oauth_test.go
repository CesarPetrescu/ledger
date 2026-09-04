package oauth

import "testing"

func TestPKCERFC7636AppendixB(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := PKCEChallenge(verifier); got != want {
		t.Fatalf("challenge = %q, want %q", got, want)
	}
	if !ValidVerifier(verifier) || ValidVerifier("short") {
		t.Fatal("verifier length validation failed")
	}
}

func TestRedirectMatcherAndScopes(t *testing.T) {
	registered := []string{"https://app.example/callback", "http://127.0.0.1:4312/callback", "http://[::1]:9999/cb"}
	for _, uri := range registered {
		if !RedirectMatches(uri, registered) {
			t.Errorf("registered redirect rejected: %s", uri)
		}
	}
	for _, uri := range []string{"http://example.com/callback", "http://127.0.0.2/callback", "https://app.example/callback#fragment", "https://app.example/other", "http://127.0.0.1:9999/callback#"} {
		if RedirectMatches(uri, registered) {
			t.Errorf("unsafe/unregistered redirect accepted: %s", uri)
		}
	}
	if ValidRedirectURI("http://127.0.0.2/callback") {
		t.Fatal("only the exact 127.0.0.1 loopback hostname is permitted")
	}
	for _, pair := range [][2]string{
		{"http://127.0.0.1:4312/callback?x=1", "http://127.0.0.1:9999/callback?x=1"},
		{"http://localhost:4312/callback", "http://localhost:9999/callback"},
		{"http://[::1]:4312/callback", "http://[::1]:9999/callback"},
	} {
		if !RedirectMatches(pair[1], []string{pair[0]}) {
			t.Errorf("loopback runtime port mismatch rejected: registered %s, candidate %s", pair[0], pair[1])
		}
	}
	for _, candidate := range []string{
		"http://127.0.0.1:9999/other?x=1",
		"http://127.0.0.1:9999/callback?x=2",
		"http://127.0.0.1:9999/callback",
		"http://localhost:9999/callback?x=1",
		"https://app.example:444/callback",
	} {
		if RedirectMatches(candidate, []string{"http://127.0.0.1:4312/callback?x=1", "http://127.0.0.1:4312/callback?", "https://app.example/callback"}) {
			t.Errorf("non-port redirect difference accepted: %s", candidate)
		}
	}
	if got, ok := ParseScopes("calendar:write ledger:write calendar:read ledger:read ledger:write"); !ok || len(got) != 4 || got[0] != "calendar:read" || got[1] != "calendar:write" || got[2] != "ledger:read" || got[3] != "ledger:write" {
		t.Fatalf("scope parsing = %v, %v", got, ok)
	}
	if got, ok := ParseScopes(""); !ok || len(got) != 1 || got[0] != "ledger:read" {
		t.Fatalf("default scope = %v, %v", got, ok)
	}
	for _, invalid := range []string{"read", "write", "admin"} {
		if _, ok := ParseScopes(invalid); ok {
			t.Errorf("invalid scope %q accepted", invalid)
		}
	}
}
