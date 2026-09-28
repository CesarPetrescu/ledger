package config

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestOpenDBGivesUpAfterWaiting(t *testing.T) {
	start := time.Now()
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for an unreachable database")
		}
		if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
			t.Fatalf("gave up after %v without retrying", elapsed)
		}
	}()
	openDB(context.Background(), "postgres://x@127.0.0.1:1/x?connect_timeout=1", 300*time.Millisecond, 50*time.Millisecond)
}

func TestOpenDBAttemptsEndAtTheDeadline(t *testing.T) {
	// Accepts TCP but never answers, like a database stuck mid-handshake.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	start := time.Now()
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("stalled attempt ran %v past a 300ms wait", elapsed)
		}
	}()
	openDB(context.Background(), "postgres://x@"+listener.Addr().String()+"/x", 300*time.Millisecond, 50*time.Millisecond)
}
