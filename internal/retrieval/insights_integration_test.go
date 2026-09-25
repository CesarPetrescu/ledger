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

// topicVector puts each topic on its own axis so similarity is exact.
func topicVector(text string) []float32 {
	v := make([]float32, 4096)
	switch {
	case strings.Contains(text, "checkpoint"):
		v[0] = 1
	case strings.Contains(text, "export"):
		v[1], v[2] = 0.8, 0.6 // related to "csv" at 0.8
	case strings.Contains(text, "csv"):
		v[1] = 1
	case strings.HasPrefix(text, "todo "):
		v[10+int(text[5]-'0')] = 1
	default:
		v[3] = 1
	}
	return v
}

func TestInsightsEmbedFoldDuplicatesMergeTagsAndWriteDigests(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"Todo 0 csv", "Todo 1", "Todo 2", "Todo 3", "Todo 4", "Todo 5", "Todo 6", "Todo 7"} {
		if _, err := db.AppendEntry(ctx, "atlas", "todo", body, "codex", "c"); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := db.AppendEntry(ctx, "atlas", "status", "Digest checkpoint reached", "codex", "c")
	second, _ := db.AppendEntry(ctx, "atlas", "status", "Digest checkpoint reached again", "codex", "c")
	export, _ := db.AppendEntry(ctx, "atlas", "note", "Shipped the export", "codex", "c")

	var shortlisted atomic.Int64
	infer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings":
			var req struct{ Input []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := []map[string]any{}
			for i, text := range req.Input {
				data = append(data, map[string]any{"index": i, "embedding": topicVector(strings.ToLower(text))})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/rerank":
			var req struct {
				Documents []string
				TopN      int `json:"top_n"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			results := []map[string]any{}
			for i := range req.TopN {
				results = append(results, map[string]any{"index": len(req.Documents) - 1 - i, "relevance_score": float64(i)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		}
	}))
	defer infer.Close()
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		system, input := req.Messages[0].Content, req.Messages[1].Content
		var reply string
		switch {
		case strings.HasPrefix(system, "You label"):
			var in struct {
				Text  string
				Todos []store.TodoCandidate `json:"open_todos"`
			}
			_ = json.Unmarshal([]byte(input), &in)
			if len(in.Todos) > 0 {
				shortlisted.Store(int64(len(in.Todos)))
			}
			tag := "benchmarks"
			if strings.Contains(in.Text, "checkpoint") {
				tag = "benchmark"
			}
			reply = `{"title":"` + in.Text + `","tags":["` + tag + `"],"priority":"normal","refs":[],"resolves":null}`
		case strings.HasPrefix(system, "You maintain"):
			reply = `{"merges":[{"from":"benchmarks","to":"benchmark"},{"from":"benchmark","to":"benchmark"},{"from":"nope","to":"benchmark"}]}`
		case strings.HasPrefix(system, "Summarize"):
			reply = `{"summary":"  Shipped   the export.  "}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer chat.Close()

	x := NewExtractor(db, chat.URL, "", "", NewInferClient(infer.URL, "embed", "rerank", 4096, ""), 0.9)
	for i := 0; ; i++ {
		worked, err := x.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
		if i > 100 {
			t.Fatal("insights never settled")
		}
	}

	if shortlisted.Load() != todoShortlist {
		t.Errorf("todo candidates sent to the model = %d, want %d", shortlisted.Load(), todoShortlist)
	}
	var dupOf *int64
	if err := db.Pool.QueryRow(ctx, `SELECT duplicate_of FROM entry_meta WHERE entry_id=$1`, second.ID).Scan(&dupOf); err != nil || dupOf == nil || *dupOf != first.ID {
		t.Fatalf("duplicate_of = %v %v, want %d", dupOf, err, first.ID)
	}
	var unrelated int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry_meta WHERE duplicate_of IS NOT NULL`).Scan(&unrelated); err != nil || unrelated != 1 {
		t.Fatalf("duplicates = %d %v", unrelated, err)
	}
	tags, err := db.EntryTags(ctx, 10)
	if err != nil || strings.Join(tags, ",") != "benchmark" {
		t.Fatalf("tags after merge = %v %v", tags, err)
	}
	aliases, _ := db.TagAliases(ctx)
	if len(aliases) != 1 || aliases["benchmarks"] != "benchmark" {
		t.Fatalf("aliases = %v", aliases)
	}
	related, err := db.RelatedEntries(ctx, export.ID, 0.62, 3)
	if err != nil || len(related) != 1 || related[0].Body != "Todo 0 csv" || related[0].Similarity < 0.79 {
		t.Fatalf("related = %#v %v", related, err)
	}
	if dupRelated, _ := db.RelatedEntries(ctx, second.ID, 0.62, 3); len(dupRelated) != 0 {
		t.Fatalf("an entry's own duplicate was listed as related: %#v", dupRelated)
	}
	summaries, err := db.ProjectSummaries(ctx)
	if err != nil || summaries[0].Digest != "Shipped the export." || summaries[0].DigestAt == nil {
		t.Fatalf("digest = %#v %v", summaries, err)
	}
}
