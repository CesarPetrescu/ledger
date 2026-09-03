package retrieval

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func NewHTTPHandler(searcher *Searcher, indexer *Indexer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /search", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Query string `json:"q"`
			Limit int    `json:"limit"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Query) == "" || input.Limit < 0 || input.Limit > 30 {
			http.Error(w, "q is required and limit must not exceed 30", http.StatusBadRequest)
			return
		}
		if input.Limit == 0 {
			input.Limit = 10
		}
		result, err := searcher.Search(r.Context(), input.Query, input.Limit)
		if err != nil {
			http.Error(w, "search failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /reindex", func(w http.ResponseWriter, r *http.Request) {
		count, err := indexer.QueueAll(r.Context())
		if err != nil {
			http.Error(w, "reindex failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"queued": count})
	})
	return mux
}
