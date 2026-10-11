// Package passkey configures the owner's WebAuthn relying party on top of
// github.com/go-webauthn/webauthn, which does every signature, CBOR, origin,
// RP ID, and flag check. Ledger has one user, the owner, so this package also
// adapts the stored passkeys to the library's user, names passkeys after their
// provider, and serves the Digital Asset Links that let the Android app use
// passkeys for this domain.
package passkey

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// DefaultAndroidApps is LEDGER_ANDROID_APPS when unset: the release Ledger app and its signing certificate.
const DefaultAndroidApps = "com.cesarpetrescu.ledger:8ff337cdace0f1656c7e4f6f24ba652b27e48ba11f49d2445905b72a04506091"

// AndroidApp is an app allowed to use the owner's passkeys: its package and the SHA-256 of its signing certificate.
type AndroidApp struct {
	Package    string
	CertSHA256 []byte
}

var androidPackage = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)

// ParseAndroidApps reads comma-separated "package:SHA256HEX" entries. The fingerprint may also be
// written with colons, as keytool prints it.
func ParseAndroidApps(value string) ([]AndroidApp, error) {
	apps := []AndroidApp{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, fingerprint, _ := strings.Cut(entry, ":")
		cert, err := hex.DecodeString(strings.ReplaceAll(fingerprint, ":", ""))
		if !androidPackage.MatchString(name) || err != nil || len(cert) != 32 {
			return nil, fmt.Errorf("LEDGER_ANDROID_APPS entry %q must be package:SHA256 of the signing certificate", entry)
		}
		apps = append(apps, AndroidApp{Package: name, CertSHA256: cert})
	}
	return apps, nil
}

// Origin is the WebAuthn origin Android's Credential Manager reports for the app.
func (a AndroidApp) Origin() string {
	return "android:apk-key-hash:" + base64.RawURLEncoding.EncodeToString(a.CertSHA256)
}

// Fingerprint is the certificate digest as Digital Asset Links write it: colon-separated uppercase hex.
func (a AndroidApp) Fingerprint() string {
	digits := strings.ToUpper(hex.EncodeToString(a.CertSHA256))
	pairs := make([]string, 0, len(a.CertSHA256))
	for i := 0; i < len(digits); i += 2 {
		pairs = append(pairs, digits[i:i+2])
	}
	return strings.Join(pairs, ":")
}

// AssetLinks is /.well-known/assetlinks.json: each app may open this site's links and use its sign-in
// credentials, which is what lets Credential Manager offer the owner's passkeys inside the app.
func AssetLinks(apps []AndroidApp) []byte {
	type target struct {
		Namespace    string   `json:"namespace"`
		PackageName  string   `json:"package_name"`
		Fingerprints []string `json:"sha256_cert_fingerprints"`
	}
	type statement struct {
		Relation []string `json:"relation"`
		Target   target   `json:"target"`
	}
	statements := make([]statement, 0, len(apps))
	for _, app := range apps {
		statements = append(statements, statement{
			Relation: []string{"delegate_permission/common.handle_all_urls", "delegate_permission/common.get_login_creds"},
			Target:   target{Namespace: "android_app", PackageName: app.Package, Fingerprints: []string{app.Fingerprint()}},
		})
	}
	body, _ := json.Marshal(statements)
	return body
}

// New builds the relying party for the console's exact public origin (scheme://host[:port]). The RP ID
// is its host, which WebAuthn requires to be a domain name, not an IP address.
func New(origin string, apps []AndroidApp) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("LEDGER_PUBLIC_URL must be an absolute URL")
	}
	opaque := make([]string, 0, len(apps))
	for _, app := range apps {
		opaque = append(opaque, app.Origin())
	}
	ceremony := webauthn.TimeoutConfig{Enforce: true, Timeout: store.WebAuthnChallengeTTL, TimeoutUVD: store.WebAuthnChallengeTTL}
	return webauthn.New(&webauthn.Config{
		RPID:            u.Hostname(),
		RPDisplayName:   "Ledger",
		RPOrigins:       []string{origin},
		RPOpaqueOrigins: opaque,
		// Discoverable credentials with user verification: the provider's PIN or biometric is the second factor.
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts:              webauthn.TimeoutsConfig{Login: ceremony, Registration: ceremony},
		// Ledger requests no extensions and reads none, so an output a provider volunteers (some always
		// send credProps) is ignored rather than failing the ceremony.
		ExtensionsUnsolicitedOutputPolicy: protocol.UnsolicitedOutputPolicyIgnore,
	})
}

// Algorithms are the credential algorithms Ledger accepts: EdDSA, ES256, and RS256.
func Algorithms() []protocol.CredentialParameter { return webauthn.CredentialParametersRecommendedL3() }

// Owner is the console's only WebAuthn user.
type Owner struct {
	Handle   []byte
	Passkeys []store.OwnerPasskey
}

func (o Owner) WebAuthnID() []byte          { return o.Handle }
func (o Owner) WebAuthnName() string        { return "owner" }
func (o Owner) WebAuthnDisplayName() string { return "Ledger owner" }

func (o Owner) WebAuthnCredentials() []webauthn.Credential {
	credentials := make([]webauthn.Credential, 0, len(o.Passkeys))
	for _, p := range o.Passkeys {
		transports := make([]protocol.AuthenticatorTransport, 0, len(p.Transports))
		for _, transport := range p.Transports {
			transports = append(transports, protocol.AuthenticatorTransport(transport))
		}
		credentials = append(credentials, webauthn.Credential{
			ID: p.CredentialID, PublicKey: p.PublicKey, AttestationFormat: p.AttestationFormat, Transport: transports,
			Flags:         webauthn.CredentialFlags{UserPresent: true, UserVerified: true, BackupEligible: p.BackupEligible, BackupState: p.BackupState},
			Authenticator: webauthn.Authenticator{AAGUID: p.AAGUID, SignCount: p.SignCount},
		})
	}
	return credentials
}

// Exclusions lists the registered passkeys so a provider does not register a second one for the owner.
func (o Owner) Exclusions() []protocol.CredentialDescriptor {
	credentials := o.WebAuthnCredentials()
	out := make([]protocol.CredentialDescriptor, len(credentials))
	for i, credential := range credentials {
		out[i] = credential.Descriptor()
	}
	return out
}

// Transports keeps the transports WebAuthn defines, dropping anything else a client sent.
func Transports(in []protocol.AuthenticatorTransport) []string {
	known := []protocol.AuthenticatorTransport{protocol.USB, protocol.NFC, protocol.BLE, protocol.SmartCard, protocol.Hybrid, protocol.Internal}
	out := []string{}
	for _, transport := range in {
		if slices.Contains(known, transport) && !slices.Contains(out, string(transport)) {
			out = append(out, string(transport))
		}
	}
	return out
}

// RequestOptions is the PublicKeyCredentialRequestOptionsJSON of an assertion. A discoverable sign-in
// lists allowCredentials as an explicit empty array, which every client reads as "any passkey".
func RequestOptions(assertion *protocol.CredentialAssertion) (json.RawMessage, error) {
	body, err := json.Marshal(assertion.Response)
	if err != nil || len(assertion.Response.AllowedCredentials) != 0 {
		return body, err
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(body, &options); err != nil {
		return nil, err
	}
	options["allowCredentials"] = json.RawMessage(`[]`)
	return json.Marshal(options)
}

// providers names the passkey providers the owner is likely to use, by authenticator AAGUID.
var providers = map[string]string{
	"d548826e-79b4-db40-a3d8-11116f7e8349": "Bitwarden",
	"fbfc3007-154e-4ecc-8c0b-6e020557d7bd": "iCloud Keychain",
	"ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4": "Google Password Manager",
	"08987058-cadc-4b81-b6e1-30de50dcbe96": "Windows Hello",
	"9ddd1817-af5a-4672-a2b9-3e3dd95000a9": "Windows Hello",
	"6028b017-b1d4-4c02-b4b3-afcdafc96bb2": "Windows Hello",
	"bada5566-a7aa-401f-bd96-45619a55120d": "1Password",
}

// Provider names a passkey's provider from its AAGUID, or "" when Ledger does not know it.
func Provider(aaguid []byte) string {
	if len(aaguid) != 16 {
		return ""
	}
	return providers[fmt.Sprintf("%x-%x-%x-%x-%x", aaguid[0:4], aaguid[4:6], aaguid[6:8], aaguid[8:10], aaguid[10:16])]
}

// DefaultName is a new passkey's name until the owner renames it.
func DefaultName(aaguid []byte) string {
	if provider := Provider(aaguid); provider != "" {
		return provider
	}
	return "Passkey"
}
