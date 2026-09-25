package retrieval

import (
	"reflect"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
)

func TestParseExtractionKeepsOnlyValidatedFields(t *testing.T) {
	entry := &store.PendingEntry{Slug: "atlas", ProjectName: "Atlas", Kind: "status", Source: "codex", Body: "Shipped export, see internal/admin/server.go and PR #33."}
	todos := []store.TodoCandidate{{ID: 7, Text: "Add export"}}
	meta, err := parseExtraction(`<think>x</think>{"title":"  Shipped   CSV export ","tags":["Deploy","status","atlas","deploy","two words","bad/tag","x","y","z"],"priority":"urgent","refs":["internal/admin/server.go","PR #33","invented.go"],"resolves":7}`, entry, todos, "m")
	if err != nil {
		t.Fatal(err)
	}
	want := store.EntryMeta{Title: "Shipped CSV export", Tags: []string{"deploy", "two-words", "x", "y"}, Priority: "normal", Refs: []string{"internal/admin/server.go", "PR #33"}, Model: "m"}
	resolves := meta.Resolves
	meta.Resolves = nil
	if !reflect.DeepEqual(meta, want) || resolves == nil || *resolves != 7 {
		t.Fatalf("meta = %#v resolves=%v", meta, resolves)
	}
	if meta, err := parseExtraction(`{"title":"t","tags":[],"priority":"high","refs":[],"resolves":99}`, entry, todos, ""); err != nil || meta.Resolves != nil {
		t.Fatalf("unknown todo id accepted: %#v %v", meta, err)
	}
	for _, bad := range []string{`not json`, `{"title":"   ","tags":[],"priority":"low","refs":[],"resolves":null}`} {
		if _, err := parseExtraction(bad, entry, todos, ""); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
