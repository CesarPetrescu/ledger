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

func TestProjectHeadlineSkipsRoutineStatuses(t *testing.T) {
	db, ctx := testdb.Open(t)
	for _, slug := range []string{"atlas", "beacon"} {
		if _, err := db.UpsertProject(ctx, store.Project{Slug: slug, Name: slug, Tier: "focus"}); err != nil {
			t.Fatal(err)
		}
	}
	status := func(slug, body, importance string) {
		t.Helper()
		e, err := db.AppendEntry(ctx, slug, "status", body, "codex", "c")
		if err != nil {
			t.Fatal(err)
		}
		if importance != "" {
			if err := db.SaveEntryMeta(ctx, e.ID, store.EntryMeta{Title: body, Importance: importance}); err != nil {
				t.Fatal(err)
			}
		}
	}
	headlines := func() map[string]string {
		t.Helper()
		summaries, err := db.ProjectSummaries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, s := range summaries {
			out[s.Slug] = s.StatusTitle + "|" + s.StatusBody
		}
		return out
	}
	status("atlas", "Shipped search", "important")
	status("atlas", "Checkpoint: tests green", "routine")
	status("beacon", "Heartbeat one", "routine")
	status("beacon", "Heartbeat two", "routine")
	// A newer routine checkpoint does not replace the real news; with only routine statuses, the newest leads.
	if got := headlines(); got["atlas"] != "Shipped search|Shipped search" || got["beacon"] != "Heartbeat two|Heartbeat two" {
		t.Fatalf("headlines = %v", got)
	}
	// A status not labelled yet is not known to be routine, so it leads.
	status("atlas", "Started the export", "")
	if got := headlines(); got["atlas"] != "|Started the export" {
		t.Fatalf("headline with an unlabelled status = %v", got)
	}
}
