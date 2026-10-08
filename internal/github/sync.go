// Package github keeps linked GitHub repositories' activity fresh with the owner's read-only token. The
// token is encrypted at rest and never leaves this package: agents see only what the sync recorded.
package github

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/jackc/pgx/v5"
)

const (
	// Every is how often each repository is refreshed.
	Every = 15 * time.Minute
	// tick is how often the loop looks for repositories that are due, so a new link syncs within a minute.
	tick = time.Minute
	// perTick bounds the GitHub calls one tick makes (about four per repository).
	perTick = 25
)

var ErrRejected = errors.New("GitHub rejected the token")

type Sync struct {
	db   *store.DB
	aead cipher.AEAD
	http *http.Client
	api  string
}

// New derives the token's encryption key from the server's credential key, separately from the
// calendar's.
func New(db *store.DB, encryptionKey string, client *http.Client) (*Sync, error) {
	if len(encryptionKey) < 32 {
		return nil, errors.New("encryption key must be at least 32 characters")
	}
	key := sha256.Sum256([]byte("ledger-github-token-v1\x00" + encryptionKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	copied := *client
	copied.Timeout = 20 * time.Second
	return &Sync{db: db, aead: aead, http: &copied, api: "https://api.github.com"}, nil
}

// WithAPI points the sync at another GitHub API root, for tests.
func (s *Sync) WithAPI(root string) *Sync {
	copied := *s
	copied.api = strings.TrimRight(root, "/")
	return &copied
}

// Status is what the console shows: never the token, only its last characters.
type Status struct {
	Configured bool       `json:"configured"`
	Hint       string     `json:"hint,omitempty"`
	Login      string     `json:"login,omitempty"`
	SavedAt    *time.Time `json:"saved_at,omitempty"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

func (s *Sync) Status(ctx context.Context) (Status, error) {
	state, err := s.db.GitHubSync(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return Status{Configured: true, Hint: state.Hint, Login: state.Login, SavedAt: &state.SavedAt, LastRunAt: state.LastRunAt, LastError: state.LastError}, nil
}

// SetToken checks the token with GitHub, then stores it encrypted.
func (s *Sync) SetToken(ctx context.Context, token string) (Status, error) {
	token = strings.TrimSpace(token)
	if len(token) < 20 || len(token) > 255 || strings.ContainsAny(token, " \t\r\n") {
		return Status{}, errors.New("paste a GitHub token: a fine-grained token starts with github_pat_")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Status{}, err
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(token), []byte("github-sync"))
	// GitHub is asked while saves and removals are held, so a removal meanwhile waits and wins.
	check := func() (string, error) {
		var user struct {
			Login string `json:"login"`
		}
		err := s.get(ctx, token, "/user", &user)
		return user.Login, err
	}
	if err := s.db.SetGitHubSync(ctx, sealed, "…"+token[len(token)-4:], check); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Sync) Clear(ctx context.Context) error { return s.db.DeleteGitHubSync(ctx) }

// token returns the saved token and when it was saved, which tells one token from its replacement.
func (s *Sync) token(ctx context.Context) (string, time.Time, error) {
	state, err := s.db.GitHubSync(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	size := s.aead.NonceSize()
	if len(state.Ciphertext) < size {
		return "", state.SavedAt, errors.New("stored GitHub token is unreadable; save it again")
	}
	plain, err := s.aead.Open(nil, state.Ciphertext[:size], state.Ciphertext[size:], []byte("github-sync"))
	if err != nil {
		return "", state.SavedAt, errors.New("stored GitHub token is unreadable; save it again")
	}
	return string(plain), state.SavedAt, nil
}

// Run syncs due repositories every minute until ctx ends.
func (s *Sync) Run(ctx context.Context) error {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		if err := s.SyncDue(ctx); err != nil && ctx.Err() == nil {
			log.Printf("github sync: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// SyncDue refreshes the repositories that are due. Without a token it does nothing.
func (s *Sync) SyncDue(ctx context.Context) error {
	token, saved, err := s.token(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return s.db.NoteGitHubSyncRun(ctx, saved, err.Error())
	}
	repos, err := s.db.ReposDueForSync(ctx, Every, perTick)
	if err != nil || len(repos) == 0 {
		return err
	}
	problem := ""
	for _, repo := range repos {
		result, err := s.syncRepo(ctx, token, repo)
		if errors.Is(err, ErrRejected) || errors.Is(err, errRateLimited) {
			// The token or the rate limit affects every repository; try again next tick.
			problem = err.Error()
			break
		}
		if err != nil {
			result = store.RepoSync{Error: err.Error()}
		}
		// Dropped if the token was removed or replaced meanwhile.
		if err := s.db.SaveRepoSync(ctx, repo.ID, saved, result); err != nil {
			return err
		}
	}
	return s.db.NoteGitHubSyncRun(ctx, saved, problem)
}

var errRateLimited = errors.New("GitHub rate limit reached; the sync resumes when it resets")

func (s *Sync) syncRepo(ctx context.Context, token string, repo store.ProjectRepo) (store.RepoSync, error) {
	base := "/repos/" + repo.Repo
	var info struct {
		DefaultBranch string `json:"default_branch"`
		Description   string `json:"description"`
		Private       bool   `json:"private"`
		Archived      bool   `json:"archived"`
	}
	if err := s.get(ctx, token, base, &info); err != nil {
		return store.RepoSync{}, err
	}
	out := store.RepoSync{DefaultBranch: clip(info.DefaultBranch, 200), Description: clip(info.Description, 300), Private: &info.Private, Archived: &info.Archived}
	branch := repo.Branch
	if branch == "" {
		branch = info.DefaultBranch
	}
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message   string `json:"message"`
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	// An empty repository answers 409; it simply has no head yet.
	if err := s.get(ctx, token, base+"/commits?per_page=1&sha="+url.QueryEscape(branch), &commits); err != nil && !isStatus(err, http.StatusConflict) {
		return store.RepoSync{}, err
	}
	if len(commits) == 1 {
		message, _, _ := strings.Cut(commits[0].Commit.Message, "\n")
		date := commits[0].Commit.Committer.Date
		out.HeadSHA, out.HeadMessage, out.HeadAt = clip(commits[0].SHA, 64), clip(message, 200), &date
	}
	var pulls []json.RawMessage
	if err := s.get(ctx, token, base+"/pulls?state=open&per_page=100", &pulls); err != nil {
		return store.RepoSync{}, err
	}
	open := len(pulls)
	out.OpenPRs = &open
	var release struct {
		TagName     string    `json:"tag_name"`
		PublishedAt time.Time `json:"published_at"`
	}
	switch err := s.get(ctx, token, base+"/releases/latest", &release); {
	case err == nil:
		out.LatestRelease, out.LatestReleaseAt = clip(release.TagName, 200), &release.PublishedAt
	case !isStatus(err, http.StatusNotFound):
		return store.RepoSync{}, err
	}
	return out, nil
}

type statusError struct {
	code    int
	message string
}

func (e *statusError) Error() string { return e.message }

func isStatus(err error, code int) bool {
	var status *statusError
	return errors.As(err, &status) && status.code == code
}

func (s *Sync) get(ctx context.Context, token, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.api+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "ledger-github-sync")
	response, err := s.http.Do(request)
	if err != nil {
		return fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out)
	case response.StatusCode == http.StatusUnauthorized:
		return ErrRejected
	case (response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests) && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != ""):
		return errRateLimited
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusForbidden:
		return &statusError{response.StatusCode, "not found, or the token cannot read this repository"}
	default:
		return &statusError{response.StatusCode, fmt.Sprintf("GitHub answered HTTP %d", response.StatusCode)}
	}
}

func clip(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max-1]) + "…"
}
