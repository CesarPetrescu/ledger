package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MetaDetails are structured extras found in an entry's text.
type MetaDetails struct {
	Checklist []ChecklistItem `json:"checklist,omitempty"`
	Numbers   []KeyNumber     `json:"numbers,omitempty"`
	Entities  []string        `json:"entities,omitempty"`
	Links     []string        `json:"links,omitempty"`
	Chosen    string          `json:"chosen,omitempty"`   // decisions: what was chosen
	Rejected  string          `json:"rejected,omitempty"` // decisions: the alternatives turned down
}

type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type KeyNumber struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// TagPattern is the shape of a tag: short, lowercase, no spaces.
var TagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+#.-]{0,29}$`)

// ErrLabelsPending means an entry has no AI labels to correct: none yet, or
// it is the console's own Done/Reopened bookkeeping.
var ErrLabelsPending = errors.New("this entry has no AI labels to correct")

// labelLimits are the editable label fields and their maximum lengths.
var labelLimits = map[string]int{"title": 120, "gist": 240, "ask": 300, "next_step": 240, "blocker": 240, "why": 300, "category": 40,
	"priority": 0, "importance": 0, "state": 0, "size": 0, "due": 0, "tags": 0}

var labelChoices = map[string][]string{
	"priority": {"low", "normal", "high"}, "importance": {"routine", "useful", "important"},
	"state": {"", "done", "in_progress", "blocked"}, "size": {"", "S", "M", "L"},
}

// EditableLabel reports whether the owner may override a label field.
func EditableLabel(field string) bool { _, ok := labelLimits[field]; return ok }

// NormalizeLabel validates one owner-supplied label value and returns it in
// stored form: a string, or a string list for tags.
func NormalizeLabel(field string, raw json.RawMessage) (any, error) {
	if field == "tags" {
		var tags []string
		if err := json.Unmarshal(raw, &tags); err != nil {
			return nil, errors.New("tags must be a list of strings")
		}
		out := []string{}
		for _, tag := range tags {
			tag = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), " ", "-"))
			if !TagPattern.MatchString(tag) {
				return nil, fmt.Errorf("tag %q must be lowercase letters, numbers, or -+#. and at most 30 characters", tag)
			}
			if !slices.Contains(out, tag) {
				out = append(out, tag)
			}
		}
		if len(out) > 4 {
			return nil, errors.New("use at most 4 tags")
		}
		return out, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s must be a string", field)
	}
	value = strings.Join(strings.Fields(value), " ")
	if field == "category" {
		value = strings.ToLower(value)
	}
	switch {
	case labelChoices[field] != nil:
		if !slices.Contains(labelChoices[field], value) {
			return nil, fmt.Errorf("%s must be one of %s", field, strings.Join(labelChoices[field], ", "))
		}
	case field == "due":
		if value != "" {
			if _, err := time.Parse(time.DateOnly, value); err != nil {
				return nil, errors.New("due must be a date YYYY-MM-DD")
			}
		}
	case field == "title" && value == "":
		return nil, errors.New("title cannot be empty")
	case utf8.RuneCountInString(value) > labelLimits[field]:
		return nil, fmt.Errorf("%s must be at most %d characters", field, labelLimits[field])
	}
	return value, nil
}

// SetLabels records the owner's corrections: set overrides fields (values
// from NormalizeLabel), reset drops overrides so the model's reading returns
// on the next extraction.
func (db *DB) SetLabels(ctx context.Context, entryID int64, set map[string]any, reset []string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var labelled bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM entry_meta WHERE entry_id=e.id AND title<>'' AND origin='model') FROM entry e WHERE e.id=$1 FOR UPDATE`, entryID).Scan(&labelled); err != nil {
		return err
	}
	if !labelled {
		return ErrLabelsPending
	}
	if set == nil {
		set = map[string]any{}
	}
	if reset == nil {
		reset = []string{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entry_meta_override(entry_id,fields) VALUES($1,$2::jsonb-$3::text[])
ON CONFLICT(entry_id) DO UPDATE SET fields=(entry_meta_override.fields-$3::text[])||$2::jsonb,updated_at=now()`, entryID, set, reset); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM entry_meta_override WHERE entry_id=$1 AND fields='{}'`, entryID); err != nil {
		return err
	}
	if len(reset) > 0 {
		// Re-extract so reset fields get the model's reading back.
		if _, err := tx.Exec(ctx, `UPDATE entry_meta SET version=0,edited=ARRAY(SELECT f FROM unnest(edited) f WHERE f<>ALL($2)) WHERE entry_id=$1`, entryID, reset); err != nil {
			return err
		}
	}
	if err := applyLabels(ctx, tx, entryID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// applyLabels writes an entry's overrides over its current labels.
func applyLabels(ctx context.Context, db execer, entryID int64) error {
	text := func(field string) string {
		return fmt.Sprintf("%[1]s=COALESCE(o.fields->>'%[1]s',m.%[1]s)", field)
	}
	_, err := db.Exec(ctx, `UPDATE entry_meta m SET `+strings.Join([]string{text("title"), text("gist"), text("priority"), text("importance"), text("ask"),
		text("state"), text("next_step"), text("blocker"), text("why"), text("size"), text("category")}, ",")+`,
 due=CASE WHEN o.fields ? 'due' THEN NULLIF(o.fields->>'due','')::date ELSE m.due END,
 tags=CASE WHEN o.fields ? 'tags' THEN ARRAY(SELECT jsonb_array_elements_text(o.fields->'tags')) ELSE m.tags END,
 edited=ARRAY(SELECT jsonb_object_keys(o.fields) ORDER BY 1),
 unsure=ARRAY(SELECT u FROM unnest(m.unsure) u WHERE NOT o.fields ? u),
 updated_at=now()
FROM entry_meta_override o WHERE o.entry_id=m.entry_id AND m.entry_id=$1`, entryID)
	return err
}

// LabelExample is an entry the owner corrected, shown to the extractor.
type LabelExample struct {
	Text      string          `json:"text"`
	Corrected json.RawMessage `json:"owner_corrected"`
}

// LabelExamples returns recent corrections, those in the same project and of
// the same kind first.
// ponytail: recency and project/kind only; rank by embedding similarity if corrections pile up.
func (db *DB) LabelExamples(ctx context.Context, slug, kind string, excludeID int64, limit int) ([]LabelExample, error) {
	rows, err := db.Pool.Query(ctx, `SELECT left(e.body,600),o.fields FROM entry_meta_override o JOIN entry e ON e.id=o.entry_id
WHERE e.id<>$3 ORDER BY (e.slug=$1) DESC,(e.kind=$2) DESC,o.updated_at DESC LIMIT $4`, slug, kind, excludeID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (LabelExample, error) {
		var x LabelExample
		return x, row.Scan(&x.Text, &x.Corrected)
	})
}

// ProjectCategories lists a project's categories in use, most used first.
func (db *DB) ProjectCategories(ctx context.Context, slug string, limit int) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT m.category FROM entry_meta m JOIN entry e ON e.id=m.entry_id
WHERE e.slug=$1 AND m.category<>'' GROUP BY m.category ORDER BY count(*) DESC,m.category LIMIT $2`, slug, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
