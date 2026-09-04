//go:build integration

package store_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestAppendEntryIsAppendOnlyAndQueuesNotification(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN chunk_dirty`); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "decision", "Folosim PostgreSQL.", "test-client", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	note, err := conn.Conn().WaitForNotification(wait)
	if err != nil || note.Payload != "entry:"+strconv.FormatInt(entry.ID, 10) {
		t.Fatalf("notification = %#v, %v", note, err)
	}
	var dirty bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chunk_dirty WHERE ref=$1)`, note.Payload).Scan(&dirty); err != nil || !dirty {
		t.Fatalf("dirty row missing: %v", err)
	}
}
