// Package testdb provides a migrated PostgreSQL database for integration tests.
//
// By default it starts a throwaway pgvector container through testcontainers.
// When LEDGER_TEST_DATABASE_URL points at a local PostgreSQL server with the
// vector and unaccent extensions, each test instead receives a freshly created
// database on that server, which allows running the integration suite without
// Docker.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Open returns a migrated, isolated database that is torn down with the test.
func Open(t *testing.T) (*store.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = containerDSN(t, ctx)
	} else {
		dsn = localDSN(t, ctx, dsn)
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func containerDSN(t *testing.T, ctx context.Context) string {
	t.Helper()
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg16", postgres.WithDatabase("ledger"), postgres.WithUsername("ledger"), postgres.WithPassword("ledger"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func localDSN(t *testing.T, ctx context.Context, adminDSN string) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	name := "ledger_test_" + hex.EncodeToString(raw)
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
		_ = admin.Close(ctx)
	})
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
