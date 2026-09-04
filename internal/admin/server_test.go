package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/oauth"
)

const testPublicURL = "https://ledger.example.com"

func newTestServer(t *testing.T, password string) *Server {
	t.Helper()
	hash, err := oauth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(Config{PublicURL: testPublicURL + "/", PasswordHash: hash, InternalProxyCIDR: "172.31.255.2/32", IndexURL: "http://127.0.0.1:1"}, nil)
}

func request(t *testing.T, server http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "172.31.255.2:4000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	return res
}

func assertSecurityHeaders(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	for header, want := range map[string]string{
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	} {
		if got := res.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestEveryEndpointExceptLoginDeniesUnauthenticatedRequests(t *testing.T) {
	server := newTestServer(t, "correct horse")
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/admin/api/session", ""},
		{http.MethodPost, "/admin/api/logout", ""},
		{http.MethodGet, "/admin/api/overview", ""},
		{http.MethodGet, "/admin/api/projects", ""},
		{http.MethodGet, "/admin/api/projects/atlas", ""},
		{http.MethodPut, "/admin/api/projects/atlas", `{"name":"Atlas","tier":"focus"}`},
		{http.MethodPost, "/admin/api/projects/atlas/entries", `{"kind":"note","body":"x"}`},
		{http.MethodPost, "/admin/api/search", `{"q":"x"}`},
		{http.MethodGet, "/admin/api/calendar/connection", ""},
		{http.MethodPost, "/admin/api/calendar/connect", `{"server_url":"https://cloud.example.com"}`},
		{http.MethodPost, "/admin/api/calendar/connect/flow/poll", ""},
		{http.MethodDelete, "/admin/api/calendar/connection", ""},
		{http.MethodGet, "/admin/api/calendar/calendars", ""},
		{http.MethodPut, "/admin/api/calendar/calendars", `{"ids":[]}`},
		{http.MethodGet, "/admin/api/calendar/events?start=2026-09-01T00:00:00Z&end=2026-10-01T00:00:00Z", ""},
		{http.MethodPost, "/admin/api/calendar/events", `{}`},
		{http.MethodGet, "/admin/api/calendar/events/event", ""},
		{http.MethodPut, "/admin/api/calendar/events/event", `{}`},
		{http.MethodDelete, "/admin/api/calendar/events/event", `{}`},
		{http.MethodGet, "/admin/api/oauth/clients", ""},
		{http.MethodPost, "/admin/api/oauth/revoke", `{"client_id":"x"}`},
		{http.MethodGet, "/admin/api/events", ""},
		{http.MethodGet, "/admin/api/unknown", ""},
		{http.MethodDelete, "/admin/api/projects/atlas", ""},
	}
	for _, route := range routes {
		headers := map[string]string{"Origin": testPublicURL, "X-CSRF-Token": "guess", "Cookie": "ledger_admin_session=forged"}
		res := request(t, server, route.method, route.path, route.body, headers)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", route.method, route.path, res.Code)
		}
		assertSecurityHeaders(t, res)
		var body map[string]string
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body["error"] == "" {
			t.Errorf("%s %s body = %q", route.method, route.path, res.Body.String())
		}
		for _, cookie := range res.Result().Cookies() {
			if cookie.Name == sessionCookie && cookie.Value != "" {
				t.Errorf("%s %s issued a session cookie without authentication", route.method, route.path)
			}
		}
	}
}

func TestEventStreamCollapsesPendingInvalidations(t *testing.T) {
	stream := newEventStream(nil)
	updates, unsubscribe := stream.subscribe()
	defer unsubscribe()
	stream.broadcast()
	stream.broadcast()
	select {
	case <-updates:
	default:
		t.Fatal("expected a pending event")
	}
	select {
	case <-updates:
		t.Fatal("unexpected second pending event")
	default:
	}
}

func TestLoginRequiresExactOriginAndWellFormedJSON(t *testing.T) {
	server := newTestServer(t, "correct horse")
	valid := `{"password":"correct horse"}`
	for name, test := range map[string]struct {
		origin, body string
		status       int
	}{
		"missing origin":          {"", valid, http.StatusForbidden},
		"foreign origin":          {"https://evil.example", valid, http.StatusForbidden},
		"subdomain origin":        {"https://ledger.example.com.evil.example", valid, http.StatusForbidden},
		"scheme downgrade":        {"http://ledger.example.com", valid, http.StatusForbidden},
		"null origin":             {"null", valid, http.StatusForbidden},
		"malformed json":          {testPublicURL, `{"password":`, http.StatusBadRequest},
		"unknown field":           {testPublicURL, `{"password":"correct horse","remember":true}`, http.StatusBadRequest},
		"trailing value":          {testPublicURL, `{"password":"correct horse"} {}`, http.StatusBadRequest},
		"wrong type":              {testPublicURL, `{"password":1}`, http.StatusBadRequest},
		"empty password":          {testPublicURL, `{"password":""}`, http.StatusUnauthorized},
		"oversized body":          {testPublicURL, `{"password":"` + strings.Repeat("x", 9000) + `"}`, http.StatusRequestEntityTooLarge},
		"array instead of object": {testPublicURL, `["correct horse"]`, http.StatusBadRequest},
	} {
		headers := map[string]string{}
		if test.origin != "" {
			headers["Origin"] = test.origin
		}
		res := request(t, server, http.MethodPost, "/admin/api/login", test.body, headers)
		if res.Code != test.status {
			t.Errorf("%s: status = %d, want %d (%s)", name, res.Code, test.status, res.Body.String())
		}
		assertSecurityHeaders(t, res)
		if strings.Contains(res.Body.String(), "correct horse") {
			t.Errorf("%s: response echoes the password", name)
		}
	}
}

func TestLoginFailureIsGenericAndRateLimitedPerTrustedClientIP(t *testing.T) {
	server := newTestServer(t, "correct horse")
	attempt := func(remote, clientIP string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(`{"password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", testPublicURL)
		req.Header.Set("X-Ledger-Client-IP", clientIP)
		req.RemoteAddr = remote
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		return res
	}
	for i := 0; i < 4; i++ {
		res := attempt("172.31.255.2:1234", "198.51.100.1")
		if res.Code != http.StatusUnauthorized || res.Body.String() != `{"error":"invalid credentials"}`+"\n" {
			t.Fatalf("failure %d = %d %q", i+1, res.Code, res.Body.String())
		}
	}
	if res := attempt("172.31.255.2:1234", "198.51.100.2"); res.Code != http.StatusUnauthorized {
		t.Fatalf("second proxied client inherited the first bucket: %d", res.Code)
	}
	if res := attempt("172.31.255.2:1234", "198.51.100.1"); res.Code != http.StatusTooManyRequests || res.Header().Get("Retry-After") != "900" {
		t.Fatalf("fifth failure = %d retry=%q", res.Code, res.Header().Get("Retry-After"))
	}
	// A correct password from the locked client is still refused until the window passes.
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(`{"password":"correct horse"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testPublicURL)
	req.Header.Set("X-Ledger-Client-IP", "198.51.100.1")
	req.RemoteAddr = "172.31.255.2:1234"
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("locked client with correct password = %d", res.Code)
	}

	direct := newTestServer(t, "correct horse")
	server = direct
	for i := 0; i < 4; i++ {
		if res := attempt("198.51.100.8:1234", "203.0.113."+string(rune('1'+i))); res.Code != http.StatusUnauthorized {
			t.Fatalf("direct failure %d = %d", i+1, res.Code)
		}
	}
	if res := attempt("198.51.100.8:1234", "203.0.113.99"); res.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed client header bypassed the bucket for an untrusted peer: %d", res.Code)
	}
}

func TestLoginRequestRateLimitAppliesBeforePasswordVerification(t *testing.T) {
	server := newTestServer(t, "correct horse")
	var last *httptest.ResponseRecorder
	for i := 0; i < 21; i++ {
		last = request(t, server, http.MethodPost, "/admin/api/login", `{}`, map[string]string{"Origin": testPublicURL, "X-Ledger-Client-IP": "198.51.100.77"})
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") != "60" {
		t.Fatalf("21st login request = %d retry=%q", last.Code, last.Header().Get("Retry-After"))
	}
}

func TestCrossOriginLoginDoesNotConsumeRequestQuota(t *testing.T) {
	server := newTestServer(t, "correct horse")
	for i := 0; i < 20; i++ {
		res := request(t, server, http.MethodPost, "/admin/api/login", `{}`, map[string]string{"Origin": "https://evil.example", "X-Ledger-Client-IP": "198.51.100.79"})
		if res.Code != http.StatusForbidden {
			t.Fatalf("cross-origin request %d = %d", i+1, res.Code)
		}
	}
	res := request(t, server, http.MethodPost, "/admin/api/login", `{}`, map[string]string{"Origin": testPublicURL, "X-Ledger-Client-IP": "198.51.100.79"})
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("first same-origin request = %d, want 401; body=%s", res.Code, res.Body.String())
	}
}

func TestLoginFailsClosedWhenFailureLimiterIsAtCapacity(t *testing.T) {
	server := newTestServer(t, "correct horse")
	for i := 0; i < 10_000; i++ {
		if !server.failures.Allow(fmt.Sprintf("occupied-%d", i), 4, 15*time.Minute) {
			t.Fatalf("failed to fill failure limiter at key %d", i)
		}
	}
	res := request(t, server, http.MethodPost, "/admin/api/login", `{"password":"wrong"}`, map[string]string{"Origin": testPublicURL, "X-Ledger-Client-IP": "198.51.100.78"})
	if res.Code != http.StatusTooManyRequests || res.Header().Get("Retry-After") != "900" {
		t.Fatalf("capacity failure = %d retry=%q body=%s", res.Code, res.Header().Get("Retry-After"), res.Body.String())
	}
}

func TestPublicOriginIsDerivedFromPublicURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://ledger.example.com":           "https://ledger.example.com",
		"https://ledger.example.com/":          "https://ledger.example.com",
		"https://ledger.example.com:443/base":  "https://ledger.example.com",
		"http://ledger.example.com:80/base":    "http://ledger.example.com",
		"https://ledger.example.com:8443/base": "https://ledger.example.com:8443",
		"HTTPS://Ledger.Example.com/":          "https://ledger.example.com",
	} {
		if got := publicOrigin(input); got != want {
			t.Errorf("publicOrigin(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMutatingMethodsAreRejectedOnReadEndpoints(t *testing.T) {
	server := newTestServer(t, "correct horse")
	res := request(t, server, http.MethodGet, "/admin/api/login", "", nil)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET login = %d, want 405", res.Code)
	}
	assertSecurityHeaders(t, res)
}
