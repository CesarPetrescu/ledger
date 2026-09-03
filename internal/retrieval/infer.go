package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

type InferClient struct {
	baseURL, embeddingModel, rerankModel, apiKey string
	dim                                          int
	http                                         *http.Client
}

type RerankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"relevance_score"`
}

func NewInferClient(baseURL, embeddingModel, rerankModel string, dim int, apiKey string) *InferClient {
	return &InferClient{baseURL: strings.TrimRight(baseURL, "/"), embeddingModel: embeddingModel, rerankModel: rerankModel, dim: dim, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *InferClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	payload := struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{c.embeddingModel, texts}
	var response struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = c.post(ctx, "/embeddings", payload, &response)
		if err == nil {
			break
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(100<<attempt) * time.Millisecond):
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(response.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(response.Data), len(texts))
	}
	sort.Slice(response.Data, func(i, j int) bool { return response.Data[i].Index < response.Data[j].Index })
	out := make([][]float32, len(response.Data))
	for i, item := range response.Data {
		if item.Index != i || len(item.Embedding) != c.dim {
			return nil, fmt.Errorf("embeddings: vector %d has dimension %d, want %d", item.Index, len(item.Embedding), c.dim)
		}
		out[i] = item.Embedding
	}
	return out, nil
}

func (c *InferClient) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	if topN < 1 || topN > len(documents) {
		return nil, fmt.Errorf("rerank: invalid top_n %d for %d documents", topN, len(documents))
	}
	payload := struct {
		Model     string   `json:"model"`
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
		TopN      int      `json:"top_n"`
	}{c.rerankModel, query, documents, topN}
	var response struct {
		Results []struct {
			Index *int     `json:"index"`
			Score *float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := c.post(ctx, "/rerank", payload, &response); err != nil {
		return nil, err
	}
	if len(response.Results) != topN {
		return nil, fmt.Errorf("rerank: got %d results, want %d", len(response.Results), topN)
	}
	results := make([]RerankResult, len(response.Results))
	seen := make([]bool, len(documents))
	for i, raw := range response.Results {
		if raw.Index == nil || raw.Score == nil {
			return nil, fmt.Errorf("rerank: result %d is missing index or relevance_score", i)
		}
		result := RerankResult{Index: *raw.Index, Score: *raw.Score}
		if result.Index < 0 || result.Index >= len(documents) {
			return nil, fmt.Errorf("rerank: invalid document index %d", result.Index)
		}
		if seen[result.Index] {
			return nil, fmt.Errorf("rerank: duplicate document index %d", result.Index)
		}
		if math.IsNaN(result.Score) || math.IsInf(result.Score, 0) {
			return nil, fmt.Errorf("rerank: non-finite score for document index %d", result.Index)
		}
		seen[result.Index] = true
		results[i] = result
	}
	return results, nil
}

func (c *InferClient) post(ctx context.Context, path string, payload, response any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, res.Body)
		return fmt.Errorf("inference: HTTP %s", res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(response); err != nil {
		return fmt.Errorf("inference response: %w", err)
	}
	return nil
}
