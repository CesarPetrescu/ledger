package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// OwnerState is the owner's own triage of an entry: read, starred, handled
// (for entries that ask something), and snoozed until a date (YYYY-MM-DD).
type OwnerState struct {
	Read         bool   `json:"read"`
	Starred      bool   `json:"starred"`
	Handled      bool   `json:"handled"`
	SnoozedUntil string `json:"snoozed_until,omitempty"`
	// Snoozed says the snooze has not ended yet by the database's calendar; SnoozedUntil keeps a past date after it wakes.
	Snoozed bool `json:"snoozed,omitempty"`
}

// OwnerPatch changes only the fields that are set. SnoozeDays 0 clears a
// snooze; 1 to 30 hides the entry from focus lists until that many days pass.
type OwnerPatch struct {
	Read       *bool
	Starred    *bool
	Handled    *bool
	SnoozeDays *int
}

var ErrInvalidSnooze = errors.New("snooze_days must be between 0 and 30")

// SetOwnerState applies a patch to the owner's state for an entry and records
// an undoable action holding the state before and after.
func (db *DB) SetOwnerState(ctx context.Context, entryID int64, p OwnerPatch) (OwnerState, int64, error) {
	if p.SnoozeDays != nil && (*p.SnoozeDays < 0 || *p.SnoozeDays > 30) {
		return OwnerState{}, 0, ErrInvalidSnooze
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return OwnerState{}, 0, err
	}
	defer tx.Rollback(ctx)
	var slug, title string
	if err := tx.QueryRow(ctx, `SELECT e.slug,COALESCE(NULLIF(m.title,''),left(e.body,120)) FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id WHERE e.id=$1 FOR UPDATE OF e`, entryID).Scan(&slug, &title); err != nil {
		return OwnerState{}, 0, err
	}
	s, before, after, err := applyOwnerPatch(ctx, tx, entryID, p)
	if err != nil {
		return OwnerState{}, 0, err
	}
	actionID, err := recordAction(ctx, tx, "owner", &entryID, slug, ownerLabel(p)+": "+title,
		map[string]any{"entry_id": entryID, "before": before, "after": after})
	if err != nil {
		return OwnerState{}, 0, err
	}
	return s, actionID, tx.Commit(ctx)
}

// applyOwnerPatch writes a triage patch for a locked entry and returns its
// state before and after, for undo.
func applyOwnerPatch(ctx context.Context, tx pgx.Tx, entryID int64, p OwnerPatch) (OwnerState, *ownerRow, *ownerRow, error) {
	snapshot := func() (*ownerRow, error) {
		var r ownerRow
		err := tx.QueryRow(ctx, `SELECT read_at,starred,handled_at,to_char(snoozed_until,'YYYY-MM-DD'),updated_at FROM entry_owner_state WHERE entry_id=$1`, entryID).
			Scan(&r.ReadAt, &r.Starred, &r.HandledAt, &r.SnoozedUntil, &r.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return &r, err
	}
	before, err := snapshot()
	if err != nil {
		return OwnerState{}, nil, nil, err
	}
	var s OwnerState
	if err := tx.QueryRow(ctx, `INSERT INTO entry_owner_state AS o(entry_id,read_at,starred,handled_at,snoozed_until)
VALUES($1,CASE WHEN $2 THEN now() END,COALESCE($3,false),CASE WHEN $4 THEN now() END,CASE WHEN $5::int>0 THEN current_date+$5::int END)
ON CONFLICT(entry_id) DO UPDATE SET
 read_at=CASE WHEN $2::bool IS NULL THEN o.read_at WHEN $2 THEN COALESCE(o.read_at,now()) END,
 starred=COALESCE($3,o.starred),
 handled_at=CASE WHEN $4::bool IS NULL THEN o.handled_at WHEN $4 THEN COALESCE(o.handled_at,now()) END,
 snoozed_until=CASE WHEN $5::int IS NULL THEN o.snoozed_until WHEN $5>0 THEN current_date+$5::int END,
 updated_at=now()
RETURNING o.read_at IS NOT NULL,o.starred,o.handled_at IS NOT NULL,COALESCE(to_char(o.snoozed_until,'YYYY-MM-DD'),''),COALESCE(o.snoozed_until>current_date,false)`,
		entryID, p.Read, p.Starred, p.Handled, p.SnoozeDays).Scan(&s.Read, &s.Starred, &s.Handled, &s.SnoozedUntil, &s.Snoozed); err != nil {
		return OwnerState{}, nil, nil, err
	}
	after, err := snapshot()
	return s, before, after, err
}

// MarkAllRead marks every unread reading entry that matches f as read, as
// one action that undoes together. It reports how many it marked; with none
// it writes nothing and records no action.
// ponytail: one statement per entry in one transaction; batch it if backlogs reach thousands.
func (db *DB) MarkAllRead(ctx context.Context, f EntryFilter) (int, int64, error) {
	f.Reading, f.Limit, f.Before = "unread", 0, nil
	unread, err := db.ListEntries(ctx, f)
	if err != nil || len(unread) == 0 {
		return 0, 0, err
	}
	ids := make([]int64, len(unread))
	for i, e := range unread {
		ids[i] = e.ID
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	// Lock the entries in one order, as other writers do; skip any read meanwhile.
	locked, err := tx.Query(ctx, `SELECT e.id FROM entry e LEFT JOIN entry_owner_state o ON o.entry_id=e.id
WHERE e.id=ANY($1) AND o.read_at IS NULL ORDER BY e.id FOR UPDATE OF e`, ids)
	if err != nil {
		return 0, 0, err
	}
	present, err := pgx.CollectRows(locked, pgx.RowTo[int64])
	if err != nil {
		return 0, 0, err
	}
	read := true
	items := make([]map[string]any, 0, len(present))
	for _, id := range present {
		_, before, after, err := applyOwnerPatch(ctx, tx, id, OwnerPatch{Read: &read})
		if err != nil {
			return 0, 0, err
		}
		items = append(items, map[string]any{"entry_id": id, "before": before, "after": after})
	}
	if len(items) == 0 {
		return 0, 0, nil
	}
	label := "Marked 1 read"
	if len(items) != 1 {
		label = fmt.Sprintf("Marked %d read", len(items))
	}
	actionID, err := recordAction(ctx, tx, "read_all", nil, "", label, map[string]any{"entries": items})
	if err != nil {
		return 0, 0, err
	}
	return len(items), actionID, tx.Commit(ctx)
}

// ownerLabel names a triage patch for the recent-actions list.
func ownerLabel(p OwnerPatch) string {
	var parts []string
	if p.Read != nil {
		parts = append(parts, map[bool]string{true: "Marked read", false: "Marked unread"}[*p.Read])
	}
	if p.Starred != nil {
		parts = append(parts, map[bool]string{true: "Starred", false: "Unstarred"}[*p.Starred])
	}
	if p.Handled != nil {
		parts = append(parts, map[bool]string{true: "Marked handled", false: "Marked not handled"}[*p.Handled])
	}
	if p.SnoozeDays != nil {
		if *p.SnoozeDays == 0 {
			parts = append(parts, "Unsnoozed")
		} else {
			parts = append(parts, fmt.Sprintf("Snoozed %d %s", *p.SnoozeDays, map[bool]string{true: "day", false: "days"}[*p.SnoozeDays == 1]))
		}
	}
	return strings.Join(parts, ", ")
}
