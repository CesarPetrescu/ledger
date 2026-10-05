//go:build integration

package store_test

import (
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

// A Trash snapshot taken before later migrations still restores: columns added since get their defaults,
// and keys for columns dropped since are ignored. No migration needs a restore fix of its own.
func TestOldTrashSnapshotsRestoreAcrossMigrations(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", Description: "Search"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProjectResearchVisible(ctx, "atlas", true); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "decision", "Use pgvector", "claude", "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO entry_meta(entry_id,title,category,details) VALUES($1,'pgvector','database','{"why":"cost"}')`, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO entry_owner_state(entry_id,starred) VALUES($1,true)`, entry.ID); err != nil {
		t.Fatal(err)
	}
	trashID, _, err := db.TrashProject(ctx, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	// Make the snapshot old: strip columns added by later migrations, and add one a later migration dropped.
	tag, err := db.Pool.Exec(ctx, `UPDATE trash SET payload=jsonb_set(payload
  #- '{project,research_visible}' #- '{entries,0,entry,context}' #- '{entries,0,entry,reply_to}'
  #- '{entries,0,meta,category}' #- '{entries,0,meta,details}' #- '{entries,0,meta,unsure}' #- '{entries,0,meta,edited}'
  #- '{entries,0,owner,starred}',
  '{project,retired_column}','"gone"') WHERE id=$1`, trashID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("age the snapshot: %v, %d", err, tag.RowsAffected())
	}
	if err := db.RestoreTrash(ctx, trashID); err != nil {
		t.Fatalf("restore an old snapshot = %v", err)
	}
	restored, err := db.GetProject(ctx, "atlas", 5)
	if err != nil || restored.Project.ResearchVisible || restored.Project.Description != "Search" || len(restored.Entries) != 1 || restored.Entries[0].Context != "" || restored.Entries[0].Body != "Use pgvector" {
		t.Fatalf("restored = %#v, %v", restored, err)
	}
	var title, category, details string
	var starred bool
	if err := db.Pool.QueryRow(ctx, `SELECT m.title,m.category,m.details::text,o.starred FROM entry_meta m JOIN entry_owner_state o ON o.entry_id=m.entry_id WHERE m.entry_id=$1`, entry.ID).Scan(&title, &category, &details, &starred); err != nil {
		t.Fatal(err)
	}
	if title != "pgvector" || category != "" || details != "{}" || starred {
		t.Fatalf("restored meta = %q %q %q starred=%v", title, category, details, starred)
	}
}
