//go:build integration

package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func glassFixtures(t *testing.T) (*store.DB, context.Context) {
	t.Helper()
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"glass-a", "glass-b"} {
		if _, err := db.PutClient(ctx, store.OAuthClient{ClientID: id, Kind: "device", Name: "Glass fixture", RedirectURIs: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx
}
func TestCaptureConcurrentRetriesAreAtomic(t *testing.T) {
	db, ctx := glassFixtures(t)
	const attempts = 12
	ids := make(chan int64, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, err := db.AppendEntryOnce(ctx, "atlas", "note", "Ședință Atlas", "glass", "glass-a", "same-request-001")
			if err != nil {
				errs <- err
			} else {
				ids <- entry.ID
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatal(err)
	}
	var id int64
	for value := range ids {
		if id != 0 && id != value {
			t.Fatalf("duplicate receipts %d and %d", id, value)
		}
		id = value
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("entries=%d err=%v", count, err)
	}
	if _, err := db.AppendEntryOnce(ctx, "atlas", "note", "Different content", "glass", "glass-a", "same-request-001"); err == nil {
		t.Fatal("conflicting retry was accepted")
	}
	// A different client has its own key namespace.
	if _, err := db.AppendEntryOnce(ctx, "atlas", "note", "Independent", "glass", "glass-b", "same-request-001"); err != nil {
		t.Fatal(err)
	}
}
func TestChangesSnapshotPaginationAcknowledgementAndIsolation(t *testing.T) {
	db, ctx := glassFixtures(t)
	for i := 0; i < 43; i++ {
		if _, err := db.AppendEntry(ctx, "atlas", "note", fmt.Sprintf("Change %d", i), "owner", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.Changes(ctx, "glass-a", "glass", "", "", 18)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 18 || !first.HasMore || first.Checkpoint != "0" {
		t.Fatalf("first page=%+v", first)
	}
	if _, err := db.AcknowledgeChanges(ctx, "glass-a", "glass", first.Through); err == nil {
		t.Fatal("acknowledged unfetched page")
	}
	if _, err := db.Changes(ctx, "glass-a", "glass", first.Through, first.Through, 18); err == nil {
		t.Fatal("skipped an unfetched range")
	}
	late, err := db.AppendEntry(ctx, "atlas", "note", "After snapshot", "owner", "owner")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	page := first
	for {
		for _, entry := range page.Entries {
			if ids[entry.ID] {
				t.Fatal("duplicate entry")
			}
			ids[entry.ID] = true
		}
		if _, err := db.AcknowledgeChanges(ctx, "glass-a", "glass", page.NextCursor); err != nil {
			t.Fatal(err)
		}
		if !page.HasMore {
			break
		}
		page, err = db.Changes(ctx, "glass-a", "glass", page.NextCursor, page.Through, 18)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(ids) != 43 {
		t.Fatalf("snapshot returned %d entries", len(ids))
	}
	restarted := &store.DB{Pool: db.Pool}
	next, err := restarted.Changes(ctx, "glass-a", "glass", "", "", 18)
	if err != nil || len(next.Entries) != 1 || next.Entries[0].ID != fmt.Sprint(late.ID) {
		t.Fatalf("restart=%+v err=%v", next, err)
	}
	other, err := db.Changes(ctx, "glass-b", "glass", "", "", 100)
	if err != nil || len(other.Entries) != 44 || other.Checkpoint != "0" {
		t.Fatalf("other reader=%+v err=%v", other, err)
	}
}
func TestChangeCursorDoesNotSkipLateLowerEntryID(t *testing.T) {
	db, ctx := glassFixtures(t)
	var reserved int64
	if err := db.Pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('entry','id'))`).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	newer, err := db.AppendEntry(ctx, "atlas", "note", "Committed first", "owner", "owner")
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.Changes(ctx, "glass-a", "glass", "", "", 18)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcknowledgeChanges(ctx, "glass-a", "glass", first.NextCursor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO entry(id,slug,kind,body,source,client_id) OVERRIDING SYSTEM VALUE VALUES($1,'atlas','note','Older identity committed late','owner','owner')`, reserved); err != nil {
		t.Fatal(err)
	}
	second, err := db.Changes(ctx, "glass-a", "glass", "", "", 18)
	if err != nil || len(second.Entries) != 1 || second.Entries[0].ID != fmt.Sprint(reserved) || reserved >= newer.ID {
		t.Fatalf("late entry was skipped: %+v %v", second, err)
	}
}
