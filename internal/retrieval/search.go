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
