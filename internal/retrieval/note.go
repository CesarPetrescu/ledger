package retrieval

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// structuredNote holds the fields of a labelled note such as the AI-news
// entries ("Title: … / What changed: … / Why it matters: … / URL: …").
type structuredNote struct {
	Title, Changed, Why, Source, Link string
	Categories                        []string
}

var noteLabel = regexp.MustCompile(`(?im)^[ \t]*(title|what changed|why it matters|technical details|source|date|url|caveats|category)[ \t]*:[ \t]*`)

// parseStructuredNote reads a labelled note deterministically. It reports
// false unless the text has a Title and at least two other known labels, so
// ordinary prose is left to the model.
func parseStructuredNote(body string) (structuredNote, bool) {
	matches := noteLabel.FindAllStringSubmatchIndex(body, -1)
	fields := map[string]string{}
	for i, m := range matches {
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		label := strings.ToLower(body[m[2]:m[3]])
		if _, seen := fields[label]; !seen {
			fields[label] = strings.Join(strings.Fields(body[m[1]:end]), " ")
		}
	}
	if fields["title"] == "" || len(fields) < 3 {
		return structuredNote{}, false
	}
	note := structuredNote{
		Title:   fields["title"],
		Changed: firstSentences(fields["what changed"], 240),
		Why:     firstSentences(fields["why it matters"], 300),
		Source:  clip(fields["source"], 120),
		Link:    firstURL(fields["url"]),
	}
	for _, part := range strings.FieldsFunc(fields["category"], func(r rune) bool { return r == '|' || r == ',' || r == '/' || r == ';' }) {
		if tag := strings.ToLower(strings.Join(strings.Fields(part), "-")); tag != "" {
			note.Categories = append(note.Categories, tag)
		}
	}
	return note, true
}

// firstSentences keeps whole leading sentences within limit runes, falling
// back to a clipped first sentence when even that is too long.
func firstSentences(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	out := ""
	for _, end := range sentenceEnds.FindAllStringIndex(text, -1) {
		candidate := strings.TrimSpace(text[:end[1]])
		if utf8.RuneCountInString(candidate) > limit {
			break
		}
		out = candidate
	}
	if out == "" {
		return clip(text, limit)
	}
	return out
}

// A sentence ends at . ! or ? followed by whitespace, so version numbers such
// as v0.34.4 stay whole. ponytail: "e.g. x" may end a sentence early; the
// result is still a correct, shorter prefix.
var sentenceEnds = regexp.MustCompile(`[.!?]\s`)

var urlPattern = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// firstURL returns the first well-formed http(s) URL, or "".
func firstURL(text string) string {
	raw := strings.TrimRight(urlPattern.FindString(text), ".,;:")
	if raw == "" || utf8.RuneCountInString(raw) > 500 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}
