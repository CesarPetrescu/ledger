package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
)

// Client calls the internal ledger-index HTTP API. With a database, Search
// falls back to Postgres full-text search when ledger-index cannot answer.
type Client struct {
	base    string
	http    *http.Client
	lexical *Searcher
}

// NewClient returns a ledger-index client; db may be nil, which disables the fallback.
func NewClient(baseURL string, db *store.DB) *Client {
	client := &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 15 * time.Second}}
	if db != nil {
		// Lexical search needs no inference client.
		client.lexical = NewSearcher(db, nil)
	}
	return client
}

// Search asks ledger-index for fused, reranked hits. If the service is down or
// fails, it returns word matches from Postgres instead, marked degraded like
// an inference outage, so callers see the same result shape either way.
func (c *Client) Search(ctx context.Context, query string, limit int) (SearchResult, error) {
	result, err := c.remote(ctx, query, limit)
	if err == nil || c.lexical == nil || ctx.Err() != nil {
		return result, err
	}
	log.Printf("index search failed; using word matches only: %v", err)
	fallback, lexicalErr := c.lexical.Lexical(ctx, query, limit)
	if lexicalErr != nil {
		return SearchResult{}, errors.Join(err, lexicalErr)
	}
	return fallback, nil
}

func (c *Client) remote(ctx context.Context, query string, limit int) (SearchResult, error) {
	body, _ := json.Marshal(map[string]any{"q": query, "limit": limit})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/search", bytes.NewReader(body))
	if err != nil {
		return SearchResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return SearchResult{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return SearchResult{}, fmt.Errorf("index search returned %s", res.Status)
	}
	var result SearchResult
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&result); err != nil {
		return SearchResult{}, err
	}
	if result.Hits == nil {
		result.Hits = []Ranked{}
	}
	if result.Degraded == nil {
		result.Degraded = []string{}
	}
	return result, nil
}
