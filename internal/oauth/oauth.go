package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strings"
)

const (
	ScopeRead          = "ledger:read"
	ScopeWrite         = "ledger:write"
	ScopeCalendarRead  = "calendar:read"
	ScopeCalendarWrite = "calendar:write"
	// ScopeResearchDispatch lets a dispatcher claim research tasks and hand each run its own token. It
	// grants nothing on /mcp, and nothing asks for it by default.
	ScopeResearchDispatch = "research:dispatch"
)

// MCPScopes are the scopes /mcp understands and the only ones advertised. SupportedScopes are all the
// scopes this server issues: research:dispatch is granted only to a client that asks for it by name.
var (
	MCPScopes       = []string{ScopeRead, ScopeWrite, ScopeCalendarRead, ScopeCalendarWrite}
	SupportedScopes = []string{ScopeRead, ScopeWrite, ScopeCalendarRead, ScopeCalendarWrite, ScopeResearchDispatch}
)

func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ValidVerifier(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func VerifyPKCE(verifier, challenge string) bool {
	if !ValidVerifier(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(PKCEChallenge(verifier)), []byte(challenge)) == 1
}

func ValidRedirectURI(candidate string) bool {
	u, err := url.Parse(candidate)
	if err != nil || strings.Contains(candidate, "#") || u.Fragment != "" || u.User != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || PrivateNetworkHost(host)
}

// PrivateNetworkHost reports whether host names a machine on a private network, where a plain-http
// redirect never crosses the internet: an exact private IP (RFC 1918, IPv6 unique-local) or a name under
// a suffix reserved for local networks. Anything a public resolver could answer for (single-label names,
// wildcard-DNS services such as nip.io) is refused. PKCE still binds the code to the client that started
// the login, and the owner still approves every client.
func PrivateNetworkHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.IsPrivate()
	}
	// Only plain ASCII names under a suffix reserved for local networks, so no public DNS can answer
	// for them and no browser rewrites them (IDNA, percent-escapes, numeric IPv4 forms) into another host.
	if strings.Trim(host, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "" {
		return false
	}
	for _, suffix := range []string{".local", ".lan", ".home.arpa", ".internal"} {
		if name, ok := strings.CutSuffix(host, suffix); ok && name != "" && !strings.HasSuffix(name, ".") {
			return true
		}
	}
	return false
}

func RedirectMatches(candidate string, registered []string) bool {
	if !ValidRedirectURI(candidate) {
		return false
	}
	candidateURL, _ := url.Parse(candidate)
	for _, allowed := range registered {
		if candidate == allowed {
			return true
		}
		allowedURL, err := url.Parse(allowed)
		if err == nil && candidateURL.Scheme == "http" && allowedURL.Scheme == "http" &&
			candidateURL.Hostname() == allowedURL.Hostname() &&
			(candidateURL.Hostname() == "localhost" || candidateURL.Hostname() == "127.0.0.1" || candidateURL.Hostname() == "::1") &&
			candidateURL.EscapedPath() == allowedURL.EscapedPath() && candidateURL.RawQuery == allowedURL.RawQuery &&
			candidateURL.ForceQuery == allowedURL.ForceQuery {
			return true
		}
	}
	return false
}

func ParseScopes(raw string) ([]string, bool) {
	if strings.TrimSpace(raw) == "" {
		return []string{ScopeRead}, true
	}
	seen := map[string]bool{}
	for _, scope := range strings.Fields(raw) {
		if !slices.Contains(SupportedScopes, scope) {
			return nil, false
		}
		seen[scope] = true
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out, true
}

func HasScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}
