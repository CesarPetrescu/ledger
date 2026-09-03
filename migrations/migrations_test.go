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
