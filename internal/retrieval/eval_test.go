//go:build eval

package retrieval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
)

// evalCase is one hand-labelled entry. Only the expectations present are
// scored; ask and chosen are scored as present or absent.
type evalCase struct {
	Project   string `json:"project"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	WrittenOn string `json:"written_on"`
	Body      string `json:"body"`
	Expect    struct {
		Importance string   `json:"importance"`
		Priority   string   `json:"priority"`
		State      string   `json:"state"`
		Size       string   `json:"size"`
		Ask        *bool    `json:"ask"`
		Chosen     *bool    `json:"chosen"`
		Tags       []string `json:"tags"`
	} `json:"expect"`
}

// TestEvalLabels scores the extractor against a labelled set. It calls the
// real chat endpoint and is run by hand:
//
//	LEDGER_EVAL_FILE=.private/eval/labels.jsonl LEDGER_CHAT_URL=http://host:port/v1 \
//	  go test -tags=eval -run TestEvalLabels -v ./internal/retrieval
//
// Keep real entries out of the repository (.private/ is ignored);
// testdata/eval_sample.jsonl shows the format with fictional entries.
func TestEvalLabels(t *testing.T) {
	file, chatURL := os.Getenv("LEDGER_EVAL_FILE"), os.Getenv("LEDGER_CHAT_URL")
	if file == "" || chatURL == "" {
		t.Skip("set LEDGER_EVAL_FILE and LEDGER_CHAT_URL")
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	x := NewExtractor(nil, chatURL, os.Getenv("LEDGER_CHAT_MODEL"), os.Getenv("LEDGER_CHAT_API_KEY"), nil, 0.9)
	hits, totals := map[string]int{}, map[string]int{}
	score := func(field string, ok bool, detail string) {
		totals[field]++
		if ok {
			hits[field]++
		} else {
			t.Logf("  %s: %s", field, detail)
		}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; scanner.Scan(); n++ {
		var c evalCase
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		written, _ := time.Parse(time.DateOnly, c.WrittenOn)
		entry := &store.PendingEntry{ID: int64(n), Slug: c.Project, ProjectName: c.Project, Kind: c.Kind, Body: c.Body, Source: c.Source, CreatedAt: written}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		meta, err := x.extract(ctx, entry, nil, nil, nil, nil)
		cancel()
		if err != nil {
			t.Errorf("line %d: %v", n, err)
			continue
		}
		t.Logf("%d %s %q unsure=%v", n, c.Kind, meta.Title, meta.Unsure)
		exact := func(field, want, got string) {
			if want != "" {
				score(field, want == got, fmt.Sprintf("want %q got %q", want, got))
			}
		}
		exact("importance", c.Expect.Importance, meta.Importance)
		exact("priority", c.Expect.Priority, meta.Priority)
		exact("state", c.Expect.State, meta.State)
		exact("size", c.Expect.Size, meta.Size)
		if c.Expect.Ask != nil {
			score("ask", *c.Expect.Ask == (meta.Ask != ""), fmt.Sprintf("want ask=%v got %q", *c.Expect.Ask, meta.Ask))
		}
		if c.Expect.Chosen != nil {
			score("chosen", *c.Expect.Chosen == (meta.Details.Chosen != ""), fmt.Sprintf("want chosen=%v got %q", *c.Expect.Chosen, meta.Details.Chosen))
		}
		for _, tag := range c.Expect.Tags {
			score("tags", strings.Contains(" "+strings.Join(meta.Tags, " ")+" ", " "+tag+" "), fmt.Sprintf("want %q in %v", tag, meta.Tags))
		}
		score("title", meta.Title != "" && len([]rune(meta.Title)) <= 60, fmt.Sprintf("title %q", meta.Title))
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 0, len(totals))
	for field := range totals {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	all, allHits := 0, 0
	for _, field := range fields {
		t.Logf("%-10s %3d/%-3d %5.1f%%", field, hits[field], totals[field], 100*float64(hits[field])/float64(totals[field]))
		all, allHits = all+totals[field], allHits+hits[field]
	}
	if all > 0 {
		t.Logf("%-10s %3d/%-3d %5.1f%%", "overall", allHits, all, 100*float64(allHits)/float64(all))
	}
}
