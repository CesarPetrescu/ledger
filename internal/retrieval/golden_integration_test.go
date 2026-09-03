//go:build integration

package retrieval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/cesarpetrescu/ledger/internal/store"
)

var concepts = [][]string{
	{"bucatarie", "bucătărie", "kitchen", "renovare", "renovation"},
	{"factura", "factură", "invoice", "furnizor", "supplier"},
	{"buget", "budget", "marketing"},
	{"gradina", "grădină", "garden", "irigatie", "irigație", "irrigation"},
	{"aplicatie", "aplicație", "application", "lansare", "launch"},
}

func normalized(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'ă', 'â':
			return 'a'
		case 'î':
			return 'i'
		case 'ș':
			return 's'
		case 'ț':
			return 't'
		}
		return unicode.ToLower(r)
	}, s)
}

func conceptFor(s string) int {
	s = normalized(s)
	for i, words := range concepts {
		for _, word := range words {
			if strings.Contains(s, normalized(word)) {
				return i
			}
		}
	}
	return len(concepts)
}

func semanticInfer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings":
			var input struct {
				Input []string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			data := make([]map[string]any, len(input.Input))
			for i, text := range input.Input {
				vector := make([]float32, 4096)
				vector[conceptFor(text)] = 1
				data[i] = map[string]any{"index": i, "embedding": vector}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/rerank":
			var input struct {
				Query     string   `json:"query"`
				Documents []string `json:"documents"`
				TopN      int      `json:"top_n"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			type scored struct {
				index int
				score float64
			}
			items := make([]scored, len(input.Documents))
			wanted := conceptFor(input.Query)
			for i, document := range input.Documents {
				items[i] = scored{index: i, score: .1}
				if conceptFor(document) == wanted {
					items[i].score = .9
				}
			}
			sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
			results := make([]map[string]any, min(input.TopN, len(items)))
			for i, item := range items[:len(results)] {
				results[i] = map[string]any{"index": item.index, "relevance_score": item.score}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestThirtyEntriesTenBilingualGoldenQueries(t *testing.T) {
	db, ctx := indexDB(t)
	for i := 0; i < 5; i++ {
		slug := "project-" + string(rune('a'+i))
		if _, err := db.UpsertProject(ctx, store.Project{Slug: slug, Name: "Project " + string(rune('A'+i)), Tier: "focus", HoursWK: i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	targetBodies := []string{
		"Decizia pentru renovare bucătărie este să păstrăm mobilierul.",
		"Factura de la furnizor se plătește vineri.",
		"Bugetul de marketing aprobat este zece mii de lei.",
		"Grădină: instalăm sistemul de irigație în aprilie.",
		"Lansarea noii aplicații este programată pentru octombrie.",
	}
	targets := make([]string, len(targetBodies))
	for i, body := range targetBodies {
		entry, err := db.AppendEntry(ctx, "project-"+string(rune('a'+i)), "decision", body, "seed", "seed-client")
		if err != nil {
			t.Fatal(err)
		}
		targets[i] = "entry:" + strconv.FormatInt(entry.ID, 10)
	}
	for i := 0; i < 25; i++ {
		if _, err := db.AppendEntry(ctx, "project-a", "note", "Notă administrativă neutră numărul "+strconv.Itoa(i)+".", "seed", "seed-client"); err != nil {
			t.Fatal(err)
		}
	}
	inference := semanticInfer(t)
	defer inference.Close()
	client := NewInferClient(inference.URL, "qwen3-embedding", "qwen3-reranker", 4096, "")
	worker := NewIndexer(db, client)
	if n, err := worker.ProcessBatch(ctx, 50); err != nil || n != 35 {
		t.Fatalf("indexed = %d, %v", n, err)
	}
	searcher := NewSearcher(db, client)
	queries := []struct {
		text   string
		target int
	}{
		{"renovare bucătărie", 0}, {"factura furnizor", 1}, {"buget marketing", 2}, {"gradina irigație", 3}, {"lansare aplicație", 4},
		{"kitchen renovation", 0}, {"supplier invoice", 1}, {"marketing budget", 2}, {"garden irrigation", 3}, {"application launch", 4},
	}
	for _, query := range queries {
		result, err := searcher.Search(ctx, query.text, 3)
		if err != nil {
			t.Fatalf("%q: %v", query.text, err)
		}
		found := false
		for _, hit := range result.Hits {
			found = found || hit.Ref == targets[query.target]
		}
		if !found {
			t.Errorf("%q top 3 = %#v, want %s", query.text, result.Hits, targets[query.target])
		}
	}
}
