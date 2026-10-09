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
	// The indexer had processed these rows before it stopped.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty`); err != nil {
		t.Fatal(err)
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
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty`); err != nil {
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

// Entries and projects written while the indexer was down (or on an install
// where it never ran) have no chunks yet; the fallback matches their live text.
func TestLexicalSearchMatchesRowsTheIndexerNeverChunked(t *testing.T) {
	db, ctx := testdb.Open(t)
	for _, project := range []store.Project{
		{Slug: "atlas", Name: "Atlas", Tier: "focus", Goal: "Ship the reranker"},
		{Slug: "beacon", Name: "Beacon", Tier: "park"},
	} {
		if _, err := db.UpsertProject(ctx, project); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := db.AppendEntry(ctx, "beacon", "note", "Measured it.\nSwapped the reranker for a smaller one.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := db.AppendEntry(ctx, "beacon", "decision", "Keep the reranker.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendEntry(ctx, "beacon", "note", "Unrelated words.", "agent", "client-1"); err != nil {
		t.Fatal(err)
	}
	indexedRef := "entry:" + strconv.FormatInt(indexed.ID, 10)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,0,'[beacon | decision]'||chr(10)||'Keep the reranker.',decode(repeat('ab',32),'hex'),'qwen3-embedding')`, indexedRef); err != nil {
		t.Fatal(err)
	}
	result, err := NewSearcher(db, nil).Lexical(ctx, "reranker", 10)
	if err != nil {
		t.Fatal(err)
	}
	hits := map[string]Ranked{}
	for _, hit := range result.Hits {
		if _, dup := hits[hit.Ref]; dup {
			t.Fatalf("%s listed twice: %#v", hit.Ref, result.Hits)
		}
		hits[hit.Ref] = hit
	}
	freshRef := "entry:" + strconv.FormatInt(fresh.ID, 10)
	if len(hits) != 3 || hits[indexedRef].Kind != "decision" {
		t.Fatalf("hits = %#v", result.Hits)
	}
	if hit := hits[freshRef]; hit.Kind != "note" || hit.ProjectSlug != "beacon" || hit.Snippet != "Measured it.\nSwapped the reranker for a smaller one." {
		t.Fatalf("unchunked entry = %#v", hit)
	}
	if hit := hits["project:atlas"]; hit.Kind != "project" || hit.ProjectSlug != "atlas" || !strings.Contains(hit.Snippet, "\nGoal: Ship the reranker\n") {
		t.Fatalf("unchunked project = %#v", hit)
	}
	if strings.Join(result.Degraded, ",") != "vector,rerank" {
		t.Fatalf("degraded = %v", result.Degraded)
	}
}

// A project edited after it was indexed keeps its old chunk while its ref waits
// in chunk_dirty; the fallback must search the current text, not the stale chunk.
func TestLexicalSearchReadsCurrentTextOfRowsQueuedForReindexing(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", Goal: "Ship the reranker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES('project:atlas',0,'[project: Atlas (atlas)]'||chr(10)||'Ship the reranker',decode(repeat('ab',32),'hex'),'qwen3-embedding')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty`); err != nil {
		t.Fatal(err)
	}
	// Edited while the indexer is down: the trigger queues it again.
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", Goal: "Ship the planner"}); err != nil {
		t.Fatal(err)
	}
	searcher := NewSearcher(db, nil)
	current, err := searcher.Lexical(ctx, "planner", 10)
	if err != nil || len(current.Hits) != 1 || current.Hits[0].Ref != "project:atlas" || !strings.Contains(current.Hits[0].Snippet, "\nGoal: Ship the planner\n") {
		t.Fatalf("current text = %#v, %v", current, err)
	}
	if removed, err := searcher.Lexical(ctx, "reranker", 10); err != nil || len(removed.Hits) != 0 {
		t.Fatalf("removed words still match the stale chunk: %#v, %v", removed, err)
	}
}

// Live text carries every field the indexer makes searchable (a project's tier,
// deadline, and hours, an entry's date and author) and is cut at the same
// chunk boundaries, so a match deep in a long entry shows the chunk holding it.
func TestLexicalLiveTextMatchesWhatTheIndexerWouldStore(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "maintain", HoursWK: 12, Deadline: "2026-11-15", Goal: "Ship it"}); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "note", "Measured the latency.", "codex", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := make([]string, 30)
	for i := range paragraphs {
		paragraphs[i] = fmt.Sprintf("Paragraph %d covers the benchmark setup, the warm cache, and the cold start numbers in detail.", i)
	}
	paragraphs[29] = "Finally the zeppelin experiment settled it."
	long, err := db.AppendEntry(ctx, "atlas", "note", strings.Join(paragraphs, "\n\n"), "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	entryRef, longRef := "entry:"+strconv.FormatInt(entry.ID, 10), "entry:"+strconv.FormatInt(long.ID, 10)
	searcher := NewSearcher(db, nil)
	deepOrd := -1
	for query, want := range map[string]string{"maintain": "project:atlas", "2026-11-15": "project:atlas", "codex": entryRef, "zeppelin": longRef} {
		result, err := searcher.Lexical(ctx, query, 10)
		if err != nil || len(result.Hits) != 1 || result.Hits[0].Ref != want {
			t.Fatalf("%q = %#v, %v", query, result, err)
		}
		if query == "zeppelin" {
			deepOrd = result.Hits[0].Ord
		}
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	rows, err := db.Pool.Query(ctx, `SELECT ref,kind,header,body FROM (`+liveDocuments+`) l`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var ref, kind, header, body string
		if err := rows.Scan(&ref, &kind, &header, &body); err != nil {
			t.Fatal(err)
		}
		want, err := buildRef(ctx, tx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if got := liveChunks(kind, header, body); !slices.Equal(got, want) {
			t.Fatalf("%s live chunks %#v, indexer %#v", ref, got, want)
		}
		if ref == longRef && (len(want) < 2 || deepOrd < 1 || !strings.Contains(want[deepOrd].Text, "zeppelin")) {
			t.Fatalf("a match deep in a long entry must come from the chunk holding it: chunk %d of %d", deepOrd, len(want))
		}
		seen++
	}
	if rows.Err() != nil || seen != 3 {
		t.Fatalf("compared %d live documents: %v", seen, rows.Err())
	}
}

// One entry with many matching chunks must not crowd others out before the limit.
func TestLexicalSearchCountsEachRefOnceBeforeTheLimit(t *testing.T) {
	db, ctx := testdb.Open(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	long, err := db.AppendEntry(ctx, "atlas", "note", "A long design note.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	short, err := db.AppendEntry(ctx, "atlas", "decision", "Use PostgreSQL.", "agent", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	longRef, shortRef := "entry:"+strconv.FormatInt(long.ID, 10), "entry:"+strconv.FormatInt(short.ID, 10)
	for ord := range 40 {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,$2,'[atlas | note]'||chr(10)||'PostgreSQL PostgreSQL PostgreSQL',decode(lpad(to_hex($2::int),64,'0'),'hex'),'qwen3-embedding')`, longRef, ord); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model) VALUES($1,0,'[atlas | decision]'||chr(10)||'Use PostgreSQL.',decode(repeat('ff',32),'hex'),'qwen3-embedding')`, shortRef); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty`); err != nil {
		t.Fatal(err)
	}
	result, err := NewSearcher(db, nil).Lexical(ctx, "postgresql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || result.Hits[0].Ref != longRef || result.Hits[1].Ref != shortRef {
		t.Fatalf("hits = %#v", result.Hits)
	}
}
