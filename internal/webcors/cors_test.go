package webcors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowExactPreflight(t *testing.T) {
	called := false
	handler := AllowExact(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}), "/mcp")

	req := httptest.NewRequest(http.MethodOptions, "https://ledger.example.com/mcp", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if called {
		t.Fatal("preflight reached the protected handler")
	}
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("allow headers missing")
	}
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("credentialed CORS unexpectedly enabled: %q", got)
	}
}

func TestAllowExactAddsHeadersOnlyToSelectedPaths(t *testing.T) {
	handler := AllowExact(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "/oauth/register", "/oauth/device", "/oauth/token", "/oauth/revoke")

	for _, tc := range []struct {
		path string
		cors bool
	}{
		{path: "/oauth/register", cors: true},
		{path: "/oauth/device", cors: true},
		{path: "/oauth/token", cors: true},
		{path: "/oauth/revoke", cors: true},
		{path: "/oauth/authorize", cors: false},
		{path: "/admin/api/session", cors: false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://ledger.example.com"+tc.path, nil))
			got := res.Header().Get("Access-Control-Allow-Origin") != ""
			if got != tc.cors {
				t.Fatalf("CORS present = %v, want %v", got, tc.cors)
			}
		})
	}
}
