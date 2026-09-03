package config

import (
	"context"
	"fmt"
	"os"
	"strconv"

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

func OpenDB(ctx context.Context) *store.DB {
	db, err := store.Open(ctx, Required("LEDGER_DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	if err := db.Migrate(ctx); err != nil {
		db.Close()
		panic(err)
	}
	return db
}
