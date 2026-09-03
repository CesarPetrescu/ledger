//go:build integration

package retrieval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchDegradesToFTSWhenEmbeddingFails(t *testing.T) {
	db, ctx := indexDB(t)
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

func TestSearchRerankFailurePreservesRRF(t *testing.T) {
	db, ctx := indexDB(t)
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
	db, ctx := indexDB(t)
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
