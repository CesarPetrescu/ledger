package config

import (
	"context"
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
