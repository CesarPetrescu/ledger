package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// AgentSummary is one agent's recent work, for the Agents page. An agent is
// a writer name (an entry's source); the owner's console is left out.
type AgentSummary struct {
	Name        string         `json:"name"`
	LastActive  *time.Time     `json:"last_active,omitempty"`
	WeekEntries int            `json:"week_entries"`
	Entries     int            `json:"entries"`
	Projects    []AgentProject `json:"projects"` // written to this week
	OpenAsks    int            `json:"open_asks"`
	Handoffs    int            `json:"handoffs"` // claimed and not done
}

type AgentProject struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Agents lists every agent that wrote an entry or holds a handoff, most
// recently active first. owner is the console's own writer name.
// ponytail: correlated counts per agent; precompute if agents or entries grow large.
func (db *DB) Agents(ctx context.Context, owner string) ([]AgentSummary, error) {
	rows, err := db.Pool.Query(ctx, `WITH names AS (
  SELECT source AS name FROM entry WHERE source<>$1
  UNION SELECT claimed_source FROM handoff_message WHERE claimed_source<>$1 AND work_state IN ('in_progress','blocked'))
SELECT n.name,
 (SELECT max(created_at) FROM entry WHERE source=n.name),
 (SELECT count(*) FROM entry WHERE source=n.name AND created_at>now()-interval '7 days'),
 (SELECT count(*) FROM entry WHERE source=n.name),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('slug',p.slug,'name',p.name) ORDER BY p.name) FROM project p
   WHERE EXISTS (SELECT 1 FROM entry e WHERE e.slug=p.slug AND e.source=n.name AND e.created_at>now()-interval '7 days')),'[]'),
 (SELECT count(*) FROM entry e JOIN entry_meta m ON m.entry_id=e.id LEFT JOIN entry_owner_state o ON o.entry_id=e.id
   WHERE e.source=n.name AND m.ask<>'' AND o.handled_at IS NULL AND (o.snoozed_until IS NULL OR o.snoozed_until<=current_date)),
 (SELECT count(*) FROM handoff_message WHERE claimed_source=n.name AND work_state IN ('in_progress','blocked'))
FROM names n ORDER BY 2 DESC NULLS LAST,n.name`, owner)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AgentSummary, error) {
		var a AgentSummary
		return a, row.Scan(&a.Name, &a.LastActive, &a.WeekEntries, &a.Entries, &a.Projects, &a.OpenAsks, &a.Handoffs)
	})
}
