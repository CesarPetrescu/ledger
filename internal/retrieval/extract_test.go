package retrieval

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
)

func TestParseExtractionKeepsOnlyValidatedFields(t *testing.T) {
	entry := &store.PendingEntry{Slug: "atlas", ProjectName: "Atlas", Kind: "status", Source: "codex", Body: "Shipped export, see internal/admin/server.go and PR #33."}
	todos := []store.TodoCandidate{{ID: 7, Text: "Add export"}}
	meta, err := parseExtraction(`{"title":"  Shipped   CSV export ","tags":["Deploy","status","atlas","deploy","two words","bad/tag","x","y","z"],"priority":"urgent","refs":["internal/admin/server.go","PR #33","invented.go"],"resolves":7}`, entry, todos, "m")
	if err != nil {
		t.Fatal(err)
	}
	want := store.EntryMeta{Title: "Shipped CSV export", Tags: []string{"deploy", "two-words", "x", "y"}, Priority: "normal", Refs: []string{"internal/admin/server.go", "PR #33"}, Model: "m", Importance: "useful"}
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

func TestParseExtractionShapesFocusFieldsByKind(t *testing.T) {
	written := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	reply := `{"title":"Verify pricing","gist":"  Two   claims unverified. ","tags":[],"priority":"high","importance":"important",
"ask":"Confirm the pricing claims","state":"blocked","next_step":"Ask legal","blocker":"Waiting on legal","why":"Launch depends on it",
"size":"M","due":"2026-09-26","refs":[],"resolves":null}`
	todo := &store.PendingEntry{Kind: "todo", Slug: "site", ProjectName: "Site", Source: "codex", Body: "x", CreatedAt: written}
	meta, err := parseExtraction(reply, todo, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Gist != "Two claims unverified." || meta.Importance != "important" || meta.Ask != "Confirm the pricing claims" ||
		meta.NextStep != "Ask legal" || meta.Blocker != "Waiting on legal" || meta.Why != "Launch depends on it" ||
		meta.Size != "M" || meta.Due != "2026-09-26" || meta.State != "" {
		t.Fatalf("todo meta = %#v", meta)
	}
	// State belongs to statuses; size and due belong to todos.
	status := &store.PendingEntry{Kind: "status", Slug: "site", ProjectName: "Site", Source: "codex", Body: "x", CreatedAt: written}
	if meta, _ := parseExtraction(reply, status, nil, ""); meta.State != "blocked" || meta.Size != "" || meta.Due != "" {
		t.Fatalf("status meta = %#v", meta)
	}
	for _, bad := range []string{
		`{"title":"t","importance":"critical","state":"stuck","size":"XL","due":"2026-02-30","tags":[],"refs":[],"resolves":null}`,
		`{"title":"t","importance":"useful","due":"2031-01-01","tags":[],"refs":[],"resolves":null}`,
		`{"title":"t","importance":"useful","due":"2026-01-01","tags":[],"refs":[],"resolves":null}`,
	} {
		meta, err := parseExtraction(bad, todo, nil, "")
		if err != nil || meta.Importance != "useful" || meta.Size != "" || meta.Due != "" {
			t.Errorf("%s -> %#v %v", bad, meta, err)
		}
	}
}

const newsNote = `Title: Ollama v0.40.0-rc0: MLX runner becomes the default on Apple Silicon
What changed: Ollama tagged v0.40.0-rc0 (pre-release, 2026-09-25 03:31 UTC), jumping the version line from 0.34.x to 0.40. The headline change is MLX by default.
Why it matters: For Mac users this flips the default engine for supported models from GGML/Metal to MLX. It changes weight formats too.
Technical details: see release notes.
Source: GitHub release (ollama/ollama)
Date: 2026-09-25
URL: https://github.com/ollama/ollama/releases/tag/v0.40.0-rc0.
Category: Local AI | Inference / Apple`

func TestStructuredNotesUseTheirOwnFields(t *testing.T) {
	note, ok := parseStructuredNote(newsNote)
	if !ok || note.Title != "Ollama v0.40.0-rc0: MLX runner becomes the default on Apple Silicon" ||
		note.Changed != "Ollama tagged v0.40.0-rc0 (pre-release, 2026-09-25 03:31 UTC), jumping the version line from 0.34.x to 0.40. The headline change is MLX by default." ||
		note.Why != "For Mac users this flips the default engine for supported models from GGML/Metal to MLX. It changes weight formats too." ||
		note.Source != "GitHub release (ollama/ollama)" || note.Link != "https://github.com/ollama/ollama/releases/tag/v0.40.0-rc0" ||
		!reflect.DeepEqual(note.Categories, []string{"local-ai", "inference", "apple"}) {
		t.Fatalf("note = %#v %v", note, ok)
	}
	for _, prose := range []string{"Deployed the site. Title: nothing else here.", "Title: A\nplain text", "What changed: x\nWhy it matters: y"} {
		if _, ok := parseStructuredNote(prose); ok {
			t.Errorf("prose parsed as a note: %q", prose)
		}
	}
	if got := firstSentences("One. Two is longer. Three.", 20); got != "One. Two is longer." {
		t.Errorf("firstSentences = %q", got)
	}
	if got := firstURL("see javascript:alert(1) and ftp://x"); got != "" {
		t.Errorf("firstURL = %q", got)
	}

	entry := &store.PendingEntry{Kind: "note", Slug: "ai-news", ProjectName: "AI news", Source: "claude-code", Body: newsNote, CreatedAt: time.Now()}
	meta, err := parseExtraction(`{"title":"Model title","gist":"model gist","tags":["ollama","inference"],"priority":"normal","importance":"useful","ask":"","state":"","next_step":"","blocker":"","why":"model why","size":"","due":"","refs":[],"resolves":null}`, entry, nil, "")
	if err != nil || meta.Title != note.Title || meta.Gist != "model gist" || meta.Why != note.Why || meta.Link != note.Link ||
		meta.SourceName != note.Source || !reflect.DeepEqual(meta.Tags, []string{"local-ai", "inference", "apple", "ollama"}) {
		t.Fatalf("merged meta = %#v %v", meta, err)
	}
	if meta, _ := parseExtraction(`{"title":"t","gist":"","tags":[],"importance":"useful","refs":[],"resolves":null}`, entry, nil, ""); meta.Gist != note.Changed {
		t.Fatalf("gist fallback = %q", meta.Gist)
	}
}
