package retrieval

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

type Indexer struct {
	db    *store.DB
	infer *InferClient
}

func NewIndexer(db *store.DB, infer *InferClient) *Indexer { return &Indexer{db: db, infer: infer} }

func (w *Indexer) ProcessBatch(ctx context.Context, limit int) (int, error) {
	processed := 0
	cutoff := time.Now()
	var batchErr error
	for processed < limit {
		done, err := w.processOne(ctx, cutoff)
		if err != nil {
			if !done {
				return processed, err
			}
			if batchErr == nil {
				batchErr = err
			}
		}
		if !done {
			break
		}
		processed++
	}
	return processed, batchErr
}

func (w *Indexer) processOne(ctx context.Context, cutoff time.Time) (bool, error) {
	tx, err := w.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var ref string
	if err := tx.QueryRow(ctx, `SELECT ref FROM chunk_dirty WHERE queued_at <= $1 ORDER BY queued_at,ref FOR UPDATE SKIP LOCKED LIMIT 1`, cutoff).Scan(&ref); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	chunks, err := buildRef(ctx, tx, ref)
	if err != nil {
		return false, err
	}
	type existing struct {
		hash     string
		model    string
		embedded bool
	}
	old := map[int]existing{}
	rows, err := tx.Query(ctx, `SELECT ord,encode(text_hash,'hex'),model,embedding IS NOT NULL FROM chunk WHERE ref=$1`, ref)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var ord int
		var item existing
		if err := rows.Scan(&ord, &item.hash, &item.model, &item.embedded); err != nil {
			rows.Close()
			return false, err
		}
		old[ord] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	missing := make([]int, 0, len(chunks))
	for i, chunk := range chunks {
		item, exists := old[i]
		changed := !exists || item.hash != chunk.Hash || item.model != w.infer.embeddingModel
		if changed {
			hash, _ := hex.DecodeString(chunk.Hash)
			if _, err := tx.Exec(ctx, `INSERT INTO chunk(ref,ord,text,text_hash,model,embedding) VALUES($1,$2,$3,$4,$5,NULL)
ON CONFLICT(ref,ord) DO UPDATE SET text=EXCLUDED.text,text_hash=EXCLUDED.text_hash,model=EXCLUDED.model,embedding=NULL`,
				ref, i, chunk.Text, hash, w.infer.embeddingModel); err != nil {
				return false, err
			}
		}
		if changed || !item.embedded {
			missing = append(missing, i)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chunk WHERE ref=$1 AND ord >= $2`, ref, len(chunks)); err != nil {
		return false, err
	}
	for start := 0; start < len(missing); start += 32 {
		end := min(start+32, len(missing))
		texts := make([]string, end-start)
		for i, ord := range missing[start:end] {
			texts[i] = chunks[ord].Text
		}
		vectors, err := w.infer.Embed(ctx, texts)
		if err != nil {
			if _, queueErr := tx.Exec(ctx, `UPDATE chunk_dirty SET queued_at=now() WHERE ref=$1`, ref); queueErr != nil {
				return false, queueErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return false, commitErr
			}
			return true, err
		}
		for i, ord := range missing[start:end] {
			if _, err := tx.Exec(ctx, `UPDATE chunk SET embedding=$3 WHERE ref=$1 AND ord=$2`,
				ref, ord, pgvector.NewHalfVector(vectors[i])); err != nil {
				return false, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM chunk_dirty WHERE ref=$1`, ref); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func buildRef(ctx context.Context, tx pgx.Tx, ref string) ([]Chunk, error) {
	if slug, ok := strings.CutPrefix(ref, "project:"); ok {
		var p store.Project
		err := tx.QueryRow(ctx, `SELECT slug,name,tier,hours_wk,type,description,goal,deadline,needs_me,automate,stack,updated_at FROM project WHERE slug=$1`, slug).
			Scan(&p.Slug, &p.Name, &p.Tier, &p.HoursWK, &p.Type, &p.Description, &p.Goal, &p.Deadline, &p.NeedsMe, &p.Automate, &p.Stack, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		text := fmt.Sprintf("[project: %s (%s) | tier: %s | deadline: %s]\nHours/week: %d\nType: %s\nDescription: %s\nGoal: %s\nNeeds me: %s\nAutomate: %s\nStack: %s", p.Name, p.Slug, p.Tier, p.Deadline, p.HoursWK, p.Type, p.Description, p.Goal, p.NeedsMe, p.Automate, p.Stack)
		return ProjectChunks(text), nil
	}
	if id, ok := strings.CutPrefix(ref, "entry:"); ok {
		entryID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid ref %q", ref)
		}
		var e store.Entry
		var projectName string
		err = tx.QueryRow(ctx, `SELECT e.id,e.slug,e.kind,e.body,e.source,e.client_id,e.created_at,p.name FROM entry e JOIN project p ON p.slug=e.slug WHERE e.id=$1`, entryID).
			Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt, &projectName)
		if err != nil {
			return nil, err
		}
		header := fmt.Sprintf("[project: %s (%s) | %s | %s | by %s]", projectName, e.Slug, e.Kind, e.CreatedAt.UTC().Format(time.DateOnly), e.Source)
		return ChunkEntry(header, e.Body), nil
	}
	return nil, fmt.Errorf("invalid ref %q", ref)
}

func (w *Indexer) QueueAll(ctx context.Context) (int64, error) {
	result, err := w.db.Pool.Exec(ctx, `INSERT INTO chunk_dirty(ref)
SELECT 'project:'||slug FROM project UNION SELECT 'entry:'||id::text FROM entry
ON CONFLICT(ref) DO UPDATE SET queued_at=now()`)
	return result.RowsAffected(), err
}

func (w *Indexer) Run(ctx context.Context) error {
	backoff := 250 * time.Millisecond
	for {
		err := w.runListener(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("index listener disconnected: %v; reconnecting", err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func (w *Indexer) runListener(ctx context.Context) error {
	conn, err := w.db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN chunk_dirty`); err != nil {
		return err
	}
	for {
		if _, err := w.ProcessBatch(ctx, 50); err != nil && ctx.Err() == nil {
			log.Printf("index batch: %v", err)
		}
		wait, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := conn.Conn().WaitForNotification(wait)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
}
