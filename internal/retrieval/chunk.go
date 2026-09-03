package retrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Chunk struct {
	Ord  int
	Text string
	Hash string
}

var sentenceBreak = regexp.MustCompile(`(?s).*?[.!?…]+(?:\s+|$)`)

func EstimateTokens(s string) int { return int(math.Ceil(float64(utf8.RuneCountInString(s)) / 3.5)) }

func ChunkEntry(header, body string) []Chunk {
	full := header + "\n" + strings.TrimSpace(body)
	if EstimateTokens(full) <= 450 {
		return []Chunk{newChunk(0, full)}
	}
	units := entryUnits(body)
	if len(units) == 0 {
		units = []string{body}
	}
	var normalized []string
	for _, unit := range units {
		if protectedUnit(unit) || EstimateTokens(unit) <= 64 {
			normalized = append(normalized, unit)
			continue
		}
		normalized = append(normalized, splitUnit(unit, 224)...)
	}
	units = normalized
	var chunks []Chunk
	for pos := 0; pos < len(units); {
		if EstimateTokens(header+"\n"+units[pos]) > 400 {
			if protectedUnit(units[pos]) {
				chunks = append(chunks, newChunk(len(chunks), header+"\n"+strings.TrimSpace(units[pos])))
				pos++
				continue
			}
			parts := splitUnit(units[pos], max(1, 1380-utf8.RuneCountInString(header)))
			units = append(append(units[:pos:pos], parts...), units[pos+1:]...)
			continue
		}
		start := pos
		if len(chunks) > 0 {
			for candidate := pos - 1; candidate >= 0; candidate-- {
				if EstimateTokens(header+"\n"+strings.Join(units[candidate:pos+1], "")) > 400 {
					break
				}
				start = candidate
				if EstimateTokens(strings.Join(units[candidate:pos], "")) >= 60 {
					break
				}
			}
		}
		end := pos + 1
		for end < len(units) && EstimateTokens(header+"\n"+strings.Join(units[start:end+1], "")) <= 400 {
			end++
		}
		chunks = append(chunks, newChunk(len(chunks), header+"\n"+strings.TrimSpace(strings.Join(units[start:end], ""))))
		pos = end
	}
	return chunks
}

func protectedUnit(s string) bool {
	first, _, _ := strings.Cut(strings.TrimLeft(s, "\r\n"), "\n")
	_, _, fenced := fenceStart(first)
	return fenced || isListItem(strings.TrimSpace(first))
}

func entryUnits(body string) []string {
	var units []string
	lines := strings.SplitAfter(body, "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if marker, width, ok := fenceStart(line); ok {
			j := fencedUnitEnd(lines, i, marker, width)
			units = append(units, strings.Join(lines[i:j], ""))
			i = j
			continue
		}
		if isListItem(trim) {
			j := listUnitEnd(lines, i)
			units = append(units, strings.Join(lines[i:j], ""))
			i = j
			continue
		}
		if trim == "" {
			units = append(units, line)
			i++
			continue
		}
		matches := sentenceBreak.FindAllString(line, -1)
		used := 0
		for _, match := range matches {
			units = append(units, match)
			used += len(match)
		}
		if used < len(line) {
			units = append(units, line[used:])
		}
		i++
	}
	return units
}

func isListItem(s string) bool {
	_, ok := listMarkerIndent(s)
	return ok
}

func listMarkerIndent(line string) (int, bool) {
	line = strings.TrimRight(line, "\r\n")
	index, indent := 0, 0
	for index < len(line) {
		switch line[index] {
		case ' ':
			indent++
			index++
		case '	':
			indent += 4 - indent%4
			index++
		default:
			goto marker
		}
	}
marker:
	line = line[index:]
	if len(line) >= 2 && (line[0] == '-' || line[0] == '*' || line[0] == '+') && (line[1] == ' ' || line[1] == '	') {
		return indent, true
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return indent, i > 0 && i+1 < len(line) && (line[i] == '.' || line[i] == ')') && (line[i+1] == ' ' || line[i+1] == '	')
}

func fenceStart(line string) (byte, int, bool) {
	trimmed := strings.TrimLeft(strings.TrimRight(line, "\r\n"), " 	")
	if len(trimmed) < 3 || trimmed[0] != '`' && trimmed[0] != '~' {
		return 0, 0, false
	}
	marker := trimmed[0]
	width := 0
	for width < len(trimmed) && trimmed[width] == marker {
		width++
	}
	return marker, width, width >= 3
}

func fencedUnitEnd(lines []string, start int, marker byte, width int) int {
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		count := 0
		for count < len(trimmed) && trimmed[count] == marker {
			count++
		}
		if count >= width && strings.TrimSpace(trimmed[count:]) == "" {
			return i + 1
		}
	}
	return len(lines)
}

func listUnitEnd(lines []string, start int) int {
	startIndent, _ := listMarkerIndent(lines[start])
	for i := start + 1; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if indent, sibling := listMarkerIndent(lines[i]); sibling && indent <= startIndent {
			return i
		}
		if trimmed != "" {
			i++
			continue
		}
		next := i
		for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
			next++
		}
		if next < len(lines) {
			indent, _ := leadingIndent(lines[next])
			if indent > startIndent {
				i = next
				continue
			}
		}
		return i
	}
	return len(lines)
}

func leadingIndent(line string) (int, int) {
	index, indent := 0, 0
	for index < len(line) {
		switch line[index] {
		case ' ':
			indent++
			index++
		case '	':
			indent += 4 - indent%4
			index++
		default:
			return indent, index
		}
	}
	return indent, index
}

func isProtectedLineStart(line string) bool {
	_, _, fenced := fenceStart(line)
	return fenced || isListItem(strings.TrimSpace(line))
}

func splitUnit(s string, maxRunes int) []string {
	runes := []rune(s)
	var parts []string
	for len(runes) > maxRunes {
		cut := maxRunes
		for cut > maxRunes/2 && runes[cut] != ' ' && runes[cut] != '\n' {
			cut--
		}
		parts = append(parts, string(runes[:cut]))
		runes = runes[cut:]
	}
	return append(parts, string(runes))
}

func ProjectChunks(text string) []Chunk {
	if EstimateTokens(text) <= 700 {
		return []Chunk{newChunk(0, text)}
	}
	header, body, ok := strings.Cut(text, "\n")
	if !ok {
		return []Chunk{newChunk(0, text)}
	}
	return chunkParagraphs(header, body, 700)
}

func chunkParagraphs(header, body string, limit int) []Chunk {
	parts := projectUnits(body)
	var out []Chunk
	for len(parts) > 0 {
		if EstimateTokens(header+"\n"+parts[0]) > limit && !protectedUnit(parts[0]) {
			maxRunes := max(1, int(float64(limit)*3.5)-utf8.RuneCountInString(header)-1)
			split := splitUnit(parts[0], maxRunes)
			parts = append(append(parts[:0:0], split...), parts[1:]...)
			continue
		}
		n := 1
		for n < len(parts) && EstimateTokens(header+"\n"+strings.Join(parts[:n+1], "")) <= limit {
			n++
		}
		piece := strings.TrimSpace(strings.Join(parts[:n], ""))
		out = append(out, newChunk(len(out), header+"\n"+piece))
		parts = parts[n:]
	}
	return out
}

func projectUnits(body string) []string {
	lines := strings.SplitAfter(body, "\n")
	var units []string
	for i := 0; i < len(lines); {
		trim := strings.TrimSpace(lines[i])
		if marker, width, ok := fenceStart(lines[i]); ok {
			j := fencedUnitEnd(lines, i, marker, width)
			units = append(units, strings.Join(lines[i:j], ""))
			i = j
			continue
		}
		if isListItem(trim) {
			j := listUnitEnd(lines, i)
			units = append(units, strings.Join(lines[i:j], ""))
			i = j
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "" && !isProtectedLineStart(lines[j]) {
			j++
		}
		if j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		units = append(units, strings.Join(lines[i:j], ""))
		i = j
	}
	return units
}

func newChunk(ord int, text string) Chunk {
	sum := sha256.Sum256([]byte(text))
	return Chunk{Ord: ord, Text: text, Hash: hex.EncodeToString(sum[:])}
}
