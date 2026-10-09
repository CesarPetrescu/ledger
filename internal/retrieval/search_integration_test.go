//go:build integration

package retrieval

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
)

func TestSearchDegradesToFTSWhenEmbeddingFails(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO project(slug,name,tier,hours_wk) VALUES($1,$2,$3,$4)`, "atlas", "Atlas", "focus", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,0,$2,digest($2,'sha256'),$3)`, "project:atlas", "[project: Atlas (atlas) | tier: focus | deadline: none]\nRenovare bucătărie", "qwen3-embedding"); err == nil {
		t.Fatal("digest unexpectedly available without pgcrypto")
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,0,$2,decode($3,'hex'),$4)`, "project:atlas", "[project: Atlas (atlas) | tier: focus | deadline: none]\nRenovare bucătărie", "de89db35c8cfef2de84a85d431e0f3f1d73fb2a6c4c88bfb4d0c76b5d1da5f9d", "qwen3-embedding"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": .8}}})
	}))
	defer server.Close()
	searcher := NewSearcher(db, NewInferClient(server.URL, "qwen3-embedding", "qwen3-reranker", 4096, ""))
	result, err := searcher.Search(ctx, "renovare bucatarie", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Ref != "project:atlas" {
		t.Fatalf("hits = %#v", result.Hits)
	}
	if len(result.Degraded) != 1 || result.Degraded[0] != "vector" {
		t.Fatalf("degraded = %v", result.Degraded)
	}
}

func TestSearchFallsBackToFTSWhenInferenceIsUnreachable(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES('project:atlas',0,$1,decode(repeat('ab',32),'hex'),'qwen3-embedding')`, "[project: Atlas]\nRenovare bucătărie"); err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 1, so every inference call is refused.
	searcher := NewSearcher(db, NewInferClient("http://127.0.0.1:1/v1", "qwen3-embedding", "qwen3-reranker", 4096, ""))
	result, err := searcher.Search(ctx, "renovare bucatarie", 10)
	if err != nil || len(result.Hits) != 1 || strings.Join(result.Degraded, ",") != "vector,rerank" {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestSearchRerankFailurePreservesRRF(t *testing.T) {
	db, ctx := testdb.Open(t)
	for _, row := range []struct{ ref, text, hash string }{
		{"project:aa", "[project: Alpha (aa) | tier: focus | deadline: none]\nalpha alpha alpha", strings.Repeat("01", 32)},
		{"project:bb", "[project: Beta (bb) | tier: focus | deadline: none]\nalpha", strings.Repeat("02", 32)},
	} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,0,$2,decode($3,'hex'),'qwen3-embedding')`, row.ref, row.text, row.hash); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rerank" {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		vector := make([]float32, 4096)
		vector[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}})
	}))
	defer server.Close()
	result, err := NewSearcher(db, NewInferClient(server.URL, "qwen3-embedding", "qwen3-reranker", 4096, "")).Search(ctx, "alpha", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || result.Hits[0].Ref != "project:aa" || result.Hits[1].Ref != "project:bb" {
		t.Fatalf("RRF order = %#v", result.Hits)
	}
	if len(result.Degraded) != 1 || result.Degraded[0] != "rerank" {
		t.Fatalf("degraded = %v", result.Degraded)
	}
}

func TestSearchMalformedRerankPreservesBoundedRRF(t *testing.T) {
	db, ctx := testdb.Open(t)
	for _, row := range []struct{ ref, text, hash string }{
		{"project:aa", "[project: Alpha (aa) | tier: focus | deadline: none]\nalpha alpha alpha", strings.Repeat("01", 32)},
		{"project:bb", "[project: Beta (bb) | tier: focus | deadline: none]\nalpha alpha", strings.Repeat("02", 32)},
		{"project:cc", "[project: Gamma (cc) | tier: focus | deadline: none]\nalpha", strings.Repeat("03", 32)},
	} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,0,$2,decode($3,'hex'),'qwen3-embedding')`, row.ref, row.text, row.hash); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rerank" {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
				map[string]any{"index": 0, "relevance_score": .9},
				map[string]any{"index": 0, "relevance_score": .8},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": make([]float32, 4096)}}})
	}))
	defer server.Close()
	result, err := NewSearcher(db, NewInferClient(server.URL, "qwen3-embedding", "qwen3-reranker", 4096, "")).Search(ctx, "alpha", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || result.Hits[0].Ref != "project:aa" || result.Hits[1].Ref != "project:bb" {
		t.Fatalf("RRF fallback = %#v", result.Hits)
	}
	if len(result.Degraded) != 1 || result.Degraded[0] != "rerank" {
		t.Fatalf("degraded = %v", result.Degraded)
	}
}

// With ledger-index down there is no inference client and no indexer: the
// fallback matches words in the stored chunks of every model, once per ref,
// and skips chunks whose entry or project was deleted after indexing.
func TestLexicalSearchAnswersWithoutTheIndexService(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "decision", "Folosim PostgreSQL.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	entryRef := "entry:" + strconv.FormatInt(entry.ID, 10)
	for i, row := range []struct {
		ref   string
		ord   int
		text  string
		model string
	}{
		{entryRef, 0, "[atlas | decision]\nFolosim PostgreSQL pentru tot.", "old-embedding"},
		{entryRef, 1, "[atlas | decision]\nPostgreSQL PostgreSQL rămâne.", "qwen3-embedding"},
		{"project:atlas", 0, "[project: Atlas (atlas)]\nMigrare la PostgreSQL", "qwen3-embedding"},
		{"entry:999999", 0, "[atlas | note]\nPostgreSQL PostgreSQL PostgreSQL", "qwen3-embedding"},
		{"project:gone", 0, "[project: Gone (gone)]\nPostgreSQL PostgreSQL PostgreSQL", "qwen3-embedding"},
	} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,$2,$3,decode(repeat($4,32),'hex'),$5)`, row.ref, row.ord, row.text, fmt.Sprintf("%02x", i+1), row.model); err != nil {
			t.Fatal(err)
		}
	}
	result, err := NewSearcher(db, nil).Lexical(ctx, "postgresql", 10)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	for _, hit := range result.Hits {
		refs = append(refs, hit.Ref)
		if strings.HasPrefix(hit.Snippet, "[") {
			t.Fatalf("snippet keeps the chunk header: %q", hit.Snippet)
		}
	}
	if len(refs) != 2 || !slices.Contains(refs, "project:atlas") || !slices.Contains(refs, entryRef) || strings.Join(result.Degraded, ",") != "vector,rerank" {
		t.Fatalf("lexical = refs %v degraded %v", refs, result.Degraded)
	}
	if limited, err := NewSearcher(db, nil).Lexical(ctx, "postgresql", 1); err != nil || len(limited.Hits) != 1 {
		t.Fatalf("limited = %#v, %v", limited, err)
	}
}

func TestClientFallsBackToWordMatchesWhenTheIndexServiceFails(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES('project:atlas',0,$1,decode(repeat('ab',32),'hex'),'qwen3-embedding')`, "[project: Atlas]\nRenovare bucătărie"); err != nil {
		t.Fatal(err)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "search failed", http.StatusInternalServerError)
	}))
	defer failing.Close()
	// Nothing listens on port 1, so that index service is unreachable.
	for name, url := range map[string]string{"unreachable": "http://127.0.0.1:1", "failing": failing.URL} {
		result, err := NewClient(url, db).Search(ctx, "renovare bucatarie", 10)
		if err != nil || len(result.Hits) != 1 || result.Hits[0].Ref != "project:atlas" || result.Hits[0].Snippet != "Renovare bucătărie" || strings.Join(result.Degraded, ",") != "vector,rerank" {
			t.Fatalf("%s: result = %#v, %v", name, result, err)
		}
	}
	if _, err := NewClient("http://127.0.0.1:1", nil).Search(ctx, "renovare", 10); err == nil {
		t.Fatal("a client without a database has no fallback and must report the outage")
	}
}
