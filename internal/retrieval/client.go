package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls the internal ledger-index HTTP API.
type Client struct {
	base string
	http *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Search(ctx context.Context, query string, limit int) (SearchResult, error) {
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
