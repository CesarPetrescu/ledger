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
func (db *DB) AppendEntryOnce(ctx context.Context, slug, kind, body, source, clientID, requestID string) (Entry, error) {
	if err := ValidateProjectSlug(slug); err != nil {
		return Entry{}, err
	}
	if err := ValidateEntry(kind, body); err != nil {
		return Entry{}, err
	}
	if !requestIDPattern.MatchString(requestID) {
		return Entry{}, errors.New("idempotency_key must be 8 to 80 letters, digits, underscores or hyphens")
	}
	payload, _ := json.Marshal([]string{slug, kind, body, source})
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
	err = tx.QueryRow(ctx, `INSERT INTO entry(slug,kind,body,source,client_id) VALUES($1,$2,$3,$4,$5) RETURNING id,slug,kind,body,source,client_id,created_at`, slug, kind, body, source, clientID).Scan(&entry.ID, &entry.Slug, &entry.Kind, &entry.Body, &entry.Source, &entry.ClientID, &entry.CreatedAt)
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
	err = db.Pool.QueryRow(ctx, `SELECT id::text,slug,kind,body,source,created_at FROM entry WHERE id=$1`, n).Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.CreatedAt)
	return e, err
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
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(change_id),0) FROM entry_change`).Scan(&high); err != nil {
		return result, err
	}
	if through == "" {
		bound = high
	}
	if bound < a || bound > high {
		return result, errors.New("invalid snapshot boundary")
	}
	rows, err := tx.Query(ctx, `SELECT c.change_id::text,e.id::text,e.slug,e.kind,e.body,e.source,e.created_at FROM entry_change c JOIN entry e ON e.id=c.entry_id WHERE c.change_id>$1 AND c.change_id<=$2 ORDER BY c.change_id LIMIT $3`, a, bound, limit+1)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var c Change
		if err = rows.Scan(&c.Cursor, &c.ID, &c.Slug, &c.Kind, &c.Body, &c.Source, &c.CreatedAt); err != nil {
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
