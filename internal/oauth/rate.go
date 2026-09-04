package oauth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type RateLimiter struct {
	mu        sync.Mutex
	events    map[string]rateBucket
	nextSweep time.Time
	now       func() time.Time
}

type rateBucket struct {
	events []time.Time
	window time.Duration
}

// rateLimiterCapacity bounds attacker-controlled IP buckets; new keys fail closed at capacity.
const rateLimiterCapacity = 10_000

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{events: map[string]rateBucket{}, now: time.Now}
}

func (l *RateLimiter) Allow(key string, maximum int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if maximum < 1 || window <= 0 {
		return false
	}
	now := l.now()
	if l.nextSweep.IsZero() || !now.Before(l.nextSweep) {
		l.sweep(now)
	}
	cutoff := now.Add(-window)
	bucket, exists := l.events[key]
	items := bucket.events
	i := 0
	for i < len(items) && items[i].Before(cutoff) {
		i++
	}
	items = items[i:]
	if len(items) >= maximum {
		l.events[key] = rateBucket{events: items, window: window}
		return false
	}
	if !exists && len(l.events) >= rateLimiterCapacity {
		return false
	}
	l.events[key] = rateBucket{events: append(items, now), window: window}
	expires := now.Add(window)
	if l.nextSweep.IsZero() || expires.Before(l.nextSweep) {
		l.nextSweep = expires
	}
	return true
}

// Blocked reports whether key already reached maximum events inside window
// without recording a new event.
func (l *RateLimiter) Blocked(key string, maximum int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-window)
	live := 0
	for _, at := range l.events[key].events {
		if !at.Before(cutoff) {
			live++
		}
	}
	return live >= maximum
}

func (l *RateLimiter) sweep(now time.Time) {
	l.nextSweep = time.Time{}
	for key, bucket := range l.events {
		cutoff := now.Add(-bucket.window)
		i := 0
		for i < len(bucket.events) && bucket.events[i].Before(cutoff) {
			i++
		}
		if i == len(bucket.events) {
			delete(l.events, key)
			continue
		}
		bucket.events = bucket.events[i:]
		l.events[key] = bucket
		expires := bucket.events[0].Add(bucket.window)
		if l.nextSweep.IsZero() || expires.Before(l.nextSweep) {
			l.nextSweep = expires
		}
	}
}

func RealIP(r *http.Request, trusted *netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil || trusted == nil || !trusted.Contains(peer) {
		return host
	}
	values := r.Header.Values("X-Ledger-Client-IP")
	if len(values) == 1 {
		if addr, err := netip.ParseAddr(values[0]); err == nil {
			return addr.String()
		}
	}
	return host
}
