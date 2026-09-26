package retrieval

import (
	"math"
	"reflect"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
)

func TestParseExtractionKeepsOnlyValidatedFields(t *testing.T) {
	entry := &store.PendingEntry{Slug: "atlas", ProjectName: "Atlas", Kind: "status", Source: "codex", Body: "Shipped export, see internal/admin/server.go and PR #33."}
	todos := []store.TodoCandidate{{ID: 7, Text: "Add export"}}
	meta, err := parseExtraction(`{"title":"  Shipped   CSV export ","tags":["Deploy","status","atlas","deploy","two words","bad/tag","x","y","z"],"priority":"urgent","refs":["internal/admin/server.go","PR #33","invented.go"],"resolves":7}`, entry, todos, "m")
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

func TestValidMergesRejectsUnknownSelfDoubleAndChainedMerges(t *testing.T) {
	merges := []struct{ From, To string }{
		{"asr", "stt"}, {"asr", "speech"}, {"stt", "stt"}, {"ghost", "stt"},
		{"llama-cpp", "llama.cpp"}, {"vlm", "vision"}, {"vision", "multimodal"},
	}
	got := validMerges(merges, []string{"asr", "stt", "speech", "llama-cpp", "llama.cpp", "vlm", "vision", "multimodal"})
	want := map[string]string{"asr": "stt", "llama-cpp": "llama.cpp", "vision": "multimodal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merges = %v, want %v", got, want)
	}
	if tags := canonicalTags([]string{"asr", "stt", "deploy"}, want); !reflect.DeepEqual(tags, []string{"stt", "deploy"}) {
		t.Fatalf("canonical tags = %v", tags)
	}
	chained := map[string]string{"a": "b", "b": "c", "loop": "loop2", "loop2": "loop"}
	if tags := canonicalTags([]string{"a", "c", "loop"}, chained); !reflect.DeepEqual(tags, []string{"c", "loop"}) {
		t.Fatalf("chained canonical tags = %v", tags)
	}
}

func TestValidDuplicateThreshold(t *testing.T) {
	for value, want := range map[float64]bool{0.9: true, 1: true, 0.01: true, 0: false, -0.9: false, 1.1: false, math.NaN(): false, math.Inf(1): false} {
		if got := ValidDuplicateThreshold(value); got != want {
			t.Errorf("ValidDuplicateThreshold(%v) = %v", value, got)
		}
	}
}
