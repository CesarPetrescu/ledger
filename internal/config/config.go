package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
)

func Required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(fmt.Sprintf("required environment variable %s is unset", name))
	}
	return value
}

func Value(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func Int(name string, fallback int) int {
	if value := os.Getenv(name); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			panic(fmt.Sprintf("%s must be an integer", name))
		}
		return parsed
	}
	return fallback
}

func Float(name string, fallback float64) float64 {
	if value := os.Getenv(name); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			panic(fmt.Sprintf("%s must be a number", name))
		}
		return parsed
	}
	return fallback
}

// OpenDB waits up to two minutes for the database: after a host reboot Docker
// restarts every container at once, ignoring compose's start order.
func OpenDB(ctx context.Context) *store.DB {
	return openDB(ctx, Required("LEDGER_DATABASE_URL"), 2*time.Minute, 2*time.Second)
}

func openDB(ctx context.Context, dsn string, wait, every time.Duration) *store.DB {
	deadline := time.Now().Add(wait)
	// Each attempt ends by the deadline too, so a stalled handshake cannot hang startup.
	open := func() (*store.DB, error) {
		attempt, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		return store.Open(attempt, dsn)
	}
	db, err := open()
	for err != nil && time.Now().Before(deadline) {
		log.Printf("database not ready, retrying: %v", err)
		time.Sleep(every)
		db, err = open()
	}
	if err != nil {
		panic(err)
	}
	if err := db.Migrate(ctx); err != nil {
		db.Close()
		panic(err)
	}
	return db
}
