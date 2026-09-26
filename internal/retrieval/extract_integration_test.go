//go:build integration

package retrieval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestExtractorLabelsEntriesAndLinksResolvedTodos(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	todo, err := db.AppendEntry(ctx, "atlas", "todo", "Add CSV export", "codex", "c")
	if err != nil {
		t.Fatal(err)
	}
	var down atomic.Bool
	var sawTodo atomic.Bool
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		var request struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		input := request.Messages[1].Content
		reply := `{"title":"Add CSV export","tags":["export"],"priority":"high","refs":[],"resolves":null}`
		if strings.Contains(input, "Shipped") {
			sawTodo.Store(strings.Contains(input, `"open_todos":[{"id":`))
			reply = `{"title":"Shipped CSV export","tags":["export"],"priority":"normal","refs":[],"resolves":` + jsonID(todo.ID) + `}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer chat.Close()
	x := NewExtractor(db, chat.URL, "", "", nil, 0.9)
	if worked, err := x.ProcessOne(ctx); !worked || err != nil {
		t.Fatalf("todo extraction = %v %v", worked, err)
	}
	done, err := db.AppendEntry(ctx, "atlas", "status", "Shipped the export.", "codex", "c")
	if err != nil {
		t.Fatal(err)
	}

	down.Store(true)
	if worked, err := x.ProcessOne(ctx); worked || err == nil {
		t.Fatalf("outage = %v %v", worked, err)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_meta WHERE entry_id=$1`, done.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("outage spent an attempt: %d %v", rows, err)
	}
	down.Store(false)
	if worked, err := x.ProcessOne(ctx); !worked || err != nil || !sawTodo.Load() {
		t.Fatalf("status extraction = %v %v open todos sent=%v", worked, err, sawTodo.Load())
	}
	if worked, err := x.ProcessOne(ctx); worked || err != nil {
		t.Fatalf("idle = %v %v", worked, err)
	}
	open, err := db.ListEntries(ctx, store.EntryFilter{Status: "open"})
	if err != nil || len(open) != 0 {
		t.Fatalf("open todos = %#v %v", open, err)
	}
	closed, err := db.ListEntries(ctx, store.EntryFilter{Status: "done", Tag: "export"})
	if err != nil || len(closed) != 1 || closed[0].Meta.Priority != "high" || closed[0].ResolvedBy.EntryID != done.ID || closed[0].ResolvedBy.Origin != "model" {
		t.Fatalf("done todos = %#v %v", closed, err)
	}

	// The owner overrides the model, and later extraction must not undo it.
	if _, err := db.ReopenTodo(ctx, todo.ID, "ledger-admin", "c"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEntryMeta(ctx, done.ID, store.EntryMeta{Title: "again", Resolves: &todo.ID}); err != nil {
		t.Fatal(err)
	}
	if open, err := db.ListEntries(ctx, store.EntryFilter{Status: "open"}); err != nil || len(open) != 1 {
		t.Fatalf("reopened todo = %#v %v", open, err)
	}
	if err := db.SaveDigest(ctx, "atlas", "Old digest.", 2, done.ID, "m"); err != nil {
		t.Fatal(err)
	}
	if n, err := db.ClearModelMeta(ctx); err != nil || n != 1 {
		t.Fatalf("clear model meta = %d %v", n, err)
	}
	var digests int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM project_digest`).Scan(&digests); err != nil || digests != 0 {
		t.Fatalf("digests after reextract = %d %v", digests, err)
	}
}

func jsonID(id int64) string { b, _ := json.Marshal(id); return string(b) }

func TestUpgradeReextractionKeepsResolutionAndSurvivesFailures(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	todo, _ := db.AppendEntry(ctx, "atlas", "todo", "Add CSV export", "codex", "c")
	done, _ := db.AppendEntry(ctx, "atlas", "status", "Shipped the export.", "codex", "c")
	broken, _ := db.AppendEntry(ctx, "atlas", "note", "Unparseable", "codex", "c")
	for _, e := range []struct {
		id       int64
		title    string
		resolves *int64
	}{{todo.ID, "Add CSV export", nil}, {done.ID, "Shipped export", &todo.ID}, {broken.ID, "Old title", nil}} {
		if err := db.SaveEntryMeta(ctx, e.id, store.EntryMeta{Title: e.title, Resolves: e.resolves}); err != nil {
			t.Fatal(err)
		}
	}
	// Pretend these rows came from the previous extraction schema.
	if _, err := db.Pool.Exec(ctx, `UPDATE entry_meta SET version=1`); err != nil {
		t.Fatal(err)
	}
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := `{"title":"Shipped CSV export","gist":"Export is live","tags":[],"priority":"normal","importance":"important","ask":"","state":"done","next_step":"","blocker":"","why":"","size":"","due":"","refs":[],"resolves":null}`
		if strings.Contains(req.Messages[1].Content, "Unparseable") {
			reply = `not json`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer chat.Close()
	x := NewExtractor(db, chat.URL, "", "", nil, 0.9)
	for i := 0; i < 10; i++ {
		if worked, err := x.ProcessOne(ctx); err != nil || !worked {
			break
		}
	}
	var version int
	var resolves *int64
	var title, gist, state string
	if err := db.Pool.QueryRow(ctx, `SELECT version,resolves,title,gist,state FROM entry_meta WHERE entry_id=$1`, done.ID).Scan(&version, &resolves, &title, &gist, &state); err != nil {
		t.Fatal(err)
	}
	if version != store.MetaVersion || resolves == nil || *resolves != todo.ID || gist != "Export is live" || state != "done" {
		t.Fatalf("upgraded status = v%d resolves=%v title=%q gist=%q state=%q", version, resolves, title, gist, state)
	}
	// A failed upgrade keeps the old title and is not retried.
	if err := db.Pool.QueryRow(ctx, `SELECT version,title FROM entry_meta WHERE entry_id=$1`, broken.ID).Scan(&version, &title); err != nil || version != store.MetaVersion || title != "Old title" {
		t.Fatalf("failed upgrade = v%d %q %v", version, title, err)
	}
	if next, err := db.NextUnlabeledEntry(ctx); err != nil || next != nil {
		t.Fatalf("work left after upgrade: %#v %v", next, err)
	}
}
