package store

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/cesarpetrescu/ledger/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() { db.Pool.Close() }

// Migrate applies every embedded NNNN_name.sql file in numeric order exactly once.
func (db *DB) Migrate(ctx context.Context) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7103375)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return err
	}
	for _, file := range files {
		name := file.Name()
		version, err := parseMigrationName(name)
		if err != nil {
			return err
		}
		var applied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migration WHERE version=$1)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migration(version) VALUES ($1)`, version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func parseMigrationName(name string) (int, error) {
	if len(name) < 5 || name[4] != '_' {
		return 0, fmt.Errorf("migration %s: name must start with a four digit version", name)
	}
	version, err := strconv.Atoi(name[:4])
	if err != nil {
		return 0, fmt.Errorf("migration %s: name must start with a four digit version", name)
	}
	return version, nil
}
