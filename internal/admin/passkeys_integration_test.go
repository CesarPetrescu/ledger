//go:build integration

package admin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/passkey"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// Authenticator data flags (WebAuthn §6.1).
const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// softPasskey is a software authenticator: a P-256 key that answers WebAuthn options JSON the way a
// synced passkey provider such as Bitwarden does (fmt "none", backed up, counter always 0).
type softPasskey struct {
	t       *testing.T
	key     *ecdsa.PrivateKey
	id      []byte
	handle  []byte
	rpID    string
	origin  string
	aaguid  []byte
	counter uint32
	flags   byte
}

func newSoftPasskey(t *testing.T) *softPasskey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	bitwarden, _ := hex.DecodeString("d548826e79b4db40a3d811116f7e8349")
	return &softPasskey{t: t, key: key, id: id, rpID: "ledger.example.com", origin: testPublicURL, aaguid: bitwarden, flags: flagUP | flagUV | flagBE | flagBS}
}

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func (p *softPasskey) clientData(kind, challenge string) []byte {
	body, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": p.origin, "crossOrigin": false})
	return body
}

func (p *softPasskey) authData(flags byte, attested []byte) []byte {
	rpIDHash := sha256.Sum256([]byte(p.rpID))
	out := append(rpIDHash[:], flags)
	out = binary.BigEndian.AppendUint32(out, p.counter)
	return append(out, attested...)
}

// create answers PublicKeyCredentialCreationOptionsJSON with RegistrationResponseJSON.
func (p *softPasskey) create(options json.RawMessage) string {
	p.t.Helper()
	var o struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		p.t.Fatal(err)
	}
	p.handle, _ = base64.RawURLEncoding.DecodeString(o.User.ID)
	point, err := p.key.PublicKey.Bytes()
	if err != nil {
		p.t.Fatal(err)
	}
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: point[1:33], -3: point[33:65]})
	if err != nil {
		p.t.Fatal(err)
	}
	attested := append(append([]byte{}, p.aaguid...), byte(len(p.id)>>8), byte(len(p.id)))
	attested = append(append(attested, p.id...), cose...)
	attestation, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": p.authData(p.flags|flagAT, attested)})
	if err != nil {
		p.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64(p.id), "rawId": b64(p.id), "type": "public-key", "authenticatorAttachment": "platform",
		"response": map[string]any{"clientDataJSON": b64(p.clientData("webauthn.create", o.Challenge)), "attestationObject": b64(attestation), "transports": []string{"internal", "hybrid"}},
		// Some providers volunteer credProps without being asked.
		"clientExtensionResults": map[string]any{"credProps": map[string]any{"rk": true}},
	})
	return string(body)
}

// get answers PublicKeyCredentialRequestOptionsJSON with AuthenticationResponseJSON.
func (p *softPasskey) get(options json.RawMessage) string {
	p.t.Helper()
	var o struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(options, &o); err != nil {
		p.t.Fatal(err)
	}
	authData := p.authData(p.flags, nil)
	clientData := p.clientData("webauthn.get", o.Challenge)
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, p.key, digest[:])
	if err != nil {
		p.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64(p.id), "rawId": b64(p.id), "type": "public-key", "authenticatorAttachment": "platform",
		"response":               map[string]any{"clientDataJSON": b64(clientData), "authenticatorData": b64(authData), "signature": b64(signature), "userHandle": b64(p.handle)},
		"clientExtensionResults": map[string]any{},
	})
	return string(body)
}

// signInHeaders are a browser's sign-in headers from one client address, plus extra header pairs.
func signInHeaders(ip string, extra ...string) map[string]string {
	headers := map[string]string{"Origin": testPublicURL, "X-Ledger-Client-IP": ip}
	for i := 0; i+1 < len(extra); i += 2 {
		headers[extra[i]] = extra[i+1]
	}
	return headers
}

func decode[T any](t *testing.T, res *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(res.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %d %s: %v", res.Code, res.Body.String(), err)
	}
	return value
}

type challengeResponse struct {
	ChallengeID string          `json:"challenge_id"`
	Pending     string          `json:"pending"`
	Required    bool            `json:"passkey_required"`
	Options     json.RawMessage `json:"options"`
}

func registrationOptions(t *testing.T, server http.Handler, owner session) challengeResponse {
	t.Helper()
	res := request(t, server, http.MethodPost, "/admin/api/passkeys/options", `{"password":"correct horse"}`, authed(owner, true))
	if res.Code != http.StatusOK {
		t.Fatalf("registration options = %d %s", res.Code, res.Body.String())
	}
	return decode[challengeResponse](t, res)
}

func finishRegistration(t *testing.T, server http.Handler, owner session, challengeID, credential string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, server, http.MethodPost, "/admin/api/passkeys", `{"challenge_id":"`+challengeID+`","credential":`+credential+`}`, authed(owner, true))
}

func registerSoftPasskey(t *testing.T, server http.Handler, owner session, p *softPasskey) {
	t.Helper()
	started := registrationOptions(t, server, owner)
	if res := finishRegistration(t, server, owner, started.ChallengeID, p.create(started.Options)); res.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", res.Code, res.Body.String())
	}
}

func passkeyOptions(t *testing.T, server http.Handler, ip string) challengeResponse {
	t.Helper()
	res := request(t, server, http.MethodPost, "/admin/api/login/passkey/options", `{}`, signInHeaders(ip))
	if res.Code != http.StatusOK {
		t.Fatalf("passkey options = %d %s", res.Code, res.Body.String())
	}
	return decode[challengeResponse](t, res)
}

func finishPasskey(t *testing.T, server http.Handler, field, id, credential, ip string, extra ...string) *httptest.ResponseRecorder {
	t.Helper()
	return request(t, server, http.MethodPost, "/admin/api/login/passkey", `{"`+field+`":"`+id+`","credential":`+credential+`,"remember":true}`, signInHeaders(ip, extra...))
}

func TestPasskeysReplacePasswordOnlySignIn(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	p := newSoftPasskey(t)

	// With no passkey the password alone signs in, as before.
	res := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse"}`, signInHeaders("198.51.100.1"))
	owner, cookie := cookieSession(res)
	if res.Code != http.StatusOK || cookie == nil || cookie.MaxAge > int(store.AdminSessionTTL/time.Second) || !strings.Contains(res.Body.String(), `"remember":false`) {
		t.Fatalf("password login = %d %s", res.Code, res.Body.String())
	}
	if list := request(t, server, http.MethodGet, "/admin/api/passkeys", "", authed(owner, false)); list.Body.String() != `{"passkeys":[],"required":false}`+"\n" {
		t.Fatalf("empty passkeys = %s", list.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/login/passkey/options", `{}`, signInHeaders("198.51.100.1")); res.Code != http.StatusConflict || res.Body.String() != `{"error":"no passkeys"}`+"\n" {
		t.Fatalf("options without passkeys = %d %s", res.Code, res.Body.String())
	}

	// Registering needs the password as well as the session, CSRF, and Origin.
	if res := request(t, server, http.MethodPost, "/admin/api/passkeys/options", `{"password":"wrong"}`, authed(owner, true)); res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "Password not accepted.") {
		t.Fatalf("wrong registration password = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/passkeys/options", `{"password":"correct horse"}`, map[string]string{"Cookie": owner.cookie, "Origin": testPublicURL}); res.Code != http.StatusForbidden {
		t.Fatalf("registration without CSRF = %d", res.Code)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(owner, false)); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"method":"password"`) {
		t.Fatalf("wrong confirmation ended the session: %d %s", res.Code, res.Body.String())
	}
	started := registrationOptions(t, server, owner)
	var o struct {
		RP   map[string]string `json:"rp"`
		User struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
		Params []struct {
			Alg int `json:"alg"`
		} `json:"pubKeyCredParams"`
		Selection   map[string]any    `json:"authenticatorSelection"`
		Attestation string            `json:"attestation"`
		Exclude     []json.RawMessage `json:"excludeCredentials"`
	}
	if err := json.Unmarshal(started.Options, &o); err != nil {
		t.Fatal(err)
	}
	handle, _ := db.OwnerHandle(ctx)
	algorithms := map[int]bool{}
	for _, param := range o.Params {
		algorithms[param.Alg] = true
	}
	if o.RP["id"] != "ledger.example.com" || o.RP["name"] != "Ledger" || len(handle) != 32 || o.User.ID != b64(handle) || o.User.Name != "owner" || o.User.DisplayName != "Ledger owner" ||
		len(o.Params) != 3 || !algorithms[-7] || !algorithms[-8] || !algorithms[-257] ||
		o.Selection["residentKey"] != "required" || o.Selection["userVerification"] != "required" || o.Attestation != "none" || len(o.Exclude) != 0 {
		t.Fatalf("creation options = %s", started.Options)
	}
	credential := p.create(started.Options)
	registered := finishRegistration(t, server, owner, started.ChallengeID, credential)
	view := decode[struct {
		Passkey passkeyView `json:"passkey"`
	}](t, registered)
	if registered.Code != http.StatusCreated || view.Passkey.Name != "Bitwarden" || view.Passkey.Provider != "Bitwarden" || view.Passkey.LastUsedAt != nil || view.Passkey.ID == "" {
		t.Fatalf("register = %d %s", registered.Code, registered.Body.String())
	}
	// The password-only session that added it now has a new cookie and CSRF token.
	owner, _ = cookieSession(registered)
	if replay := finishRegistration(t, server, owner, started.ChallengeID, credential); replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed registration = %d %s", replay.Code, replay.Body.String())
	}
	again := registrationOptions(t, server, owner)
	if !strings.Contains(string(again.Options), `"excludeCredentials":[{"type":"public-key","id":"`+b64(p.id)+`"`) {
		t.Fatalf("second registration does not exclude the first passkey: %s", again.Options)
	}
	if res := finishRegistration(t, server, owner, again.ChallengeID, p.create(again.Options)); res.Code != http.StatusConflict {
		t.Fatalf("registering the same credential twice = %d %s", res.Code, res.Body.String())
	}
	if list := request(t, server, http.MethodGet, "/admin/api/passkeys", "", authed(owner, false)); !strings.Contains(list.Body.String(), `"required":true`) || !strings.Contains(list.Body.String(), `"name":"Bitwarden"`) {
		t.Fatalf("passkeys = %s", list.Body.String())
	}

	// The password alone no longer signs in: it earns one passkey step and no cookie.
	step := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse","remember":true}`, signInHeaders("198.51.100.2"))
	if _, cookie := cookieSession(step); step.Code != http.StatusOK || cookie != nil || strings.Contains(step.Body.String(), "csrf_token") {
		t.Fatalf("password alone once a passkey exists = %d %s", step.Code, step.Body.String())
	}
	pending := decode[challengeResponse](t, step)
	if !pending.Required || pending.Pending == "" || !strings.Contains(string(pending.Options), `"allowCredentials":[{"type":"public-key","id":"`+b64(p.id)+`"`) || !strings.Contains(string(pending.Options), `"userVerification":"required"`) {
		t.Fatalf("pending = %s", step.Body.String())
	}
	assertion := p.get(pending.Options)
	// A pending token is not a one-step challenge.
	if res := finishPasskey(t, server, "challenge_id", pending.Pending, assertion, "198.51.100.2"); res.Code != http.StatusUnauthorized {
		t.Fatalf("pending used as challenge_id = %d", res.Code)
	}
	both := finishPasskey(t, server, "pending", pending.Pending, assertion, "198.51.100.2")
	second, cookie := cookieSession(both)
	if both.Code != http.StatusOK || cookie == nil || !strings.Contains(both.Body.String(), `"remember":true`) {
		t.Fatalf("password+passkey = %d %s", both.Code, both.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(second, false)); !strings.Contains(res.Body.String(), `"method":"password+passkey"`) || !strings.Contains(res.Body.String(), `"remember":true`) {
		t.Fatalf("session = %s", res.Body.String())
	}
	if replay := finishPasskey(t, server, "pending", pending.Pending, assertion, "198.51.100.3"); replay.Code != http.StatusUnauthorized || replay.Body.String() != `{"error":"passkey not accepted"}`+"\n" {
		t.Fatalf("replayed pending = %d %s", replay.Code, replay.Body.String())
	}

	// One step: a discoverable assertion with no username; the provider's verification is the second factor.
	discoverable := passkeyOptions(t, server, "198.51.100.4")
	if !strings.Contains(string(discoverable.Options), `"allowCredentials":[]`) || !strings.Contains(string(discoverable.Options), `"userVerification":"required"`) {
		t.Fatalf("discoverable options = %s", discoverable.Options)
	}
	// A one-step challenge is not a pending token.
	if res := finishPasskey(t, server, "pending", discoverable.ChallengeID, p.get(discoverable.Options), "198.51.100.4"); res.Code != http.StatusUnauthorized {
		t.Fatalf("challenge_id used as pending = %d", res.Code)
	}
	discoverable = passkeyOptions(t, server, "198.51.100.4")
	one := finishPasskey(t, server, "challenge_id", discoverable.ChallengeID, p.get(discoverable.Options), "198.51.100.4", "Cookie", second.cookie)
	third, cookie := cookieSession(one)
	if one.Code != http.StatusOK || cookie == nil || cookie.MaxAge < int((store.AdminRememberMax-time.Minute)/time.Second) || cookie.MaxAge > int(store.AdminRememberMax/time.Second) {
		t.Fatalf("one-step passkey = %d %s cookie=%+v", one.Code, one.Body.String(), cookie)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(third, false)); !strings.Contains(res.Body.String(), `"method":"passkey"`) {
		t.Fatalf("one-step session = %s", res.Body.String())
	}
	// Signing in replaces the session the request carried.
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(second, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("replaced session still valid: %d", res.Code)
	}
	// Synced passkeys always report a zero counter; it is accepted every time.
	var count int64
	var used *time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT sign_count,last_used_at FROM owner_passkey`).Scan(&count, &used); err != nil || count != 0 || used == nil {
		t.Fatalf("passkey after use: count=%d used=%v err=%v", count, used, err)
	}

	// Rename, then remove with the password; removing the last passkey returns to password-only.
	path := "/admin/api/passkeys/" + view.Passkey.ID
	if res := request(t, server, http.MethodPatch, path, `{"name":"  Phone vault  "}`, authed(third, true)); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"Phone vault"`) || !strings.Contains(res.Body.String(), `"provider":"Bitwarden"`) {
		t.Fatalf("rename = %d %s", res.Code, res.Body.String())
	}
	for _, body := range []string{`{"name":""}`, `{"name":"` + strings.Repeat("x", 61) + `"}`, `{"name":"a\nb"}`} {
		if res := request(t, server, http.MethodPatch, path, body, authed(third, true)); res.Code != http.StatusBadRequest {
			t.Fatalf("rename %s = %d", body, res.Code)
		}
	}
	if res := request(t, server, http.MethodPatch, "/admin/api/passkeys/999999", `{"name":"x"}`, authed(third, true)); res.Code != http.StatusNotFound {
		t.Fatalf("rename unknown = %d", res.Code)
	}
	if res := request(t, server, http.MethodDelete, path, `{"password":"wrong"}`, authed(third, true)); res.Code != http.StatusForbidden {
		t.Fatalf("delete with wrong password = %d", res.Code)
	}
	if res := request(t, server, http.MethodDelete, "/admin/api/passkeys/999999", `{"password":"correct horse"}`, authed(third, true)); res.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d", res.Code)
	}
	if res := request(t, server, http.MethodDelete, path, `{"password":"correct horse"}`, authed(third, true)); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse"}`, signInHeaders("198.51.100.5")); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "csrf_token") {
		t.Fatalf("password-only after removing the last passkey = %d %s", res.Code, res.Body.String())
	}
}

func TestPasskeyAssertionsAreRefusedUnlessEverythingMatches(t *testing.T) {
	db, ctx := testdb.Open(t)
	apps, _ := passkey.ParseAndroidApps(passkey.DefaultAndroidApps)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1", apps...)
	_, owner := login(t, server, "correct horse", "")
	p := newSoftPasskey(t)
	registerSoftPasskey(t, server, owner, p)

	address := 10
	// next is a fresh client address, so the per-address lockout does not mask the next case.
	next := func() string { address++; return "203.0.113." + strconv.Itoa(address) }
	refused := func(name string, res *httptest.ResponseRecorder) {
		t.Helper()
		if res.Code != http.StatusUnauthorized || res.Body.String() != `{"error":"passkey not accepted"}`+"\n" {
			t.Errorf("%s = %d %s", name, res.Code, res.Body.String())
		}
		if _, cookie := cookieSession(res); cookie != nil {
			t.Errorf("%s issued a session", name)
		}
	}
	for name, change := range map[string]func(*softPasskey){
		"wrong origin":            func(c *softPasskey) { c.origin = "https://evil.example" },
		"subdomain origin":        func(c *softPasskey) { c.origin = "https://ledger.example.com.evil.example" },
		"unknown Android app":     func(c *softPasskey) { c.origin = "android:apk-key-hash:" + b64(make([]byte, 32)) },
		"wrong RP ID":             func(c *softPasskey) { c.rpID = "evil.example" },
		"missing user verified":   func(c *softPasskey) { c.flags = flagUP | flagBE | flagBS },
		"missing user present":    func(c *softPasskey) { c.flags = flagUV | flagBE | flagBS },
		"other key, same id":      func(c *softPasskey) { c.key = newSoftPasskey(t).key },
		"unregistered credential": func(c *softPasskey) { c.id = newSoftPasskey(t).id },
		"other user handle":       func(c *softPasskey) { c.handle = make([]byte, 32) },
	} {
		ip := next()
		started := passkeyOptions(t, server, ip)
		tampered := *p
		change(&tampered)
		refused(name, finishPasskey(t, server, "challenge_id", started.ChallengeID, tampered.get(started.Options), ip))
	}
	ip := next()
	refused("no credential", request(t, server, http.MethodPost, "/admin/api/login/passkey", `{"challenge_id":"`+passkeyOptions(t, server, ip).ChallengeID+`"}`, signInHeaders(ip)))
	ip = next()
	refused("unknown challenge", finishPasskey(t, server, "challenge_id", strings.Repeat("A", 43), p.get(passkeyOptions(t, server, ip).Options), ip))

	// A challenge works once.
	ip = next()
	started := passkeyOptions(t, server, ip)
	assertion := p.get(started.Options)
	if res := finishPasskey(t, server, "challenge_id", started.ChallengeID, assertion, ip); res.Code != http.StatusOK {
		t.Fatalf("first use = %d %s", res.Code, res.Body.String())
	}
	refused("replayed challenge", finishPasskey(t, server, "challenge_id", started.ChallengeID, assertion, ip))

	// A challenge and a pending token expire after five minutes.
	ip = next()
	started = passkeyOptions(t, server, ip)
	step := decode[challengeResponse](t, request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse"}`, signInHeaders(ip)))
	if _, err := db.Pool.Exec(ctx, `UPDATE owner_webauthn_challenge SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	refused("expired challenge", finishPasskey(t, server, "challenge_id", started.ChallengeID, p.get(started.Options), ip))
	refused("expired pending", finishPasskey(t, server, "pending", step.Pending, p.get(step.Options), ip))
	var lifetime time.Duration
	started = passkeyOptions(t, server, next())
	if err := db.Pool.QueryRow(ctx, `SELECT max(expires_at)-now() FROM owner_webauthn_challenge`).Scan(&lifetime); err != nil || lifetime > store.WebAuthnChallengeTTL || lifetime < store.WebAuthnChallengeTTL-time.Minute {
		t.Fatalf("challenge lifetime = %v, %v", lifetime, err)
	}

	// A non-zero counter must grow; zero, which synced passkeys always send, is always accepted.
	if _, err := db.Pool.Exec(ctx, `UPDATE owner_passkey SET sign_count=10`); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		counter uint32
		status  int
	}{{5, 401}, {10, 401}, {11, 200}, {0, 200}, {11, 401}, {12, 200}} {
		ip := next()
		started := passkeyOptions(t, server, ip)
		counted := *p
		counted.counter = step.counter
		if res := finishPasskey(t, server, "challenge_id", started.ChallengeID, counted.get(started.Options), ip); res.Code != step.status {
			t.Fatalf("counter %d = %d, want %d", step.counter, res.Code, step.status)
		}
	}
	var stored int64
	if err := db.Pool.QueryRow(ctx, `SELECT sign_count FROM owner_passkey`).Scan(&stored); err != nil || stored != 12 {
		t.Fatalf("stored counter = %d, %v", stored, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE owner_passkey SET sign_count=0`); err != nil {
		t.Fatal(err)
	}

	// The Ledger Android app signs with its own origin, derived from its signing certificate.
	ip = next()
	started = passkeyOptions(t, server, ip)
	android := *p
	android.origin = apps[0].Origin()
	if res := finishPasskey(t, server, "challenge_id", started.ChallengeID, android.get(started.Options), ip, "User-Agent", "LedgerAndroid/0.20.0 (Android 15; Pixel 9)"); res.Code != http.StatusOK {
		t.Fatalf("Android app origin = %d %s", res.Code, res.Body.String())
	}

	// Refused passkeys count toward the same lockout as wrong passwords.
	ip = next()
	for i := 0; i < 4; i++ {
		started := passkeyOptions(t, server, ip)
		wrong := *p
		wrong.origin = "https://evil.example"
		refused("lockout attempt", finishPasskey(t, server, "challenge_id", started.ChallengeID, wrong.get(started.Options), ip))
	}
	if res := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse"}`, signInHeaders(ip)); res.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out password = %d", res.Code)
	}
	if res := request(t, server, http.MethodPost, "/admin/api/login/passkey/options", `{}`, signInHeaders(ip)); res.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out passkey options = %d", res.Code)
	}
}

func TestRegistrationChallengeBelongsToItsSession(t *testing.T) {
	db, _ := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, first := login(t, server, "correct horse", "")
	_, other := login(t, server, "correct horse", "")
	p := newSoftPasskey(t)
	started := registrationOptions(t, server, first)
	credential := p.create(started.Options)
	if res := finishRegistration(t, server, other, started.ChallengeID, credential); res.Code != http.StatusBadRequest {
		t.Fatalf("another session finished the registration: %d %s", res.Code, res.Body.String())
	}
	// A registration challenge is no sign-in challenge.
	if res := finishPasskey(t, server, "challenge_id", started.ChallengeID, credential, "198.51.100.30"); res.Code != http.StatusUnauthorized {
		t.Fatalf("registration challenge signed in: %d", res.Code)
	}
	// Missing user verification at registration is refused.
	unverified := newSoftPasskey(t)
	unverified.flags = flagUP
	again := registrationOptions(t, server, first)
	if res := finishRegistration(t, server, first, again.ChallengeID, unverified.create(again.Options)); res.Code != http.StatusBadRequest {
		t.Fatalf("unverified registration = %d %s", res.Code, res.Body.String())
	}
	res := finishRegistration(t, server, first, started.ChallengeID, credential)
	if res.Code != http.StatusCreated {
		t.Fatalf("owning session = %d %s", res.Code, res.Body.String())
	}
	first, _ = cookieSession(res)
	// Signing out deletes the session's unfinished registrations with it.
	registrationOptions(t, server, first)
	if res := request(t, server, http.MethodPost, "/admin/api/logout", "", authed(first, true)); res.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", res.Code)
	}
	var left int
	if err := db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM owner_webauthn_challenge WHERE purpose='register'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("registration challenges after logout = %d, %v", left, err)
	}
}

// A session opened with the password alone, before the first passkey existed, ends when a passkey
// is added. Otherwise it would outlive the switch (90 days when remembered) and, with the password,
// could add its own passkey or remove the owner's.
func TestAddingAPasskeySignsOutPasswordOnlySessions(t *testing.T) {
	db, _ := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	_, owner := login(t, server, "correct horse", "")
	stale, _ := cookieSession(request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse","remember":true}`, signInHeaders("198.51.100.60")))
	p := newSoftPasskey(t)
	started := registrationOptions(t, server, owner)
	added := finishRegistration(t, server, owner, started.ChallengeID, p.create(started.Options))
	if added.Code != http.StatusCreated || !strings.Contains(added.Body.String(), `"revoked":1`) {
		t.Fatalf("first passkey = %d %s", added.Code, added.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/passkeys/options", `{"password":"correct horse"}`, authed(stale, true)); res.Code != http.StatusUnauthorized {
		t.Fatalf("password-only session after the first passkey = %d %s", res.Code, res.Body.String())
	}
	// The session that added the passkey stays signed in, under a new cookie and CSRF token: a copy
	// of its old cookie, taken while it had only proved the password, stops working.
	rotated, cookie := cookieSession(added)
	if cookie == nil || rotated.csrf == "" || rotated.cookie == owner.cookie || rotated.csrf == owner.csrf {
		t.Fatalf("registering session not rotated: %s", added.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(owner, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie of the registering session = %d", res.Code)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(rotated, false)); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"method":"password+passkey"`) {
		t.Fatalf("registering session = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodPost, "/admin/api/passkeys/options", `{"password":"correct horse"}`, authed(owner, true)); res.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie can still add a passkey: %d", res.Code)
	}
	owner = rotated
	// A session that signed in with a passkey is not signed out by the next one.
	ip := "198.51.100.61"
	discoverable := passkeyOptions(t, server, ip)
	withPasskey, _ := cookieSession(finishPasskey(t, server, "challenge_id", discoverable.ChallengeID, p.get(discoverable.Options), ip))
	started = registrationOptions(t, server, owner)
	if res := finishRegistration(t, server, owner, started.ChallengeID, newSoftPasskey(t).create(started.Options)); res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"revoked":0`) {
		t.Fatalf("second passkey = %d %s", res.Code, res.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/session", "", authed(withPasskey, false)); res.Code != http.StatusOK {
		t.Fatalf("passkey session after another passkey = %d", res.Code)
	}
}

func TestSignedInDevicesCanBeListedAndSignedOut(t *testing.T) {
	db, _ := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	signIn := func(body, userAgent string) session {
		t.Helper()
		res := request(t, server, http.MethodPost, "/admin/api/login", body, signInHeaders("198.51.100.40", "User-Agent", userAgent))
		s, cookie := cookieSession(res)
		if cookie == nil {
			t.Fatalf("sign-in = %d %s", res.Code, res.Body.String())
		}
		return s
	}
	desk := signIn(`{"password":"correct horse"}`, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36")
	phone := signIn(`{"password":"correct horse","remember":true}`, "LedgerAndroid/0.20.0 (Android 15; Pixel 9)")
	laptop := signIn(`{"password":"correct horse"}`, "Mozilla/5.0 (X11; Linux x86_64; rv:143.0) Gecko/20100101 Firefox/143.0")

	res := request(t, server, http.MethodGet, "/admin/api/sessions", "", authed(desk, false))
	listed := decode[struct {
		Sessions []sessionView `json:"sessions"`
	}](t, res).Sessions
	byLabel := map[string]sessionView{}
	for _, s := range listed {
		byLabel[s.Label] = s
	}
	current := byLabel["Chrome on Windows"]
	if len(listed) != 3 || !current.Current || current.Remember || current.Method != "password" || byLabel["Firefox on Linux"].Current ||
		!byLabel["Ledger app on Android"].Remember || byLabel["Ledger app on Android"].Current || current.LastSeenAt.IsZero() || current.CreatedAt.IsZero() {
		t.Fatalf("sessions = %s", res.Body.String())
	}
	for _, secret := range []string{desk.csrf, phone.csrf, strings.TrimPrefix(phone.cookie, sessionCookie+"=")} {
		if strings.Contains(res.Body.String(), secret) {
			t.Fatal("session list exposes session secrets")
		}
	}
	if status := request(t, server, http.MethodGet, "/admin/api/session", "", authed(desk, false)); !strings.Contains(status.Body.String(), `"session_id":"`+current.ID+`"`) {
		t.Fatalf("session id = %s", status.Body.String())
	}

	// Signing a device out takes effect on its next request.
	if res := request(t, server, http.MethodDelete, "/admin/api/sessions/"+byLabel["Ledger app on Android"].ID, "", authed(desk, true)); res.Code != http.StatusNoContent || len(res.Result().Cookies()) != 0 {
		t.Fatalf("sign out phone = %d %v", res.Code, res.Result().Cookies())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(phone, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("revoked phone = %d", res.Code)
	}
	for _, id := range []string{"00000000-0000-4000-8000-000000000000", "not-a-session"} {
		if res := request(t, server, http.MethodDelete, "/admin/api/sessions/"+id, "", authed(desk, true)); res.Code != http.StatusNotFound {
			t.Fatalf("sign out %s = %d", id, res.Code)
		}
	}
	if res := request(t, server, http.MethodDelete, "/admin/api/sessions/"+byLabel["Firefox on Linux"].ID, "", authed(desk, false)); res.Code != http.StatusForbidden {
		t.Fatalf("sign out without CSRF = %d", res.Code)
	}

	// An expired session is not listed and not counted as signed out.
	signIn(`{"password":"correct horse"}`, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15")
	if _, err := db.Pool.Exec(t.Context(), `UPDATE admin_session SET expires_at=now()-interval '1 second' WHERE label='Safari on macOS'`); err != nil {
		t.Fatal(err)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/sessions", "", authed(desk, false)); strings.Contains(res.Body.String(), "Safari on macOS") {
		t.Fatalf("expired session listed: %s", res.Body.String())
	}
	others := request(t, server, http.MethodPost, "/admin/api/sessions/revoke-others", "", authed(desk, true))
	if others.Code != http.StatusOK || others.Body.String() != `{"revoked":1}`+"\n" {
		t.Fatalf("revoke others = %d %s", others.Code, others.Body.String())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(laptop, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("laptop after revoke-others = %d", res.Code)
	}
	if res := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(desk, false)); res.Code != http.StatusOK {
		t.Fatalf("current session after revoke-others = %d", res.Code)
	}

	// Signing out the current device is signing out: the cookie is cleared.
	out := request(t, server, http.MethodDelete, "/admin/api/sessions/"+current.ID, "", authed(desk, true))
	cleared := false
	for _, cookie := range out.Result().Cookies() {
		cleared = cleared || (cookie.Name == sessionCookie && cookie.Value == "" && cookie.MaxAge < 0)
	}
	if out.Code != http.StatusNoContent || !cleared {
		t.Fatalf("sign out current = %d %v", out.Code, out.Result().Cookies())
	}
	if res := request(t, server, http.MethodGet, "/admin/api/overview", "", authed(desk, false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("current session after signing itself out = %d", res.Code)
	}
}

func TestRememberedSessionsSlideButNeverPassNinetyDays(t *testing.T) {
	db, ctx := testdb.Open(t)
	server := newIntegrationServer(t, db, "http://127.0.0.1:1")
	signIn := func(remember bool) (session, *http.Cookie, string) {
		t.Helper()
		res := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"correct horse","remember":`+strconv.FormatBool(remember)+`}`, signInHeaders("198.51.100.50"))
		s, cookie := cookieSession(res)
		if cookie == nil {
			t.Fatalf("sign-in = %d %s", res.Code, res.Body.String())
		}
		status := decode[struct {
			ID string `json:"session_id"`
		}](t, request(t, server, http.MethodGet, "/admin/api/session", "", authed(s, false)))
		return s, cookie, status.ID
	}
	expiresIn := func(id string) time.Duration {
		t.Helper()
		var left time.Duration
		if err := db.Pool.QueryRow(ctx, `SELECT expires_at-now() FROM admin_session WHERE public_id::text=$1`, id).Scan(&left); err != nil {
			t.Fatal(err)
		}
		return left
	}
	set := func(id, assignments string) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `UPDATE admin_session SET `+assignments+` WHERE public_id::text=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	use := func(s session) int {
		return request(t, server, http.MethodGet, "/admin/api/overview", "", authed(s, false)).Code
	}

	short, cookie, shortID := signIn(false)
	if cookie.MaxAge > int(store.AdminSessionTTL/time.Second) || cookie.MaxAge < int((store.AdminSessionTTL-time.Minute)/time.Second) {
		t.Fatalf("12-hour cookie Max-Age = %d", cookie.MaxAge)
	}
	// A session that is not remembered never slides and never outlives 12 hours.
	set(shortID, `created_at=now()-interval '11 hours', expires_at=now()+interval '30 minutes'`)
	if use(short) != http.StatusOK || expiresIn(shortID) > 31*time.Minute {
		t.Fatalf("short session slid: %v", expiresIn(shortID))
	}
	set(shortID, `created_at=now()-interval '13 hours', expires_at=now()+interval '1 hour'`)
	if code := use(short); code != http.StatusUnauthorized {
		t.Fatalf("short session past 12 hours = %d", code)
	}

	long, cookie, longID := signIn(true)
	if cookie.MaxAge > int(store.AdminRememberMax/time.Second) || cookie.MaxAge < int((store.AdminRememberMax-time.Minute)/time.Second) {
		t.Fatalf("remembered cookie Max-Age = %d, want the 90-day cap", cookie.MaxAge)
	}
	if left := expiresIn(longID); left > store.AdminRememberIdle || left < store.AdminRememberIdle-time.Minute {
		t.Fatalf("remembered idle expiry = %v", left)
	}
	// Use slides the idle expiry back to 30 days...
	set(longID, `created_at=now()-interval '29 days', expires_at=now()+interval '1 day'`)
	if use(long) != http.StatusOK || expiresIn(longID) < store.AdminRememberIdle-time.Minute {
		t.Fatalf("remembered session did not slide: %v", expiresIn(longID))
	}
	// ...at most about once an hour...
	set(longID, `expires_at=now()+interval '30 days'-interval '30 minutes'`)
	if use(long) != http.StatusOK || expiresIn(longID) > store.AdminRememberIdle-29*time.Minute {
		t.Fatalf("remembered session slid within the hour: %v", expiresIn(longID))
	}
	// ...but never past 90 days from sign-in.
	set(longID, `created_at=now()-interval '90 days'+interval '2 hours', expires_at=now()+interval '30 minutes'`)
	if left := func() time.Duration { use(long); return expiresIn(longID) }(); left > 2*time.Hour || left < 2*time.Hour-time.Minute {
		t.Fatalf("slide past the 90-day cap: %v", left)
	}
	set(longID, `created_at=now()-interval '90 days'-interval '1 minute', expires_at=now()+interval '1 hour'`)
	if code := use(long); code != http.StatusUnauthorized {
		t.Fatalf("remembered session past 90 days = %d", code)
	}
	// Thirty days without use ends it.
	idle, _, idleID := signIn(true)
	set(idleID, `expires_at=now()-interval '1 second'`)
	if code := use(idle); code != http.StatusUnauthorized {
		t.Fatalf("idle remembered session = %d", code)
	}
}
