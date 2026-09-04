package migrations

import (
	"strings"
	"testing"
)

func TestInitialMigrationContract(t *testing.T) {
	body, err := Files.ReadFile("0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(body))
	for _, required := range []string{
		"hours_wk integer not null default 0 check (hours_wk between 0 and 168)",
		"deadline text not null default ''",
		"needs_me text not null default ''",
		"slug text not null references project(slug) on delete cascade",
		"create table oauth_client",
		"create table oauth_code",
		"create table oauth_token",
		"hash bytea primary key",
		"code_challenge text not null",
		"scope text not null",
		"create index oauth_token_family_idx on oauth_token(family)",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"create table client",
		"create table code",
		"create table token",
		"project_slug",
		"fetched_at",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("migration retains obsolete schema %q", forbidden)
		}
	}
	codeStart := strings.Index(sql, "create table oauth_code")
	if codeStart >= 0 {
		codeEnd := strings.Index(sql[codeStart:], ");")
		if codeEnd >= 0 && strings.Contains(sql[codeStart:codeStart+codeEnd], "family") {
			t.Error("oauth_code must not contain a family column")
		}
	}
}

func TestAdminSessionMigrationStoresHashesOnly(t *testing.T) {
	body, err := Files.ReadFile("0002_admin_session.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(body))
	for _, required := range []string{
		"create table admin_session",
		"hash bytea primary key check (octet_length(hash) = 32)",
		"csrf_token text not null",
		"created_at timestamptz not null default now()",
		"expires_at timestamptz not null",
		"last_seen_at timestamptz not null default now()",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("admin session migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"session_id", "password", "token bytea", "secret"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("admin session migration stores raw credential column %q", forbidden)
		}
	}
	entries, err := Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 4 || names[0] != "0001_init.sql" || names[1] != "0002_admin_session.sql" || names[2] != "0003_admin_events.sql" || names[3] != "0004_calendar.sql" {
		t.Fatalf("migration files = %v, want strictly numbered sequence", names)
	}
}

func TestAdminEventsMigrationCoversEveryLiveSurface(t *testing.T) {
	body, err := Files.ReadFile("0003_admin_events.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(body))
	for _, required := range []string{"ledger_admin_event", "'project'", "'entry'", "'oauth_client'", "'oauth_token'", "'chunk'", "'admin_session'", "after update of kind, redirect_uris, name", "for each statement"} {
		if !strings.Contains(sql, required) {
			t.Errorf("admin events migration missing %q", required)
		}
	}
}

func TestCalendarMigrationStoresEncryptedCredential(t *testing.T) {
	body, err := Files.ReadFile("0004_calendar.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(body))
	for _, required := range []string{"create table calendar_account", "password_ciphertext bytea", "selected_calendars text[]", "notify_admin_event('calendar')"} {
		if !strings.Contains(sql, required) {
			t.Errorf("calendar migration missing %q", required)
		}
	}
	if strings.Contains(sql, "password text") {
		t.Error("calendar migration must not store a plaintext password")
	}
}
