package retrieval

import (
	"fmt"
	"strings"
	"testing"
)

func TestChunkEntryBoundsOverlapAndDeterminism(t *testing.T) {
	body := strings.Repeat("Aceasta este o propoziție românească suficient de lungă pentru testare. ", 75)
	first := ChunkEntry("[project: Atlas (atlas) | decision | 2025-01-15 | by test-client]", body)
	second := ChunkEntry("[project: Atlas (atlas) | decision | 2025-01-15 | by test-client]", body)
	if len(first) < 2 {
		t.Fatalf("got %d chunks, want multiple", len(first))
	}
	for i, chunk := range first {
		if EstimateTokens(chunk.Text) > 400 {
			t.Errorf("chunk %d has %d estimated tokens", i, EstimateTokens(chunk.Text))
		}
		if chunk.Ord != i || chunk.Hash == "" {
			t.Errorf("chunk %d metadata = ord %d hash %q", i, chunk.Ord, chunk.Hash)
		}
	}
	if first[0].Hash != second[0].Hash || first[0].Text != second[0].Text {
		t.Fatal("chunking is not deterministic")
	}
	last := strings.Fields(first[0].Text)
	if overlap := strings.Join(last[len(last)-45:], " "); !strings.Contains(first[1].Text, overlap) {
		t.Fatal("second chunk does not retain roughly 60 estimated tokens of overlap")
	}
}

func TestChunkEntryAtMost450TokensIsOneChunk(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	body := strings.Repeat("x", 1480)
	if got := EstimateTokens(header + "\n" + body); got <= 400 || got > 450 {
		t.Fatalf("test fixture has %d tokens, want 401..450", got)
	}
	if chunks := ChunkEntry(header, body); len(chunks) != 1 {
		t.Fatalf("entry at most 450 tokens produced %d chunks", len(chunks))
	}
}

func TestChunkEntryLargeSentencesRetainOverlap(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	body := strings.Repeat("alpha ", 140) + ". " + strings.Repeat("beta ", 140) + ". " + strings.Repeat("gamma ", 140) + "."
	chunks := ChunkEntry(header, body)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want multiple", len(chunks))
	}
	for i := 1; i < len(chunks); i++ {
		previous := strings.TrimPrefix(chunks[i-1].Text, header+"\n")
		current := strings.TrimPrefix(chunks[i].Text, header+"\n")
		runes := []rune(previous)
		overlap := string(runes[max(0, len(runes)-210):])
		if !strings.Contains(current, strings.TrimSpace(overlap)) {
			t.Fatalf("chunk %d lacks approximately 60 estimated tokens of overlap", i)
		}
	}
}

func TestChunkEntryLongSentenceRetainsOverlap(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	var body strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&body, "word%04d ", i)
	}
	chunks := ChunkEntry(header, body.String())
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want multiple", len(chunks))
	}
	for i, chunk := range chunks {
		if got := EstimateTokens(chunk.Text); got > 400 {
			t.Fatalf("chunk %d has %d estimated tokens", i, got)
		}
	}
	firstBody := strings.TrimPrefix(chunks[0].Text, header+"\n")
	secondBody := strings.TrimPrefix(chunks[1].Text, header+"\n")
	words := strings.Fields(firstBody)
	overlap := strings.Join(words[max(0, len(words)-24):], " ")
	if !strings.Contains(secondBody, overlap) {
		t.Fatalf("second chunk lacks overlap suffix %q", overlap)
	}
}

func TestChunkEntryNoWhitespaceMakesBoundedProgress(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	chunks := ChunkEntry(header, strings.Repeat("x", 4000))
	if len(chunks) > 5 {
		t.Fatalf("no-whitespace input produced %d chunks", len(chunks))
	}
	for i, chunk := range chunks {
		if got := EstimateTokens(chunk.Text); got > 400 {
			t.Fatalf("chunk %d has %d estimated tokens", i, got)
		}
	}
}

func TestChunkEntryKeepsFencesAndListItemsAtomic(t *testing.T) {
	body := strings.Repeat("Propoziție de umplere pentru primul segment. ", 70) +
		"\n```go\nfunc main() { println(\"nu mă rupe\") }\n```\n" +
		strings.Repeat("Altă propoziție de umplere. ", 70) +
		"\n- un element de listă care trebuie să rămână întreg\n  inclusiv continuarea sa indentată\n"
	chunks := ChunkEntry("[project: Test (test) | note | 2025-01-15 | by test]", body)
	for _, chunk := range chunks {
		if strings.Contains(chunk.Text, "```go") != strings.Contains(chunk.Text, "```\n") {
			t.Fatalf("fenced block split across chunk: %q", chunk.Text)
		}
		if strings.Contains(chunk.Text, "un element de listă") && !strings.Contains(chunk.Text, "un element de listă care trebuie să rămână întreg") {
			t.Fatal("list item was split")
		}
		if strings.Contains(chunk.Text, "un element de listă") != strings.Contains(chunk.Text, "continuarea sa indentată") {
			t.Fatal("multi-line list item was split")
		}
	}
}

func TestChunkEntryKeepsOversizedProtectedUnitsAtomic(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	fence := "```text\n" + strings.Repeat("cod protejat ", 180) + "\n```"
	list := "- " + strings.Repeat("element protejat ", 180)
	chunks := ChunkEntry(header, "Înainte.\n"+fence+"\nDupă.\n"+list)
	joined := ""
	for _, chunk := range chunks {
		joined += chunk.Text
	}
	if !strings.Contains(joined, strings.TrimSpace(fence)) || !strings.Contains(joined, strings.TrimSpace(list)) {
		t.Fatalf("oversized protected unit was split: %#v", chunks)
	}
}

func TestEveryProjectChunkRetainsContextHeader(t *testing.T) {
	header := "[project: Atlas (atlas) | tier: focus | deadline: Friday]"
	text := header + "\n" + strings.Repeat("Paragraf contextual suficient de lung pentru împărțire.\n\n", 180)
	chunks := ProjectChunks(text)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want multiple", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.HasPrefix(chunk.Text, header+"\n") {
			t.Errorf("chunk %d lost project header: %q", i, chunk.Text[:min(len(chunk.Text), 100)])
		}
	}
}

func TestProjectChunksKeepProtectedUnitsAtomic(t *testing.T) {
	header := "[project: Atlas (atlas) | tier: focus | deadline: Friday]"
	fence := "```text\n" + strings.Repeat("cod protejat ", 120) + "\n\n" + strings.Repeat("continuare protejată ", 120) + "\n```"
	list := "- " + strings.Repeat("element protejat ", 180)
	chunks := ProjectChunks(header + "\nÎnainte.\n\n" + fence + "\n\nDupă.\n\n" + list)
	for _, protected := range []string{fence, strings.TrimSpace(list)} {
		found := false
		for _, chunk := range chunks {
			found = found || strings.Contains(chunk.Text, protected)
		}
		if !found {
			t.Fatalf("project protected unit was split: %q", protected[:min(len(protected), 80)])
		}
	}
}

func TestChunkersKeepTabSeparatedListItemsAtomic(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	item := "-	" + strings.Repeat("tab-separated protected item. ", 100)
	protected := strings.TrimSpace(item)
	body := strings.Repeat("Preface sentence. ", 90) + "\n" + item + "\n\nTail."
	for name, chunks := range map[string][]Chunk{
		"entry":   ChunkEntry(header, body),
		"project": ProjectChunks(header + "\n" + body),
	} {
		found := false
		for _, chunk := range chunks {
			found = found || strings.Contains(chunk.Text, protected)
		}
		if !found {
			t.Errorf("%s chunker split tab-separated list item", name)
		}
	}
}

func TestChunkersSeparateSiblingListItemsByIndentation(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	first := "  - " + strings.Repeat("first sibling protected ", 100)
	nested := "    - nested child remains with first sibling"
	second := "  - " + strings.Repeat("second sibling protected ", 100)
	body := strings.Repeat("Preface sentence. ", 90) + "\n" + first + "\n" + nested + "\n" + second
	for name, chunks := range map[string][]Chunk{
		"entry":   ChunkEntry(header, body),
		"project": ProjectChunks(header + "\n" + body),
	} {
		firstFound, secondFound := false, false
		for _, chunk := range chunks {
			hasFirst := strings.Contains(chunk.Text, strings.TrimSpace(first))
			hasSecond := strings.Contains(chunk.Text, strings.TrimSpace(second))
			if hasFirst && hasSecond {
				t.Errorf("%s chunker merged same-indentation sibling list items", name)
			}
			firstFound = firstFound || hasFirst && strings.Contains(chunk.Text, strings.TrimSpace(nested))
			secondFound = secondFound || hasSecond
		}
		if !firstFound || !secondFound {
			t.Errorf("%s chunker split a sibling or its nested continuation", name)
		}
	}
}

func TestChunkersKeepTildeFencesAndMultiParagraphListsAtomic(t *testing.T) {
	header := "[project: Test (test) | note | 2025-01-15 | by test]"
	fence := "~~~go\n" + strings.Repeat("fmt.Println(\"protected\")\n", 90) + "~~~"
	list := "- first paragraph of one list item\n\n    " + strings.Repeat("indented continuation remains protected ", 90)
	body := strings.Repeat("Preface sentence. ", 90) + "\n" + fence + "\n" + list + "\n" + strings.Repeat("Tail sentence. ", 90)
	for name, chunks := range map[string][]Chunk{
		"entry":   ChunkEntry(header, body),
		"project": ProjectChunks(header + "\n" + body),
	} {
		for _, protected := range []string{fence, list} {
			found := false
			for _, chunk := range chunks {
				if strings.Contains(chunk.Text, protected) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s chunker split protected Markdown unit %q", name, protected[:min(len(protected), 80)])
			}
		}
	}
}

func TestChunkEntryRomanianBodyLosesNoSentence(t *testing.T) {
	var body strings.Builder
	var sentences []string
	for i := 0; body.Len() < 3900; i++ {
		sentence := fmt.Sprintf("Propoziția românească numărul %d păstrează toate diacriticele și sensul. ", i)
		if body.Len()+len(sentence) > 4000 {
			break
		}
		body.WriteString(sentence)
		sentences = append(sentences, strings.TrimSpace(sentence))
	}
	chunks := ChunkEntry("[project: Test (test) | note | 2025-01-15 | by test]", body.String())
	joined := ""
	for _, chunk := range chunks {
		joined += chunk.Text
		if EstimateTokens(chunk.Text) > 400 {
			t.Fatalf("chunk exceeds 400 estimated tokens")
		}
	}
	for _, sentence := range sentences {
		if !strings.Contains(joined, sentence) {
			t.Fatalf("lost sentence %q", sentence)
		}
	}
}
