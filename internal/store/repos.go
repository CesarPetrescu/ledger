package store

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	slashpath "path"
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
	repoProviders = map[string]string{"github.com": "github", "gitlab.com": "gitlab", "bitbucket.org": "bitbucket", "codeberg.org": "forgejo"}
	scpLike       = regexp.MustCompile(`^(?:([^@/:]+)@)?([A-Za-z0-9.-]+):(.+)$`)
	// An SSH Git URL logs in as an account: git on hosted forges, or a person's login on their own server.
	sshUser      = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,19}$`)
	tokenMarkers = []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "gldt-", "glptt-", "x-access-token", "oauth", "token", "secret", "pat_"}
	githubRepo   = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
)

var errUserInURL = errors.New("url's SSH user must be a plain account name such as git; never put a password or token in a URL")

// plainSSHUser accepts a short lowercase account name (at most 20 characters and 4 digits) and nothing
// shaped like a token, so a secret cannot enter Ledger as an SSH user name.
func plainSSHUser(user string) bool {
	if user == "" {
		return true
	}
	lower := strings.ToLower(user)
	for _, marker := range tokenMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	// Real account names are short and mostly letters; random tokens are long and full of digits.
	digits := 0
	for _, r := range user {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return sshUser.MatchString(user) && digits <= 4
}

// ParseRepoURL accepts HTTPS, HTTP, SSH, and scp-style (git@host:owner/repo) URLs. It refuses any URL that
// carries a password or token, so a secret cannot enter Ledger through a link.
func ParseRepoURL(raw string) (RepoLocation, error) {
	raw = strings.TrimSpace(raw)
	unsafe := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }
	if raw == "" || len(raw) > 500 || strings.IndexFunc(raw, unsafe) >= 0 {
		return RepoLocation{}, errors.New("url must be a Git repository URL of at most 500 characters")
	}
	var host, port, path, scheme string
	if !strings.Contains(raw, "://") {
		m := scpLike.FindStringSubmatch(raw)
		if m == nil {
			return RepoLocation{}, errors.New("url must look like https://host/owner/repo or git@host:owner/repo.git")
		}
		if !plainSSHUser(m[1]) {
			return RepoLocation{}, errUserInURL
		}
		host, path, scheme = m[2], m[3], "ssh"
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
			if _, hasPassword := u.User.Password(); hasPassword || !plainSSHUser(u.User.Username()) {
				return RepoLocation{}, errUserInURL
			}
		default:
			return RepoLocation{}, errors.New("url must use https, http, ssh, or git")
		}
		host, port, path = u.Hostname(), u.Port(), u.Path
		// A default port names the same server as none at all; any other port is another server.
		if port == map[string]string{"https": "443", "http": "80", "ssh": "22", "git": "9418"}[scheme] {
			port = ""
		}
	}
	// www. is an alias only on the hosted providers; on another server it may be a different host.
	// A terminal dot names the same host (github.com. is github.com).
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return RepoLocation{}, errors.New("url must name a host")
	}
	if bare := strings.TrimPrefix(host, "www."); repoProviders[bare] != "" {
		host = bare
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	// url.Parse decodes percent escapes, so check the decoded path too: %0A must not become a newline.
	if strings.IndexFunc(path, unsafe) >= 0 || strings.ContainsAny(path, "?#%") {
		return RepoLocation{}, errors.New("url's repository path has characters a repository name cannot have")
	}
	if path == "" || strings.Contains("/"+path+"/", "/../") || strings.Contains("/"+path+"/", "/./") || strings.Contains(path, "//") {
		return RepoLocation{}, errors.New("url must name a repository path, such as owner/repo")
	}
	provider := repoProviders[host]
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
	// Brackets keep an IPv6 address apart from its port: [::1]:3000 is not [::1:3000].
	server := host
	if port != "" {
		server = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		server = "[" + host + "]"
	}
	// Hosted providers match owner and repository names in any case; a generic Git server may not.
	key := path
	if provider != "git" {
		key = strings.ToLower(path)
	}
	return RepoLocation{URL: raw, Provider: provider, Host: host, Repo: path, WebURL: web, Key: server + "/" + key}, nil
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
	n.Branch, n.Role, n.Note = strings.TrimSpace(n.Branch), strings.TrimSpace(n.Role), strings.TrimSpace(n.Note)
	// Folders use forward slashes only: a backslash would be a separator on Windows and slip past these checks.
	if strings.ContainsRune(n.Path, '\\') || strings.ContainsRune(n.Branch, '\\') {
		return ProjectRepo{}, errors.New("path and branch use forward slashes, never backslashes")
	}
	// One spelling per folder, so services/./web and services//web are the same link as services/web.
	if n.Path = strings.Trim(strings.TrimSpace(n.Path), "/"); n.Path != "" {
		n.Path = slashpath.Clean(n.Path)
		if n.Path == "." {
			n.Path = ""
		}
	}
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
	if n.Path == ".." || strings.HasPrefix(n.Path, "../") {
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

// RepoSyncResult is what one round saw for one link.
type RepoSyncResult struct {
	ID   string
	Sync RepoSync
}

// SaveSyncRound records a round's results and outcome in one transaction, so an open console refreshes
// once per round rather than once per repository. It writes nothing unless the token the round used (its
// SavedAt) is still the saved one: a removed or replaced token's late results are dropped.
func (db *DB) SaveSyncRound(ctx context.Context, token time.Time, results []RepoSyncResult, problem string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM github_sync WHERE saved_at=$1 FOR SHARE`, token).Scan(&current); IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, r := range results {
		s := r.Sync
		// A failed check keeps the last good snapshot and only reports the problem.
		if s.Error != "" {
			if _, err := tx.Exec(ctx, `UPDATE project_repo SET synced_at=now(),sync_error=$2 WHERE id=$1::bigint`, r.ID, s.Error); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE project_repo SET synced_at=now(),sync_error=$2,default_branch=$3,description=$4,private=$5,archived=$6,head_sha=$7,head_message=$8,head_at=$9,
open_prs=$10,latest_release=$11,latest_release_at=$12 WHERE id=$1::bigint`, r.ID, s.Error, s.DefaultBranch, s.Description, s.Private, s.Archived, s.HeadSHA, s.HeadMessage, s.HeadAt, s.OpenPRs, s.LatestRelease, s.LatestReleaseAt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE github_sync SET last_run_at=now(),last_error=$1`, problem); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// githubSyncLock serializes saving and removing the token.
const githubSyncLock = 7103379

// SetGitHubSync stores a token once check (which asks GitHub who it belongs to) passes, and clears what the
// previous token synced, so every repository syncs afresh with the new one. Saves and removals take turns: a removal requested while a token is
// being checked runs after the save, so turning sync off is never undone by an older request.
func (db *DB) SetGitHubSync(ctx context.Context, ciphertext []byte, hint string, check func() (login string, err error)) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, githubSyncLock); err != nil {
		return err
	}
	login, err := check()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO github_sync(token_ciphertext,token_hint,login) VALUES($1,$2,$3)
ON CONFLICT (singleton) DO UPDATE SET token_ciphertext=EXCLUDED.token_ciphertext,token_hint=EXCLUDED.token_hint,login=EXCLUDED.login,saved_at=now(),last_run_at=NULL,last_error=''`, ciphertext, hint, login); err != nil {
		return err
	}
	// What the previous token saw is not this token's to show: start every repository afresh.
	if err := clearRepoSync(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// clearRepoSync forgets everything a token synced.
func clearRepoSync(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `UPDATE project_repo SET synced_at=NULL,sync_error='',default_branch='',description='',private=NULL,archived=NULL,head_sha='',head_message='',head_at=NULL,
open_prs=NULL,latest_release='',latest_release_at=NULL`)
	return err
}

// DeleteGitHubSync forgets the token and what it synced, after any save in progress.
func (db *DB) DeleteGitHubSync(ctx context.Context) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, githubSyncLock); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM github_sync`); err != nil {
		return err
	}
	if err := clearRepoSync(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NoteGitHubSyncRun records a round's outcome for the token it used (its SavedAt).
func (db *DB) NoteGitHubSyncRun(ctx context.Context, token time.Time, problem string) error {
	_, err := db.Pool.Exec(ctx, `UPDATE github_sync SET last_run_at=now(),last_error=$1 WHERE saved_at=$2`, problem, token)
	return err
}
