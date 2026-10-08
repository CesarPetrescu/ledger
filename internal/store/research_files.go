package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// FileLinkTTL is how long a download link works.
const FileLinkTTL = time.Hour

var ErrUnknownUpload = errors.New("upload_ids must name files this run uploaded and has not submitted yet")

// checkResearchFiles validates the attachments of one research message and normalizes their media types.
// Files a run uploaded over HTTP (UploadID set) are checked when they are moved into the message.
func checkResearchFiles(files []ResearchFile) error {
	if len(files) > MaxHandoffFiles {
		return ErrHandoffFileLimit
	}
	var total int64
	for i := range files {
		if files[i].UploadID != 0 {
			continue
		}
		if err := validateHandoffFile(files[i].Filename, files[i].Data); err != nil {
			return err
		}
		mediaType, err := normalizeMediaType(files[i].MediaType)
		if err != nil {
			return err
		}
		files[i].MediaType = mediaType
		total += int64(len(files[i].Data))
	}
	if total > MaxHandoffMessageBytes {
		return ErrHandoffFileLimit
	}
	return nil
}

// insertResearchFiles attaches files to a research message. A file with an UploadID moves out of the
// run's uploads, so it must belong to that task and attempt.
func insertResearchFiles(ctx context.Context, tx pgx.Tx, handoffID, messageID int64, attempt int, files []ResearchFile) ([]HandoffFile, error) {
	out := make([]HandoffFile, 0, len(files))
	var total int64
	for _, f := range files {
		var file HandoffFile
		var err error
		if f.UploadID == 0 {
			file, err = insertHandoffFile(ctx, tx, messageID, f.Filename, f.MediaType, f.Data)
		} else {
			err = tx.QueryRow(ctx, `WITH moved AS (DELETE FROM research_upload WHERE id=$2 AND handoff_id=$3 AND attempt=$4 RETURNING filename,media_type,size_bytes,sha256,data)
INSERT INTO handoff_file(message_id,filename,media_type,size_bytes,sha256,data) SELECT $1,filename,media_type,size_bytes,sha256,data FROM moved
RETURNING id,message_id,filename,media_type,size_bytes,encode(sha256,'hex'),created_at`, messageID, f.UploadID, handoffID, attempt).
				Scan(&file.ID, &file.MessageID, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.CreatedAt)
			if IsNotFound(err) {
				err = ErrUnknownUpload
			}
		}
		if err != nil {
			return nil, err
		}
		file.HandoffID = handoffID
		total += file.SizeBytes
		out = append(out, file)
	}
	if total > MaxHandoffMessageBytes {
		return nil, ErrHandoffFileLimit
	}
	return out, nil
}

// StageResearchUpload keeps a file a live run uploaded over HTTP until it names it in submit. A run holds
// at most as many uploads as a message holds files.
func (db *DB) StageResearchUpload(ctx context.Context, id int64, attempt int, f ResearchFile) (HandoffFile, error) {
	f.UploadID = 0
	checked := []ResearchFile{f}
	if err := checkResearchFiles(checked); err != nil {
		return HandoffFile{}, err
	}
	f = checked[0]
	var file HandoffFile
	err := db.withRun(ctx, id, attempt, func(tx pgx.Tx, _ researchLock) error {
		var count int
		var total int64
		if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM research_upload WHERE handoff_id=$1 AND attempt=$2`, id, attempt).Scan(&count, &total); err != nil {
			return err
		}
		if count >= MaxHandoffFiles || total+int64(len(f.Data)) > MaxHandoffMessageBytes {
			return ErrHandoffFileLimit
		}
		sum := sha256.Sum256(f.Data)
		return tx.QueryRow(ctx, `INSERT INTO research_upload(handoff_id,attempt,filename,media_type,size_bytes,sha256,data) VALUES($1,$2,$3,$4,$5,$6,$7)
RETURNING id,filename,media_type,size_bytes,encode(sha256,'hex'),created_at`, id, attempt, f.Filename, f.MediaType, len(f.Data), sum[:], f.Data).
			Scan(&file.ID, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.CreatedAt)
	})
	file.HandoffID = id
	return file, err
}

// CreateFileLinks returns a download secret for each file, valid for FileLinkTTL. Callers hand them only
// to someone already allowed to read the files.
func (db *DB) CreateFileLinks(ctx context.Context, fileIDs []int64) (map[int64]string, time.Time, error) {
	expires := time.Now().Add(FileLinkTTL).UTC().Truncate(time.Second)
	links := make(map[int64]string, len(fileIDs))
	if len(fileIDs) == 0 {
		return links, expires, nil
	}
	ids := make([]int64, 0, len(fileIDs))
	hashes := make([][]byte, 0, len(fileIDs))
	for _, id := range fileIDs {
		if _, ok := links[id]; ok {
			continue
		}
		secret, err := randomToken()
		if err != nil {
			return nil, time.Time{}, err
		}
		hash := sha256.Sum256([]byte(secret))
		links[id] = secret
		ids = append(ids, id)
		hashes = append(hashes, hash[:])
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO file_link(hash,file_id,expires_at) SELECT h,f,$3 FROM unnest($1::bytea[],$2::bigint[]) AS l(h,f)`, hashes, ids, expires)
	return links, expires, err
}

// LinkedFile resolves a download secret that has not expired.
func (db *DB) LinkedFile(ctx context.Context, secret string) (HandoffFile, error) {
	hash := sha256.Sum256([]byte(secret))
	var file HandoffFile
	err := db.Pool.QueryRow(ctx, `SELECT f.id,f.message_id,f.filename,f.media_type,f.size_bytes,encode(f.sha256,'hex'),f.created_at,f.data
FROM file_link l JOIN handoff_file f ON f.id=l.file_id WHERE l.hash=$1 AND l.expires_at>now()`, hash[:]).
		Scan(&file.ID, &file.MessageID, &file.Filename, &file.MediaType, &file.SizeBytes, &file.SHA256, &file.CreatedAt, &file.Data)
	return file, err
}

// SweepResearchFiles removes expired download links and the uploads of runs that are over.
func (db *DB) SweepResearchFiles(ctx context.Context) error {
	if _, err := db.Pool.Exec(ctx, `DELETE FROM file_link WHERE expires_at<=now()`); err != nil {
		return err
	}
	_, err := db.Pool.Exec(ctx, `DELETE FROM research_upload u USING research_task t JOIN handoff_message m ON m.id=t.message_id
WHERE u.handoff_id=t.handoff_id AND (u.attempt<>t.attempt OR m.work_state<>'in_progress')`)
	return err
}
