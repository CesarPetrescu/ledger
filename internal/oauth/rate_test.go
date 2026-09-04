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
	limiter := NewRateLimiter()
	limiter.now = func() time.Time { return now }
	if !limiter.Allow("expired", 1, time.Minute) {
		t.Fatal("initial key denied")
	}
	now = now.Add(2 * time.Minute)
	if !limiter.Allow("fresh", 1, time.Minute) {
		t.Fatal("fresh key denied after expiry")
	}
	if _, ok := limiter.events["expired"]; ok {
		t.Fatal("expired unrelated key was not swept")
	}

	limiter = NewRateLimiter()
	limiter.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if limiter.Blocked("peek", 2, time.Minute) || !limiter.Allow("peek", 2, time.Minute) {
			t.Fatalf("attempt %d blocked early", i+1)
		}
	}
	if !limiter.Blocked("peek", 2, time.Minute) || limiter.Blocked("other", 2, time.Minute) {
		t.Fatal("Blocked does not reflect the recorded window")
	}
	now = now.Add(2 * time.Minute)
	if limiter.Blocked("peek", 2, time.Minute) {
		t.Fatal("Blocked ignored window expiry")
	}

	limiter = NewRateLimiter()
	limiter.now = func() time.Time { return now }
	for i := 0; i < rateLimiterCapacity; i++ {
		if !limiter.Allow(fmt.Sprintf("key-%d", i), 1, time.Hour) {
			t.Fatalf("key %d denied before capacity", i)
		}
	}
	if limiter.Allow("overflow", 1, time.Hour) {
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
	if got := RealIP(trusted, &proxy); got != "203.0.113.7" {
		t.Fatalf("trusted proxy IP = %q", got)
	}

	untrusted := httptest.NewRequest("GET", "/", nil)
	untrusted.RemoteAddr = "198.51.100.8:1234"
	untrusted.Header.Set("X-Ledger-Client-IP", "203.0.113.9")
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.10")
	if got := RealIP(untrusted, &proxy); got != "198.51.100.8" {
		t.Fatalf("untrusted spoof changed IP to %q", got)
	}
}
