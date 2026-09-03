package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestInferRejectsWrongDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{1, 2}}}})
	}))
	defer server.Close()
	client := NewInferClient(server.URL, "embed", "rerank", 3, "")
	if _, err := client.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("wrong embedding dimensions accepted")
	}
}

func TestRerankRequestShape(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"index": 1, "relevance_score": .9}}})
	}))
	defer server.Close()
	client := NewInferClient(server.URL, "embed", "qwen3-reranker", 3, "")
	got, err := client.Rerank(context.Background(), "query", []string{"a", "b"}, 1)
	if err != nil || len(got) != 1 || got[0].Index != 1 {
		t.Fatalf("rerank = %#v, %v", got, err)
	}
	want := map[string]any{"model": "qwen3-reranker", "query": "query", "documents": []any{"a", "b"}, "top_n": float64(1)}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v, want %#v", body, want)
	}
}

func TestRerankRejectsMalformedResults(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "empty", response: `{"results":[]}`},
		{name: "missing index", response: `{"results":[{"relevance_score":0.9},{"index":1,"relevance_score":0.8}]}`},
		{name: "missing score", response: `{"results":[{"index":0},{"index":1,"relevance_score":0.8}]}`},
		{name: "short", response: `{"results":[{"index":0,"relevance_score":0.9}]}`},
		{name: "long", response: `{"results":[{"index":0,"relevance_score":0.9},{"index":1,"relevance_score":0.8},{"index":2,"relevance_score":0.7}]}`},
		{name: "duplicate", response: `{"results":[{"index":0,"relevance_score":0.9},{"index":0,"relevance_score":0.8}]}`},
		{name: "negative", response: `{"results":[{"index":-1,"relevance_score":0.9},{"index":1,"relevance_score":0.8}]}`},
		{name: "out of range", response: `{"results":[{"index":0,"relevance_score":0.9},{"index":3,"relevance_score":0.8}]}`},
		{name: "NaN", response: `{"results":[{"index":0,"relevance_score":NaN},{"index":1,"relevance_score":0.8}]}`},
		{name: "Inf", response: `{"results":[{"index":0,"relevance_score":1e999},{"index":1,"relevance_score":0.8}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, test.response)
			}))
			defer server.Close()
			client := NewInferClient(server.URL, "embed", "rerank", 3, "")
			if results, err := client.Rerank(context.Background(), "query", []string{"a", "b", "c"}, 2); err == nil {
				t.Fatalf("accepted %#v", results)
			}
		})
	}
}
