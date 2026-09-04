package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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
)

var SupportedScopes = []string{ScopeRead, ScopeWrite, ScopeCalendarRead, ScopeCalendarWrite}

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
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
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
