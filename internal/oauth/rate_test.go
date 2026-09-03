package oauth

import (
	"fmt"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestRateLimiterSweepsExpiredKeysAndFailsClosedAtCapacity(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter()
	limiter.now = func() time.Time { return now }
	if !limiter.allow("expired", 1, time.Minute) {
		t.Fatal("initial key denied")
	}
	now = now.Add(2 * time.Minute)
	if !limiter.allow("fresh", 1, time.Minute) {
		t.Fatal("fresh key denied after expiry")
	}
	if _, ok := limiter.events["expired"]; ok {
		t.Fatal("expired unrelated key was not swept")
	}

	limiter = newRateLimiter()
	limiter.now = func() time.Time { return now }
	for i := 0; i < rateLimiterCapacity; i++ {
		if !limiter.allow(fmt.Sprintf("key-%d", i), 1, time.Hour) {
			t.Fatalf("key %d denied before capacity", i)
		}
	}
	if limiter.allow("overflow", 1, time.Hour) {
		t.Fatal("new key allowed over capacity")
	}
	if len(limiter.events) != rateLimiterCapacity {
		t.Fatalf("cardinality = %d, want %d", len(limiter.events), rateLimiterCapacity)
	}
}

func TestRealIPTrustsOnlyAuthoritativeHeaderFromInternalProxy(t *testing.T) {
	proxy := netip.MustParsePrefix("172.31.255.2/32")
	trusted := httptest.NewRequest("GET", "/", nil)
	trusted.RemoteAddr = "172.31.255.2:1234"
	trusted.Header.Set("X-Ledger-Client-IP", "203.0.113.7")
	trusted.Header.Set("X-Forwarded-For", "198.51.100.99")
	if got := realIP(trusted, &proxy); got != "203.0.113.7" {
		t.Fatalf("trusted proxy IP = %q", got)
	}

	untrusted := httptest.NewRequest("GET", "/", nil)
	untrusted.RemoteAddr = "198.51.100.8:1234"
	untrusted.Header.Set("X-Ledger-Client-IP", "203.0.113.9")
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.10")
	if got := realIP(untrusted, &proxy); got != "198.51.100.8" {
		t.Fatalf("untrusted spoof changed IP to %q", got)
	}
}
