package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)
var readerPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// AppendEntryOnce is atomic even when concurrent attempts race or a successful
// response is lost. Reusing a key with different content is an error, not a write.
func (db *DB) AppendEntryOnce(ctx context.Context, e NewEntry, requestID string) (Entry, error) {
	if err := e.validate(); err != nil {
		return Entry{}, err
	}
	slug, kind, body, source, clientID := e.Slug, e.Kind, e.Body, e.Source, e.ClientID
	if !requestIDPattern.MatchString(requestID) {
		return Entry{}, errors.New("idempotency_key must be 8 to 80 letters, digits, underscores or hyphens")
	}
	// A plain entry keeps the original receipt format, so retries from before
	// replies existed still match their receipts.
	fields := []any{slug, kind, body, source}
	if e.ReplyTo != 0 || e.Context != "" {
		fields = append(fields, e.ReplyTo, e.Context)
	}
	payload, _ := json.Marshal(fields)
	hash := sha256.Sum256(payload)
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,7103377))`, clientID+":"+requestID); err != nil {
		return Entry{}, err
	}
	var entry Entry
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT r.payload_hash,e.id,e.slug,e.kind,e.body,e.source,e.client_id,e.created_at FROM entry_write_receipt r JOIN entry e ON e.id=r.entry_id WHERE r.client_id=$1 AND r.request_id=$2`, clientID, requestID).Scan(&previous, &entry.ID, &entry.Slug, &entry.Kind, &entry.Body, &entry.Source, &entry.ClientID, &entry.CreatedAt)
	if err == nil {
		if !bytes.Equal(previous, hash[:]) {
			return Entry{}, errors.New("idempotency_key already used with different content")
		}
		return entry, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, err
	}
	// A retry of a write whose entry the owner moved to Trash must not
	// recreate it; the receipt lives on in the trash payload.
	var trashed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trash t,
 jsonb_array_elements(CASE WHEN t.kind='entry' THEN jsonb_build_array(t.payload) ELSE t.payload->'entries' END) e,
 jsonb_array_elements(COALESCE(NULLIF(e->'receipts','null'),'[]')) r
 WHERE r->>'client_id'=$1 AND r->>'request_id'=$2)`, clientID, requestID).Scan(&trashed); err != nil {
		return Entry{}, err
	}
	if trashed {
		return Entry{}, ErrEntryTrashed
	}
	entry, _, err = insertEntry(ctx, tx, e)
	if err != nil {
		return Entry{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO entry_write_receipt(client_id,request_id,payload_hash,entry_id) VALUES($1,$2,$3,$4)`, clientID, requestID, hash[:], entry.ID); err != nil {
		return Entry{}, err
	}
	return entry, tx.Commit(ctx)
}

// String IDs preserve all 64 bits in browser clients.
type EntryView struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	Context   string    `json:"context,omitempty"`
	// Replies answer this entry, oldest first; only get_entry fills them.
	Replies []EntryReply `json:"replies,omitempty"`
	// RepliesTotal counts every reply; only the newest maxReplies are listed.
	RepliesTotal int `json:"replies_total,omitempty"`
}

// maxReplies is how many of a thread's newest replies get_entry lists.
const maxReplies = 100

// EntryReply is one answer in an entry's thread.
type EntryReply struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
type Change struct {
	EntryView
	Cursor string `json:"cursor"`
}
type ChangePage struct {
	Entries    []Change `json:"entries"`
	Checkpoint string   `json:"checkpoint"`
	NextCursor string   `json:"next_cursor"`
	Through    string   `json:"through"`
	HasMore    bool     `json:"has_more"`
}
type ReadReceipt struct {
	Checkpoint string `json:"checkpoint"`
}

func ParseCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != value {
		return 0, errors.New("cursor must be a nonnegative decimal integer")
	}
	return n, nil
}
func (db *DB) GetEntry(ctx context.Context, id string) (EntryView, error) {
	n, err := ParseCursor(id)
	if err != nil || n == 0 {
		return EntryView{}, errors.New("entry ID must be a positive decimal integer")
	}
	var e EntryView
	err = db.Pool.QueryRow(ctx, `SELECT id::text,slug,kind,body,source,created_at,COALESCE(reply_to::text,''),context FROM entry WHERE id=$1`, n).Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.CreatedAt, &e.ReplyTo, &e.Context)
	if err != nil {
		return e, err
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM entry WHERE reply_to=$1`, n).Scan(&e.RepliesTotal); err != nil {
		return e, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT * FROM (SELECT id::text,source,body,created_at,id n FROM entry WHERE reply_to=$1 ORDER BY created_at DESC,id DESC LIMIT $2) r ORDER BY created_at,n`, n, maxReplies)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var r EntryReply
		var order int64
		if err := rows.Scan(&r.ID, &r.Source, &r.Body, &r.CreatedAt, &order); err != nil {
			return e, err
		}
		e.Replies = append(e.Replies, r)
	}
	return e, rows.Err()
}

// Changes freezes a high-water mark for pagination. Delivered is advanced only
// over a contiguous fetched range, never over an arbitrary caller-supplied gap.
func (db *DB) Changes(ctx context.Context, clientID, reader, after, through string, limit int) (ChangePage, error) {
	result := ChangePage{Entries: []Change{}}
	if !readerPattern.MatchString(reader) || limit < 1 || limit > 100 {
		return result, errors.New("invalid reader or limit (1..100)")
	}
	a, err := ParseCursor(after)
	if err != nil {
		return result, err
	}
	bound, err := ParseCursor(through)
	if err != nil {
		return result, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO glass_reader(client_id,reader) VALUES($1,$2) ON CONFLICT DO NOTHING`, clientID, reader); err != nil {
		return result, err
	}
	var checkpoint, delivered, high int64
	if err = tx.QueryRow(ctx, `SELECT checkpoint,delivered FROM glass_reader WHERE client_id=$1 AND reader=$2 FOR UPDATE`, clientID, reader).Scan(&checkpoint, &delivered); err != nil {
		return result, err
	}
	if after == "" {
		a = checkpoint
	}
	if a < checkpoint || a > delivered {
		return result, errors.New("cursor would skip unfetched changes or precedes acknowledgement")
	}
	// The high-water mark is the last change ID allocated, read under the
	// writers' lock so every lower ID is committed or rolled back. Unlike
	// max(change_id), deleting the newest entries never lowers it, so issued
	// cursors and snapshot bounds stay valid.
	// ponytail: waits for in-flight writers; fine at this write rate.
	if err = tx.QueryRow(ctx, `SELECT COALESCE(pg_sequence_last_value(pg_get_serial_sequence('entry_change','change_id')),0) FROM pg_advisory_xact_lock(7103376)`).Scan(&high); err != nil {
		return result, err
	}
	if through == "" {
		bound = high
	}
	if bound < a || bound > high {
		return result, errors.New("invalid snapshot boundary")
	}
	rows, err := tx.Query(ctx, `SELECT c.change_id::text,e.id::text,e.slug,e.kind,e.body,e.source,e.created_at,COALESCE(e.reply_to::text,''),e.context FROM entry_change c JOIN entry e ON e.id=c.entry_id WHERE c.change_id>$1 AND c.change_id<=$2 ORDER BY c.change_id LIMIT $3`, a, bound, limit+1)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var c Change
		if err = rows.Scan(&c.Cursor, &c.ID, &c.Slug, &c.Kind, &c.Body, &c.Source, &c.CreatedAt, &c.ReplyTo, &c.Context); err != nil {
			rows.Close()
			return result, err
		}
		result.Entries = append(result.Entries, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	result.HasMore = len(result.Entries) > limit
	if result.HasMore {
		result.Entries = result.Entries[:limit]
	}
	next := bound
	if result.HasMore {
		next, _ = ParseCursor(result.Entries[len(result.Entries)-1].Cursor)
	}
	if _, err = tx.Exec(ctx, `UPDATE glass_reader SET delivered=GREATEST(delivered,$3) WHERE client_id=$1 AND reader=$2`, clientID, reader, next); err != nil {
		return result, err
	}
	result.Checkpoint = strconv.FormatInt(checkpoint, 10)
	result.NextCursor = strconv.FormatInt(next, 10)
	result.Through = strconv.FormatInt(bound, 10)
	return result, tx.Commit(ctx)
}
func (db *DB) AcknowledgeChanges(ctx context.Context, clientID, reader, through string) (ReadReceipt, error) {
	n, err := ParseCursor(through)
	if err != nil {
		return ReadReceipt{}, err
	}
	if !readerPattern.MatchString(reader) {
		return ReadReceipt{}, errors.New("invalid reader")
	}
	var checkpoint int64
	err = db.Pool.QueryRow(ctx, `UPDATE glass_reader SET checkpoint=GREATEST(checkpoint,$3) WHERE client_id=$1 AND reader=$2 AND delivered >= $3 RETURNING checkpoint`, clientID, reader, n).Scan(&checkpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReadReceipt{}, fmt.Errorf("cannot acknowledge unfetched changes")
	}
	return ReadReceipt{Checkpoint: strconv.FormatInt(checkpoint, 10)}, err
}
