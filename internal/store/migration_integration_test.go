//go:build integration

package store_test

import (
	"reflect"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestMigrationsEmptyAndIdempotent(t *testing.T) {
	db, ctx := testdb.Open(t)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var versions []int
	if err := db.Pool.QueryRow(ctx, `SELECT array_agg(version ORDER BY version) FROM schema_migration`).Scan(&versions); err != nil || !reflect.DeepEqual(versions, []int{1, 2}) {
		t.Fatalf("applied migrations = %v, %v", versions, err)
	}
	var exists bool
	if err := db.Pool.QueryRow(ctx, `SELECT to_regclass('public.project') IS NOT NULL AND to_regclass('public.chunk') IS NOT NULL AND to_regclass('public.admin_session') IS NOT NULL`).Scan(&exists); err != nil || !exists {
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
	var sessionColumns []string
	if err := db.Pool.QueryRow(ctx, `SELECT array_agg(column_name::text ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='admin_session'`).Scan(&sessionColumns); err != nil {
		t.Fatal(err)
	}
	wantSessionColumns := []string{"hash", "csrf_token", "created_at", "expires_at", "last_seen_at"}
	if !reflect.DeepEqual(sessionColumns, wantSessionColumns) {
		t.Fatalf("admin_session columns = %v, want %v", sessionColumns, wantSessionColumns)
	}
}
