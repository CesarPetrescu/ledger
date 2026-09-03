package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"
)

func TestOAuthMetadataIsExactAndCacheable(t *testing.T) {
	server := NewServer(Config{PublicURL: "https://ledger.example.com"}, nil)
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "max-age=3600" {
			t.Fatalf("%s status/cache = %d, %q", path, res.Code, res.Header().Get("Cache-Control"))
		}
		var got map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &got)
		want := map[string]any{
			"resource":                 "https://ledger.example.com/mcp",
			"authorization_servers":    []any{"https://ledger.example.com"},
			"scopes_supported":         []any{"ledger:read", "ledger:write"},
			"bearer_methods_supported": []any{"header"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s metadata = %#v, want %#v", path, got, want)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	var got map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	want := map[string]any{
		"issuer":                                         "https://ledger.example.com",
		"authorization_endpoint":                         "https://ledger.example.com/oauth/authorize",
		"token_endpoint":                                 "https://ledger.example.com/oauth/token",
		"registration_endpoint":                          "https://ledger.example.com/oauth/register",
		"response_types_supported":                       []any{"code"},
		"grant_types_supported":                          []any{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []any{"S256"},
		"token_endpoint_auth_methods_supported":          []any{"none"},
		"scopes_supported":                               []any{"ledger:read", "ledger:write"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("authorization metadata = %#v, want %#v", got, want)
	}
}

func TestCIMDFetchAddressPolicy(t *testing.T) {
	for _, raw := range []string{
		"0.0.0.1", "127.0.0.1", "10.0.0.1", "169.254.169.254", "192.0.2.1", "192.88.99.1", "198.18.0.1", "224.0.0.1",
		"::1", "64:ff9b::1", "fc00::1", "fec0::1", "fe80::1", "2001:db8::1", "2002::1", "3fff::1", "4000::1", "5f00::1",
	} {
		if publicCIMDIP(netip.MustParseAddr(raw)) {
			t.Errorf("reserved CIMD destination accepted: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicCIMDIP(netip.MustParseAddr(raw)) {
			t.Errorf("public CIMD destination rejected: %s", raw)
		}
	}
}
