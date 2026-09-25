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
	if err := db.ReopenTodo(ctx, todo.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEntryMeta(ctx, done.ID, store.EntryMeta{Title: "again", Resolves: &todo.ID}); err != nil {
		t.Fatal(err)
	}
	if open, err := db.ListEntries(ctx, store.EntryFilter{Status: "open"}); err != nil || len(open) != 1 {
		t.Fatalf("reopened todo = %#v %v", open, err)
	}
	if n, err := db.ClearModelMeta(ctx); err != nil || n != 1 {
		t.Fatalf("clear model meta = %d %v", n, err)
	}
}

func jsonID(id int64) string { b, _ := json.Marshal(id); return string(b) }
