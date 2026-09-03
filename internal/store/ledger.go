package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Project struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	Tier        string     `json:"tier"`
	HoursWK     int        `json:"hours_wk"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Goal        string     `json:"goal"`
	Deadline    string     `json:"deadline"`
	NeedsMe     string     `json:"needs_me"`
	Automate    string     `json:"automate"`
	Stack       string     `json:"stack"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastEntryAt *time.Time `json:"last_entry_at,omitempty"`
}

type Entry struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Source    string    `json:"source"`
	ClientID  string    `json:"client_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ProjectWithEntries struct {
	Project Project `json:"project"`
	Entries []Entry `json:"entries"`
}

const projectColumns = `p.slug,p.name,p.tier,p.hours_wk,p.type,p.description,p.goal,p.deadline,p.needs_me,p.automate,p.stack,p.updated_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.Slug, &p.Name, &p.Tier, &p.HoursWK, &p.Type, &p.Description, &p.Goal, &p.Deadline, &p.NeedsMe, &p.Automate, &p.Stack, &p.UpdatedAt)
	return p, err
}

func (db *DB) UpsertProject(ctx context.Context, p Project) (Project, error) {
	return scanProject(db.Pool.QueryRow(ctx, `
INSERT INTO project(slug,name,tier,hours_wk,type,description,goal,deadline,needs_me,automate,stack)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(slug) DO UPDATE SET name=EXCLUDED.name,tier=EXCLUDED.tier,hours_wk=EXCLUDED.hours_wk,
 type=EXCLUDED.type,description=EXCLUDED.description,goal=EXCLUDED.goal,deadline=EXCLUDED.deadline,
 needs_me=EXCLUDED.needs_me,automate=EXCLUDED.automate,stack=EXCLUDED.stack,updated_at=now()
RETURNING slug,name,tier,hours_wk,type,description,goal,deadline,needs_me,automate,stack,updated_at`,
		p.Slug, p.Name, p.Tier, p.HoursWK, p.Type, p.Description, p.Goal, p.Deadline, p.NeedsMe, p.Automate, p.Stack))
}

func (db *DB) ListProjects(ctx context.Context, tier string) ([]Project, error) {
	rows, err := db.Pool.Query(ctx, `SELECT `+projectColumns+`,max(e.created_at)
FROM project p LEFT JOIN entry e ON e.slug=p.slug
WHERE ($1='' OR p.tier=$1) GROUP BY p.slug ORDER BY p.slug`, tier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Slug, &p.Name, &p.Tier, &p.HoursWK, &p.Type, &p.Description, &p.Goal, &p.Deadline, &p.NeedsMe, &p.Automate, &p.Stack, &p.UpdatedAt, &p.LastEntryAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (db *DB) GetProject(ctx context.Context, slug string, entryLimit int) (ProjectWithEntries, error) {
	p, err := scanProject(db.Pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM project p WHERE slug=$1`, slug))
	if err != nil {
		return ProjectWithEntries{}, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT id,slug,kind,body,source,client_id,created_at FROM entry WHERE slug=$1 ORDER BY created_at DESC,id DESC LIMIT $2`, slug, entryLimit)
	if err != nil {
		return ProjectWithEntries{}, err
	}
	defer rows.Close()
	result := ProjectWithEntries{Project: p, Entries: []Entry{}}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt); err != nil {
			return ProjectWithEntries{}, err
		}
		result.Entries = append(result.Entries, e)
	}
	return result, rows.Err()
}

func (db *DB) AppendEntry(ctx context.Context, slug, kind, body, source, clientID string) (Entry, error) {
	var e Entry
	err := db.Pool.QueryRow(ctx, `INSERT INTO entry(slug,kind,body,source,client_id) VALUES($1,$2,$3,$4,$5) RETURNING id,slug,kind,body,source,client_id,created_at`, slug, kind, body, source, clientID).
		Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt)
	return e, err
}

func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
