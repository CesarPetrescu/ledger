package ledger_test

import (
	"os"
	"strings"
	"testing"
)

func TestRepositoryLayout(t *testing.T) {
	for _, path := range []string{
		"migrations/0001_init.sql",
		"migrations/migrations.go",
		"seed/projects.json",
		"docker-compose.yml",
		"nginx.conf",
		"internal/retrieval/chunk.go",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("required path %s: %v", path, err)
		}
	}
	for _, path := range []string{"compose.yaml", "nginx", "internal/store/migrations", "internal/index"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("obsolete path still exists: %s", path)
		}
	}
}

func TestBackupArtifactsAreIgnoredWithoutIgnoringMigrations(t *testing.T) {
	for _, path := range []string{".gitignore", ".dockerignore"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := map[string]bool{}
		for _, line := range strings.Split(string(body), "\n") {
			lines[strings.TrimSpace(line)] = true
		}
		for _, pattern := range []string{"/ledger.sql", "*.dump", "*.backup", "backup/", "backups/"} {
			if !lines[pattern] {
				t.Errorf("%s missing %q", path, pattern)
			}
		}
		for _, pattern := range []string{"*.sql", "migrations/", "/migrations/"} {
			if lines[pattern] {
				t.Errorf("%s ignores migration SQL with %q", path, pattern)
			}
		}
	}
}

func TestPrivateContextIsExcludedFromGitAndDocker(t *testing.T) {
	for path, required := range map[string]string{".gitignore": "/.private/", ".dockerignore": ".private"} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := map[string]bool{}
		for _, line := range strings.Split(string(body), "\n") {
			lines[strings.TrimSpace(line)] = true
		}
		if !lines[required] {
			t.Errorf("%s does not contain the exact private-context exclusion %q", path, required)
		}
	}
}

func TestRetrievalUsesConcreteRows(t *testing.T) {
	body, err := os.ReadFile("internal/retrieval/search.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "type rowScanner interface") {
		t.Fatal("single-implementation rowScanner interface remains")
	}
}

func TestComposeAndProxyTopology(t *testing.T) {
	composeBody, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(composeBody)
	for _, required := range []string{
		"  ledger-auth:\n",
		"  ledger-mcp:\n",
		"  ledger-index:\n",
		"LEDGER_INDEX_URL: http://ledger-index:8083",
		"./nginx.conf:/etc/nginx/templates/default.conf.template:ro",
		"LEDGER_INTERNAL_PROXY_CIDR:",
	} {
		if !strings.Contains(compose, required) {
			t.Errorf("compose missing %q", required)
		}
	}
	for _, obsolete := range []string{"  auth:\n", "  mcp:\n", "  index:\n", "http://index:8083", "./nginx/"} {
		if strings.Contains(compose, obsolete) {
			t.Errorf("compose retains %q", obsolete)
		}
	}
	if strings.Count(compose, `"8080:8080"`) != 1 {
		t.Errorf("compose host exposure count = %d", strings.Count(compose, `"8080:8080"`))
	}

	nginxBody, err := os.ReadFile("nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	nginx := string(nginxBody)
	for _, required := range []string{
		"proxy_pass http://ledger-auth:8082",
		"proxy_pass http://ledger-mcp:8081",
		"proxy_set_header X-Ledger-Client-IP $remote_addr",
	} {
		if !strings.Contains(nginx, required) {
			t.Errorf("nginx missing %q", required)
		}
	}
}

func TestBuildAndAcceptanceTargets(t *testing.T) {
	dockerfile, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "FROM golang:1.25.14-alpine AS build") {
		t.Fatal("Docker builder is not pinned to current patched Go 1.25.14")
	}
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "github.com/jackc/pgx/v5 v5.9.2") {
		t.Fatal("pgx/v5 is not upgraded to v5.9.2")
	}
	makefile, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), ".PHONY: build test test-integration test-stack lint up down reindex") ||
		!strings.Contains(string(makefile), "test-integration:\n\tgo test -tags=integration -count=1 ./...") ||
		!strings.Contains(string(makefile), "test-stack:\n\tgo test -tags=stack -count=1 ./...") {
		t.Fatal("Makefile lacks reproducible, distinct integration and stack targets")
	}
}

func TestDocumentationContract(t *testing.T) {
	body, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(body)
	for _, required := range []string{
		"`ledger:read`", "`ledger:write`", "`ledger:read` is the default",
		"`oauth_client`, `oauth_code`, and `oauth_token`",
		"`make test-integration` runs the local-Docker/fake-inference testcontainers suite and does not run the `stack` build tag",
		"`make test-stack`",
		"X-Ledger-Client-IP",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README missing %q", required)
		}
	}
	for _, obsolete := range []string{"request `read`", "and/or `write`", "`client`, `code`, and `token`", "docker compose run --rm index"} {
		if strings.Contains(readme, obsolete) {
			t.Errorf("README retains obsolete contract %q", obsolete)
		}
	}
}
