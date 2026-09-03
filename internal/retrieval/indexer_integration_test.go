//go:build integration

package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func indexDB(t *testing.T) (*store.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	c, err := postgres.Run(ctx, "pgvector/pgvector:pg16", postgres.WithDatabase("ledger"), postgres.WithUsername("ledger"), postgres.WithPassword("ledger"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func fakeInfer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	requests := &atomic.Int64{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		data := make([]map[string]any, len(request.Input))
		for i := range request.Input {
			vector := make([]float32, 4096)
			vector[i%4096] = 1
			data[i] = map[string]any{"index": i, "embedding": vector}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})), requests
}

func TestEntryDirtyRebuildIsIdempotentAndProjectEditIsIsolated(t *testing.T) {
	db, ctx := indexDB(t)
	infer, embedRequests := fakeInfer(t)
	defer infer.Close()
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: 8}); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "decision", "Folosim PostgreSQL.", "test-client", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty WHERE ref LIKE 'project:%'`); err != nil {
		t.Fatal(err)
	}
	worker := NewIndexer(db, NewInferClient(infer.URL, "qwen3-embedding", "qwen3-reranker", 4096, ""))
	if n, err := worker.ProcessBatch(ctx, 50); err != nil || n != 1 {
		t.Fatalf("first pass = %d, %v", n, err)
	}
	var text string
	var embedded bool
	if err := db.Pool.QueryRow(ctx, `SELECT text,embedding IS NOT NULL FROM chunk WHERE ref=$1`, "entry:"+strconv.FormatInt(entry.ID, 10)).Scan(&text, &embedded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "[project: Atlas (atlas) | decision |") || !embedded {
		t.Fatalf("indexed chunk = %q, embedded=%v", text, embedded)
	}
	var xmin string
	if err := db.Pool.QueryRow(ctx, `SELECT xmin::text FROM chunk WHERE ref=$1`, "entry:"+strconv.FormatInt(entry.ID, 10)).Scan(&xmin); err != nil {
		t.Fatal(err)
	}
	firstEmbeds := embedRequests.Load()
	if _, err := db.Pool.Exec(ctx, `INSERT INTO chunk_dirty(ref) VALUES($1) ON CONFLICT(ref) DO UPDATE SET queued_at=now()`, "entry:"+strconv.FormatInt(entry.ID, 10)); err != nil {
		t.Fatal(err)
	}
	if n, err := worker.ProcessBatch(ctx, 50); err != nil || n != 1 {
		t.Fatalf("unchanged requeue = %d, %v", n, err)
	}
	var secondXmin string
	if err := db.Pool.QueryRow(ctx, `SELECT xmin::text FROM chunk WHERE ref=$1`, "entry:"+strconv.FormatInt(entry.ID, 10)).Scan(&secondXmin); err != nil {
		t.Fatal(err)
	}
	if embedRequests.Load() != firstEmbeds || secondXmin != xmin {
		t.Fatalf("unchanged ref churned: embeds %d -> %d, xmin %s -> %s", firstEmbeds, embedRequests.Load(), xmin, secondXmin)
	}
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas Nou", Tier: "maintain", HoursWK: 4}); err != nil {
		t.Fatal(err)
	}
	var refs []string
	rows, err := db.Pool.Query(ctx, `SELECT ref FROM chunk_dirty ORDER BY ref`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		_ = rows.Scan(&ref)
		refs = append(refs, ref)
	}
	if len(refs) != 1 || refs[0] != "project:atlas" {
		t.Fatalf("dirty refs after edit = %v", refs)
	}
}

func TestConcurrentWorkersProcessEachDirtyRefExactlyOnce(t *testing.T) {
	db, parent := indexDB(t)
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	const refs = 6
	for i := 0; i < refs; i++ {
		slug := fmt.Sprintf("project-%d", i)
		if _, err := db.UpsertProject(ctx, store.Project{Slug: slug, Name: slug, Tier: "focus"}); err != nil {
			t.Fatal(err)
		}
	}

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	embedded := map[string]int{}
	infer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		select {
		case started <- struct{}{}:
			<-release
		default:
		}
		mu.Lock()
		for _, text := range request.Input {
			embedded[text]++
		}
		mu.Unlock()
		data := make([]map[string]any, len(request.Input))
		for i := range request.Input {
			data[i] = map[string]any{"index": i, "embedding": make([]float32, 4096)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer infer.Close()
	client := NewInferClient(infer.URL, "qwen3-embedding", "qwen3-reranker", 4096, "")
	type outcome struct {
		processed int
		err       error
	}
	outcomes := make(chan outcome, 2)
	for range 2 {
		go func() {
			processed, err := NewIndexer(db, client).ProcessBatch(ctx, 50)
			outcomes <- outcome{processed, err}
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not overlap")
		}
	}
	close(release)
	processed := 0
	for range 2 {
		result := <-outcomes
		if result.err != nil {
			t.Fatal(result.err)
		}
		processed += result.processed
	}
	var chunks, dirty int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM chunk WHERE ref LIKE 'project:%'`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM chunk_dirty`).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if processed != refs || chunks != refs || dirty != 0 || len(embedded) != refs {
		t.Fatalf("processed=%d chunks=%d dirty=%d unique embeds=%d", processed, chunks, dirty, len(embedded))
	}
	for text, count := range embedded {
		if count != 1 {
			t.Errorf("embedded %d times: %q", count, text)
		}
	}
}

func TestIndexerRunReconnectsAfterListenerConnectionLoss(t *testing.T) {
	db, parent := indexDB(t)
	infer, _ := fakeInfer(t)
	defer infer.Close()
	worker := NewIndexer(db, NewInferClient(infer.URL, "qwen3-embedding", "qwen3-reranker", 4096, ""))
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	var listenerPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := db.Pool.QueryRow(parent, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND query='LISTEN chunk_dirty' AND pid<>pg_backend_pid() ORDER BY query_start DESC LIMIT 1`).Scan(&listenerPID)
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if listenerPID == 0 {
		t.Fatal("indexer did not establish LISTEN connection")
	}
	if _, err := db.Pool.Exec(parent, `SELECT pg_terminate_backend($1)`, listenerPID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("indexer stopped after listener connection loss: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}

	if _, err := db.UpsertProject(parent, store.Project{Slug: "recovered", Name: "Recovered", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var exists bool
		if err := db.Pool.QueryRow(parent, `SELECT EXISTS(SELECT 1 FROM chunk WHERE ref='project:recovered' AND embedding IS NOT NULL)`).Scan(&exists); err == nil && exists {
			cancel()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("indexer did not reconnect and process notified work")
}

func TestEmbeddingFailureCommitsFTSAndKeepsDirty(t *testing.T) {
	db, ctx := indexDB(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus", HoursWK: 8}); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "decision", "Decizia zmeură rămâne căutabilă lexical.", "test-client", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM chunk_dirty WHERE ref LIKE 'project:%'`); err != nil {
		t.Fatal(err)
	}
	infer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": .9}}})
	}))
	defer infer.Close()
	client := NewInferClient(infer.URL, "qwen3-embedding", "qwen3-reranker", 4096, "")
	worker := NewIndexer(db, client)
	if _, err := worker.ProcessBatch(ctx, 1); err == nil {
		t.Fatal("embedding failure was not reported")
	}
	ref := "entry:" + strconv.FormatInt(entry.ID, 10)
	var text string
	var embedded, dirty bool
	if err := db.Pool.QueryRow(ctx, `SELECT text,embedding IS NOT NULL FROM chunk WHERE ref=$1`, ref).Scan(&text, &embedded); err != nil {
		t.Fatalf("FTS fallback chunk missing: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chunk_dirty WHERE ref=$1)`, ref).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "zmeură") || embedded || !dirty {
		t.Fatalf("fallback chunk = %q, embedded=%v, dirty=%v", text, embedded, dirty)
	}
	result, err := NewSearcher(db, client).Search(ctx, "zmeura", 10)
	if err != nil || len(result.Hits) != 1 || result.Hits[0].Ref != ref || len(result.Degraded) != 1 || result.Degraded[0] != "vector" {
		t.Fatalf("degraded FTS = %#v, %v", result, err)
	}
}

func TestEmbeddingFailuresDoNotStarveSiblingDirtyRefs(t *testing.T) {
	db, ctx := indexDB(t)
	if _, err := db.UpsertProject(ctx, store.Project{Slug: "atlas", Name: "Atlas", Tier: "focus"}); err != nil {
		t.Fatal(err)
	}
	entry, err := db.AppendEntry(ctx, "atlas", "note", "Zmeură lexicală.", "test", "client")
	if err != nil {
		t.Fatal(err)
	}
	infer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer infer.Close()
	worker := NewIndexer(db, NewInferClient(infer.URL, "qwen3-embedding", "qwen3-reranker", 4096, ""))
	if processed, err := worker.ProcessBatch(ctx, 50); err == nil || processed != 2 {
		t.Fatalf("failed batch = processed %d, err %v", processed, err)
	}
	var chunks, dirty int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM chunk WHERE ref IN ($1,$2)`, "project:atlas", "entry:"+strconv.FormatInt(entry.ID, 10)).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM chunk_dirty WHERE ref IN ($1,$2)`, "project:atlas", "entry:"+strconv.FormatInt(entry.ID, 10)).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if chunks != 2 || dirty != 2 {
		t.Fatalf("failed sibling refs = chunks %d dirty %d", chunks, dirty)
	}
}
