//go:build integration

package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestMigrationsEmptyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "pgvector/pgvector:pg16", postgres.WithDatabase("ledger"), postgres.WithUsername("ledger"), postgres.WithPassword("ledger"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var exists bool
	if err := db.Pool.QueryRow(ctx, `SELECT to_regclass('public.project') IS NOT NULL AND to_regclass('public.chunk') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("tables missing: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT
  to_regclass('public.oauth_client') IS NOT NULL AND
  to_regclass('public.oauth_code') IS NOT NULL AND
  to_regclass('public.oauth_token') IS NOT NULL AND
  to_regclass('public.client') IS NULL AND
  to_regclass('public.code') IS NULL AND
  to_regclass('public.token') IS NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("OAuth table contract mismatch: %v", err)
	}
	for _, column := range []struct{ name, dataType, defaultValue string }{
		{"hours_wk", "integer", "0"},
		{"deadline", "text", "''::text"},
		{"needs_me", "text", "''::text"},
	} {
		var dataType, nullable, defaultValue string
		err := db.Pool.QueryRow(ctx, `SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema='public' AND table_name='project' AND column_name=$1`, column.name).
			Scan(&dataType, &nullable, &defaultValue)
		if err != nil || dataType != column.dataType || nullable != "NO" || defaultValue != column.defaultValue {
			t.Errorf("project.%s = type %q nullable %q default %q, err %v", column.name, dataType, nullable, defaultValue, err)
		}
	}
	var entrySlug, obsoleteEntryColumn bool
	if err := db.Pool.QueryRow(ctx, `SELECT
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='entry' AND column_name='slug'),
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='entry' AND column_name='project_slug')`).Scan(&entrySlug, &obsoleteEntryColumn); err != nil || !entrySlug || obsoleteEntryColumn {
		t.Fatalf("entry FK columns: slug=%v project_slug=%v err=%v", entrySlug, obsoleteEntryColumn, err)
	}
	var codeColumns []string
	if err := db.Pool.QueryRow(ctx, `SELECT array_agg(column_name::text ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='oauth_code'`).Scan(&codeColumns); err != nil {
		t.Fatal(err)
	}
	wantCodeColumns := []string{"hash", "client_id", "redirect_uri", "code_challenge", "scope", "expires_at", "used"}
	if !reflect.DeepEqual(codeColumns, wantCodeColumns) {
		t.Fatalf("oauth_code columns = %v, want %v", codeColumns, wantCodeColumns)
	}
}
