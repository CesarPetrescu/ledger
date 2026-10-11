package passkey

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func TestDefaultAndroidAppIsTheReleaseApp(t *testing.T) {
	apps, err := ParseAndroidApps(DefaultAndroidApps)
	if err != nil || len(apps) != 1 || apps[0].Package != "com.cesarpetrescu.ledger" {
		t.Fatalf("default apps = %+v, %v", apps, err)
	}
	if got := apps[0].Fingerprint(); got != "8F:F3:37:CD:AC:E0:F1:65:6C:7E:4F:6F:24:BA:65:2B:27:E4:8B:A1:1F:49:D2:44:59:05:B7:2A:04:50:60:91" {
		t.Fatalf("fingerprint = %s", got)
	}
	// base64url without padding of the certificate's SHA-256, as Credential Manager reports it.
	if got := apps[0].Origin(); got != "android:apk-key-hash:j_M3zazg8WVsfk9vJLplKyfki6EfSdJEWQW3KgRQYJE" {
		t.Fatalf("origin = %s", got)
	}
}

func TestParseAndroidAppsAcceptsSeveralAndKeytoolFingerprints(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	colons := strings.ToUpper(strings.TrimSuffix(strings.Repeat("ab:", 32), ":"))
	apps, err := ParseAndroidApps(" com.example.one:" + digest + " , com.example.two:" + colons + ",")
	if err != nil || len(apps) != 2 || apps[1].Package != "com.example.two" || hex.EncodeToString(apps[1].CertSHA256) != digest {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	if apps, err := ParseAndroidApps(""); err != nil || len(apps) != 0 {
		t.Fatalf("empty = %+v, %v", apps, err)
	}
	for _, bad := range []string{"com.example", "noDots:" + digest, "com.example.app:" + digest[:62], "com.example.app:" + strings.Repeat("zz", 32), "com.example.app", "1com.example:" + digest} {
		if _, err := ParseAndroidApps(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestAssetLinksDelegateLinksAndSignInCredentials(t *testing.T) {
	apps, _ := ParseAndroidApps(DefaultAndroidApps)
	var statements []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			PackageName  string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.Unmarshal(AssetLinks(apps), &statements); err != nil || len(statements) != 1 {
		t.Fatalf("asset links = %s, %v", AssetLinks(apps), err)
	}
	got := statements[0]
	if strings.Join(got.Relation, ",") != "delegate_permission/common.handle_all_urls,delegate_permission/common.get_login_creds" ||
		got.Target.Namespace != "android_app" || got.Target.PackageName != "com.cesarpetrescu.ledger" ||
		len(got.Target.Fingerprints) != 1 || got.Target.Fingerprints[0] != apps[0].Fingerprint() {
		t.Fatalf("statement = %+v", got)
	}
	if string(AssetLinks(nil)) != "[]" {
		t.Fatalf("no apps = %s", AssetLinks(nil))
	}
}

func TestRelyingPartyNeedsADomainAndRequiresUserVerification(t *testing.T) {
	apps, _ := ParseAndroidApps(DefaultAndroidApps)
	rp, err := New("https://ledger.example.com", apps)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Config.RPID != "ledger.example.com" || rp.Config.RPOrigins[0] != "https://ledger.example.com" || rp.Config.RPOpaqueOrigins[0] != apps[0].Origin() {
		t.Fatalf("config = %+v", rp.Config)
	}
	assertion, _, err := rp.BeginDiscoverableLogin()
	if err != nil {
		t.Fatal(err)
	}
	options, err := RequestOptions(assertion)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(options, &parsed)
	if parsed["userVerification"] != "required" || parsed["rpId"] != "ledger.example.com" || parsed["allowCredentials"] == nil || len(parsed["allowCredentials"].([]any)) != 0 || parsed["challenge"] == "" {
		t.Fatalf("discoverable options = %s", options)
	}
	creation, _, err := rp.BeginRegistration(Owner{Handle: make([]byte, 32)}, webauthn.WithCredentialParameters(Algorithms()))
	if err != nil {
		t.Fatal(err)
	}
	selection := creation.Response.AuthenticatorSelection
	if selection.ResidentKey != protocol.ResidentKeyRequirementRequired || selection.UserVerification != protocol.VerificationRequired || creation.Response.Attestation != protocol.PreferNoAttestation ||
		creation.Response.User.Name != "owner" || creation.Response.User.DisplayName != "Ledger owner" || creation.Response.RelyingParty.Name != "Ledger" || len(creation.Response.Parameters) != 3 {
		t.Fatalf("creation options = %+v", creation.Response)
	}
	for _, origin := range []string{"https://192.0.2.10", "https://[2001:db8::1]:8443"} {
		if _, err := New(origin, nil); err == nil {
			t.Errorf("%s accepted as an RP ID", origin)
		}
	}
	// Local development and non-default ports keep passkeys, Android apps included.
	for origin, rpID := range map[string]string{"http://localhost:8080": "localhost", "https://ledger.example.com:8443": "ledger.example.com"} {
		if rp, err := New(origin, apps); err != nil || rp.Config.RPID != rpID {
			t.Errorf("%s: %v", origin, err)
		}
	}
}

func TestProvidersAreNamedByAAGUID(t *testing.T) {
	bitwarden, _ := hex.DecodeString("d548826e79b4db40a3d811116f7e8349")
	unknown := make([]byte, 16)
	if Provider(bitwarden) != "Bitwarden" || DefaultName(bitwarden) != "Bitwarden" {
		t.Fatalf("bitwarden = %q", Provider(bitwarden))
	}
	if Provider(unknown) != "" || DefaultName(unknown) != "Passkey" || DefaultName(nil) != "Passkey" {
		t.Fatalf("unknown = %q %q", Provider(unknown), DefaultName(unknown))
	}
	if got := Transports([]protocol.AuthenticatorTransport{"internal", "hybrid", "carrier-pigeon", "internal"}); strings.Join(got, ",") != "internal,hybrid" {
		t.Fatalf("transports = %v", got)
	}
}
