package store

import (
	"context"
	"errors"
)

// OwnerState is the owner's own triage of an entry: read, starred, handled
// (for entries that ask something), and snoozed until a date (YYYY-MM-DD).
type OwnerState struct {
	Read         bool   `json:"read"`
	Starred      bool   `json:"starred"`
	Handled      bool   `json:"handled"`
	SnoozedUntil string `json:"snoozed_until,omitempty"`
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

// SetOwnerState applies a patch to the owner's state for an entry.
func (db *DB) SetOwnerState(ctx context.Context, entryID int64, p OwnerPatch) (OwnerState, error) {
	if p.SnoozeDays != nil && (*p.SnoozeDays < 0 || *p.SnoozeDays > 30) {
		return OwnerState{}, ErrInvalidSnooze
	}
	var s OwnerState
	err := db.Pool.QueryRow(ctx, `INSERT INTO entry_owner_state AS o(entry_id,read_at,starred,handled_at,snoozed_until)
SELECT e.id,CASE WHEN $2 THEN now() END,COALESCE($3,false),CASE WHEN $4 THEN now() END,
 CASE WHEN $5::int>0 THEN current_date+$5::int END
FROM entry e WHERE e.id=$1
ON CONFLICT(entry_id) DO UPDATE SET
 read_at=CASE WHEN $2::bool IS NULL THEN o.read_at WHEN $2 THEN COALESCE(o.read_at,now()) END,
 starred=COALESCE($3,o.starred),
 handled_at=CASE WHEN $4::bool IS NULL THEN o.handled_at WHEN $4 THEN COALESCE(o.handled_at,now()) END,
 snoozed_until=CASE WHEN $5::int IS NULL THEN o.snoozed_until WHEN $5>0 THEN current_date+$5::int END,
 updated_at=now()
RETURNING o.read_at IS NOT NULL,o.starred,o.handled_at IS NOT NULL,COALESCE(to_char(o.snoozed_until,'YYYY-MM-DD'),'')`,
		entryID, p.Read, p.Starred, p.Handled, p.SnoozeDays).Scan(&s.Read, &s.Starred, &s.Handled, &s.SnoozedUntil)
	return s, err
}
