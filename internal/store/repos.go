package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// MaxProjectRepos bounds how many repositories one project links.
const MaxProjectRepos = 20

var (
	ErrRepoExists    = errors.New("that repository is already linked to this project")
	ErrRepoLimit     = fmt.Errorf("a project links at most %d repositories", MaxProjectRepos)
	ErrRepoForbidden = errors.New("only the owner, or the agent that linked a repository, can unlink it")
)

// ProjectRepo is a Git repository that belongs to a project. Sync describes what the GitHub sync last
// saw; its text comes from GitHub and is data, never instructions.
type ProjectRepo struct {
	ID          string    `json:"id"`
	ProjectSlug string    `json:"project_slug"`
	URL         string    `json:"url"`
	Provider    string    `json:"provider"`
	Repo        string    `json:"repo"`
	WebURL      string    `json:"web_url,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	Path        string    `json:"path,omitempty"`
	Role        string    `json:"role,omitempty"`
	Note        string    `json:"note,omitempty"`
	AddedBy     string    `json:"added_by"`
	CreatedAt   time.Time `json:"created_at"`
	Sync        *RepoSync `json:"sync,omitempty"`
	addedClient string
}

type RepoSync struct {
	SyncedAt      time.Time  `json:"synced_at"`
	Error         string     `json:"error,omitempty"`
	DefaultBranch string     `json:"default_branch,omitempty"`
	Description   string     `json:"description,omitempty"`
	Private       *bool      `json:"private,omitempty"`
	Archived      *bool      `json:"archived,omitempty"`
	HeadSHA       string     `json:"head_sha,omitempty"`
	HeadMessage   string     `json:"head_message,omitempty"`
	HeadAt        *time.Time `json:"head_at,omitempty"`
	// OpenPRs counts open pull requests, up to 100.
	OpenPRs         *int       `json:"open_prs,omitempty"`
	LatestRelease   string     `json:"latest_release,omitempty"`
	LatestReleaseAt *time.Time `json:"latest_release_at,omitempty"`
}

// RepoLocation is a parsed repository URL.
type RepoLocation struct {
	URL      string
	Provider string
	Host     string
	Repo     string
	WebURL   string
	Key      string
}

var (
	scpLike    = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+):(.+)$`)
	githubRepo = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
)

// ParseRepoURL accepts HTTPS, HTTP, SSH, and scp-style (git@host:owner/repo) URLs. It refuses any URL that
// carries a password or token, so a secret cannot enter Ledger through a link.
func ParseRepoURL(raw string) (RepoLocation, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 500 || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) >= 0 {
		return RepoLocation{}, errors.New("url must be a Git repository URL of at most 500 characters")
	}
	var host, path, scheme string
	if !strings.Contains(raw, "://") {
		m := scpLike.FindStringSubmatch(raw)
		if m == nil {
			return RepoLocation{}, errors.New("url must look like https://host/owner/repo or git@host:owner/repo.git")
		}
		host, path, scheme = m[1], m[2], "ssh"
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return RepoLocation{}, errors.New("url must be a Git repository URL without a query or fragment")
		}
		scheme = strings.ToLower(u.Scheme)
		switch scheme {
		case "https", "http":
			if u.User != nil {
				return RepoLocation{}, errors.New("url must not contain a user name, password, or token; remove the part before @")
			}
		case "ssh", "git":
			if _, hasPassword := u.User.Password(); hasPassword {
				return RepoLocation{}, errors.New("url must not contain a password")
			}
		default:
			return RepoLocation{}, errors.New("url must use https, http, ssh, or git")
		}
		host, path = u.Hostname(), u.Path
	}
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if path == "" || strings.Contains("/"+path+"/", "/../") || strings.Contains("/"+path+"/", "/./") || strings.Contains(path, "//") {
		return RepoLocation{}, errors.New("url must name a repository path, such as owner/repo")
	}
	provider := map[string]string{"github.com": "github", "gitlab.com": "gitlab", "bitbucket.org": "bitbucket", "codeberg.org": "forgejo"}[host]
	if provider == "" {
		provider = "git"
	}
	if provider == "github" && !githubRepo.MatchString(path) {
		return RepoLocation{}, errors.New("a GitHub url must be github.com/OWNER/REPO")
	}
	web := ""
	switch {
	case provider != "git":
		web = "https://" + host + "/" + path
	case scheme == "https" || scheme == "http":
		u, _ := url.Parse(raw)
		web = scheme + "://" + u.Host + "/" + path
	}
	return RepoLocation{URL: raw, Provider: provider, Host: host, Repo: path, WebURL: web, Key: strings.ToLower(host + "/" + path)}, nil
}

// NewRepo is a repository to link to a project.
type NewRepo struct {
	ProjectSlug string
	URL         string
	Branch      string
	Path        string
	Role        string
	Note        string
	Source      string
	ClientID    string
}

func validateRepoText(name, value string, max int, oneWord bool) error {
	if len([]rune(value)) > max || strings.IndexFunc(value, unicode.IsControl) >= 0 || oneWord && strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		if oneWord {
			return fmt.Errorf("%s must be at most %d characters without spaces", name, max)
		}
		return fmt.Errorf("%s must be one line of at most %d characters", name, max)
	}
	return nil
}

const repoColumns = `id::text,project_slug,url,provider,repo,web_url,branch,path,role,note,added_source,added_client_id,created_at,
synced_at,sync_error,default_branch,description,private,archived,head_sha,head_message,head_at,open_prs,latest_release,latest_release_at`

func scanRepo(row pgx.Row) (ProjectRepo, error) {
	var r ProjectRepo
	var s RepoSync
	var syncedAt *time.Time
	err := row.Scan(&r.ID, &r.ProjectSlug, &r.URL, &r.Provider, &r.Repo, &r.WebURL, &r.Branch, &r.Path, &r.Role, &r.Note, &r.AddedBy, &r.addedClient, &r.CreatedAt,
		&syncedAt, &s.Error, &s.DefaultBranch, &s.Description, &s.Private, &s.Archived, &s.HeadSHA, &s.HeadMessage, &s.HeadAt, &s.OpenPRs, &s.LatestRelease, &s.LatestReleaseAt)
	if syncedAt != nil {
		s.SyncedAt = *syncedAt
		r.Sync = &s
	}
	return r, err
}

// LinkRepo links a repository to a project.
func (db *DB) LinkRepo(ctx context.Context, n NewRepo) (ProjectRepo, error) {
	if err := ValidateProjectSlug(n.ProjectSlug); err != nil {
		return ProjectRepo{}, err
	}
	location, err := ParseRepoURL(n.URL)
	if err != nil {
		return ProjectRepo{}, err
	}
	n.Branch, n.Path, n.Role, n.Note = strings.TrimSpace(n.Branch), strings.Trim(strings.TrimSpace(n.Path), "/"), strings.TrimSpace(n.Role), strings.TrimSpace(n.Note)
	for _, check := range []error{
		validateRepoText("branch", n.Branch, 200, true),
		validateRepoText("path", n.Path, 300, true),
		validateRepoText("role", n.Role, 60, false),
		validateRepoText("note", n.Note, 500, false),
	} {
		if check != nil {
			return ProjectRepo{}, check
		}
	}
	if strings.Contains("/"+n.Path+"/", "/../") {
		return ProjectRepo{}, errors.New("path must be a folder inside the repository")
	}
	if err := validateHandoffAttribution(n.Source, n.ClientID); err != nil {
		return ProjectRepo{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return ProjectRepo{}, err
	}
	defer tx.Rollback(ctx)
	// Lock the project so concurrent links count against the same limit.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM project WHERE slug=$1 FOR UPDATE`, n.ProjectSlug); err != nil {
		return ProjectRepo{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_repo WHERE project_slug=$1`, n.ProjectSlug).Scan(&count); err != nil {
		return ProjectRepo{}, err
	}
	if count >= MaxProjectRepos {
		return ProjectRepo{}, ErrRepoLimit
	}
	repo, err := scanRepo(tx.QueryRow(ctx, `INSERT INTO project_repo(project_slug,url,repo_key,provider,repo,web_url,branch,path,role,note,added_source,added_client_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (project_slug,repo_key,path) DO NOTHING RETURNING `+repoColumns,
		n.ProjectSlug, location.URL, location.Key, location.Provider, location.Repo, location.WebURL, n.Branch, n.Path, n.Role, n.Note, n.Source, n.ClientID))
	if IsNotFound(err) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM project WHERE slug=$1)`, n.ProjectSlug).Scan(&exists); err != nil {
			return ProjectRepo{}, err
		}
		if !exists {
			return ProjectRepo{}, pgx.ErrNoRows
		}
		return ProjectRepo{}, ErrRepoExists
	}
	if IsForeignKeyViolation(err) {
		return ProjectRepo{}, pgx.ErrNoRows
	}
	if err != nil {
		return ProjectRepo{}, err
	}
	return repo, tx.Commit(ctx)
}

// ListRepos returns a project's repositories, or every project's when slug is empty.
func (db *DB) ListRepos(ctx context.Context, slug string) ([]ProjectRepo, error) {
	rows, err := db.Pool.Query(ctx, `SELECT `+repoColumns+` FROM project_repo WHERE $1='' OR project_slug=$1 ORDER BY project_slug,created_at,id`, slug)
	if err != nil {
		return nil, err
	}
	repos, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ProjectRepo, error) { return scanRepo(row) })
	if repos == nil {
		repos = []ProjectRepo{}
	}
	return repos, err
}

// UnlinkRepo removes a link. An agent may remove only the links it added; the owner removes any.
func (db *DB) UnlinkRepo(ctx context.Context, id int64, clientID string, owner bool) (ProjectRepo, error) {
	repo, err := scanRepo(db.Pool.QueryRow(ctx, `SELECT `+repoColumns+` FROM project_repo WHERE id=$1`, id))
	if err != nil {
		return ProjectRepo{}, err
	}
	if !owner && repo.addedClient != clientID {
		return ProjectRepo{}, ErrRepoForbidden
	}
	tag, err := db.Pool.Exec(ctx, `DELETE FROM project_repo WHERE id=$1 AND ($2 OR added_client_id=$3)`, id, owner, clientID)
	if err == nil && tag.RowsAffected() == 0 {
		err = pgx.ErrNoRows
	}
	return repo, err
}

// ReposDueForSync returns GitHub repositories not synced within every, never-synced ones first.
func (db *DB) ReposDueForSync(ctx context.Context, every time.Duration, limit int) ([]ProjectRepo, error) {
	rows, err := db.Pool.Query(ctx, `SELECT `+repoColumns+` FROM project_repo WHERE provider='github' AND (synced_at IS NULL OR synced_at<now()-make_interval(secs=>$1))
ORDER BY synced_at NULLS FIRST,id LIMIT $2`, every.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ProjectRepo, error) { return scanRepo(row) })
}

// SaveRepoSync records what the sync saw for one link, only while the token it used is still the saved
// one (token is its SavedAt): a removed or replaced token's late results are dropped.
func (db *DB) SaveRepoSync(ctx context.Context, id string, token time.Time, s RepoSync) error {
	_, err := db.Pool.Exec(ctx, `UPDATE project_repo SET synced_at=now(),sync_error=$2,default_branch=$3,description=$4,private=$5,archived=$6,head_sha=$7,head_message=$8,head_at=$9,
open_prs=$10,latest_release=$11,latest_release_at=$12 WHERE id=$1::bigint AND EXISTS (SELECT 1 FROM github_sync WHERE saved_at=$13 FOR SHARE)`,
		id, s.Error, s.DefaultBranch, s.Description, s.Private, s.Archived, s.HeadSHA, s.HeadMessage, s.HeadAt, s.OpenPRs, s.LatestRelease, s.LatestReleaseAt, token)
	return err
}

// GitHubSyncState is the stored sync token and its status. Ciphertext never leaves the GitHub package.
type GitHubSyncState struct {
	Ciphertext []byte
	Hint       string
	Login      string
	SavedAt    time.Time
	LastRunAt  *time.Time
	LastError  string
}

func (db *DB) GitHubSync(ctx context.Context) (GitHubSyncState, error) {
	var s GitHubSyncState
	err := db.Pool.QueryRow(ctx, `SELECT token_ciphertext,token_hint,login,saved_at,last_run_at,last_error FROM github_sync`).
		Scan(&s.Ciphertext, &s.Hint, &s.Login, &s.SavedAt, &s.LastRunAt, &s.LastError)
	return s, err
}

// SetGitHubSync replaces the token and marks every GitHub repository for a fresh sync.
func (db *DB) SetGitHubSync(ctx context.Context, ciphertext []byte, hint, login string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO github_sync(token_ciphertext,token_hint,login) VALUES($1,$2,$3)
ON CONFLICT (singleton) DO UPDATE SET token_ciphertext=EXCLUDED.token_ciphertext,token_hint=EXCLUDED.token_hint,login=EXCLUDED.login,saved_at=now(),last_run_at=NULL,last_error=''`, ciphertext, hint, login); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_repo SET synced_at=NULL WHERE provider='github'`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteGitHubSync forgets the token and what it synced.
func (db *DB) DeleteGitHubSync(ctx context.Context) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM github_sync`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE project_repo SET synced_at=NULL,sync_error='',default_branch='',description='',private=NULL,archived=NULL,head_sha='',head_message='',head_at=NULL,
open_prs=NULL,latest_release='',latest_release_at=NULL`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NoteGitHubSyncRun records a round's outcome for the token it used (its SavedAt).
func (db *DB) NoteGitHubSyncRun(ctx context.Context, token time.Time, problem string) error {
	_, err := db.Pool.Exec(ctx, `UPDATE github_sync SET last_run_at=now(),last_error=$1 WHERE saved_at=$2`, problem, token)
	return err
}
