package retrieval

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

const queryInstruction = "Instruct: Retrieve project notes and decisions that answer the query\nQuery: "

type SearchResult struct {
	Hits     []Ranked `json:"hits"`
	Degraded []string `json:"degraded"`
}

type Searcher struct {
	db    *store.DB
	infer *InferClient
}

func NewSearcher(db *store.DB, infer *InferClient) *Searcher { return &Searcher{db: db, infer: infer} }

const rankedColumns = `c.ref,c.ord,COALESCE(e.kind,'project'),COALESCE(e.slug,substring(c.ref from 9)),c.text`

func (s *Searcher) Search(ctx context.Context, query string, limit int) (SearchResult, error) {
	fts, err := s.fts(ctx, query)
	if err != nil {
		return SearchResult{}, err
	}
	degraded := []string{}
	vector := []Ranked{}
	vectorCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	embeddings, embedErr := s.infer.Embed(vectorCtx, []string{queryInstruction + query})
	if embedErr != nil {
		degraded = append(degraded, "vector")
	} else {
		vector, err = s.vector(vectorCtx, embeddings[0])
		if err != nil {
			cancel()
			return SearchResult{}, err
		}
	}
	cancel()
	fused := RRF(fts, vector, 60)
	seen := map[string]bool{}
	candidates := make([]Ranked, 0, min(20, len(fused)))
	for _, hit := range fused {
		if seen[hit.Ref] {
			continue
		}
		seen[hit.Ref] = true
		candidates = append(candidates, hit)
		if len(candidates) == 20 {
			break
		}
	}
	if len(candidates) == 0 {
		return SearchResult{Hits: []Ranked{}, Degraded: degraded}, nil
	}
	documents := make([]string, len(candidates))
	for i := range candidates {
		documents[i] = candidates[i].Snippet
	}
	rerankCtx, rerankCancel := context.WithTimeout(ctx, 3*time.Second)
	reranked, rerankErr := s.infer.Rerank(rerankCtx, query, documents, min(limit, len(documents)))
	rerankCancel()
	if rerankErr != nil {
		degraded = append(degraded, "rerank")
		for i := range candidates {
			candidates[i].Snippet = snippet(candidates[i].Snippet)
		}
		return SearchResult{Hits: candidates[:min(limit, len(candidates))], Degraded: degraded}, nil
	}
	hits := make([]Ranked, 0, min(limit, len(reranked)))
	for _, result := range reranked {
		hit := candidates[result.Index]
		hit.Score = result.Score
		hit.Snippet = snippet(hit.Snippet)
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Ref < hits[j].Ref
	})
	return SearchResult{Hits: hits, Degraded: degraded}, nil
}

// Lexical ranks Postgres full-text matches alone and never calls the
// inference service (the Searcher may have none), so it still answers while
// ledger-index is unreachable. It reads the stored chunks of any embedding
// model, skipping chunks of deleted entries and projects that the stopped
// indexer has not dropped yet. Entries and projects the indexer never chunked,
// or that changed since (still queued in chunk_dirty), are matched on their
// live text instead of a missing or stale chunk. Each ref counts once, by its
// best chunk, before the limit. The result reports the vector and rerank
// stages as degraded.
//
// ponytail: live rows are tokenized per query with no index; fine while the
// backlog is small, add a GIN index on entry body if installs run without an
// indexer for long.
func (s *Searcher) Lexical(ctx context.Context, query string, limit int) (SearchResult, error) {
	rows, err := s.db.Pool.Query(ctx, `WITH q AS (SELECT websearch_to_tsquery('public.ledger_ts'::regconfig,$1) q),
stale AS (SELECT ref FROM chunk_dirty),
live AS (
 SELECT 'entry:'||e.id ref,e.kind,e.slug,'[project: '||p.name||' ('||e.slug||') | '||e.kind||']'||E'\n'||e.body text
 FROM entry e JOIN project p ON p.slug=e.slug
 WHERE NOT EXISTS(SELECT 1 FROM chunk c WHERE c.ref='entry:'||e.id) OR 'entry:'||e.id IN (SELECT ref FROM stale)
 UNION ALL
 SELECT 'project:'||p.slug,'project',p.slug,'[project: '||p.name||' ('||p.slug||')]'||E'\n'||concat_ws(E'\n',NULLIF(p.type,''),NULLIF(p.description,''),NULLIF(p.goal,''),NULLIF(p.needs_me,''),NULLIF(p.automate,''),NULLIF(p.stack,''))
 FROM project p
 WHERE NOT EXISTS(SELECT 1 FROM chunk c WHERE c.ref='project:'||p.slug) OR 'project:'||p.slug IN (SELECT ref FROM stale)
),
matches AS (
 SELECT c.ref,c.ord,COALESCE(e.kind,'project') kind,COALESCE(e.slug,substring(c.ref from 9)) slug,c.text,ts_rank_cd(c.tsv,q.q) score
 FROM q,chunk c LEFT JOIN entry e ON c.ref='entry:'||e.id::text
 WHERE (e.id IS NOT NULL OR EXISTS(SELECT 1 FROM project p WHERE c.ref='project:'||p.slug)) AND c.ref NOT IN (SELECT ref FROM stale) AND c.tsv @@ q.q
 UNION ALL
 SELECT l.ref,0,l.kind,l.slug,l.text,ts_rank_cd(to_tsvector('public.ledger_ts'::regconfig,l.text),q.q)
 FROM q,live l WHERE to_tsvector('public.ledger_ts'::regconfig,l.text) @@ q.q
)
SELECT ref,ord,kind,slug,text,score FROM (SELECT DISTINCT ON (ref) * FROM matches ORDER BY ref,score DESC,ord) best
ORDER BY score DESC,ref LIMIT $2`, query, limit)
	if err != nil {
		return SearchResult{}, err
	}
	hits, err := scanRanked(rows)
	rows.Close()
	if err != nil {
		return SearchResult{}, err
	}
	for i := range hits {
		hits[i].Snippet = snippet(hits[i].Snippet)
	}
	return SearchResult{Hits: hits, Degraded: []string{"vector", "rerank"}}, nil
}

// fts returns the 30 best full-text matches among chunks of the current model.
func (s *Searcher) fts(ctx context.Context, query string) ([]Ranked, error) {
	rows, err := s.db.Pool.Query(ctx, `SELECT `+rankedColumns+`,ts_rank_cd(c.tsv,websearch_to_tsquery('public.ledger_ts'::regconfig,$1)) score
FROM chunk c LEFT JOIN entry e ON c.ref='entry:'||e.id::text
WHERE c.model=$2 AND c.tsv @@ websearch_to_tsquery('public.ledger_ts'::regconfig,$1)
ORDER BY score DESC,c.ref,c.ord LIMIT 30`, query, s.infer.embeddingModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRanked(rows)
}

func (s *Searcher) vector(ctx context.Context, embedding []float32) ([]Ranked, error) {
	rows, err := s.db.Pool.Query(ctx, `SELECT `+rankedColumns+`,1-(c.embedding <=> $1) score
FROM chunk c LEFT JOIN entry e ON c.ref='entry:'||e.id::text
WHERE c.model=$2 AND c.embedding IS NOT NULL
ORDER BY c.embedding <=> $1,c.ref,c.ord LIMIT 30`, pgvector.NewHalfVector(embedding), s.infer.embeddingModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRanked(rows)
}

func scanRanked(rows pgx.Rows) ([]Ranked, error) {
	out := []Ranked{}
	for rows.Next() {
		var hit Ranked
		if err := rows.Scan(&hit.Ref, &hit.Ord, &hit.Kind, &hit.ProjectSlug, &hit.Snippet, &hit.Score); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func snippet(text string) string {
	if _, body, ok := strings.Cut(text, "\n"); ok {
		text = body
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= 300 {
		return text
	}
	return string([]rune(text)[:299]) + "…"
}
