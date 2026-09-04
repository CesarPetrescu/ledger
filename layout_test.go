package ledger_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

type composeService struct {
	Image       string         `yaml:"image"`
	Build       any            `yaml:"build"`
	Ports       []any          `yaml:"ports"`
	Environment map[string]any `yaml:"environment"`
	Networks    any            `yaml:"networks"`
	DependsOn   map[string]any `yaml:"depends_on"`
}

func loadCompose(t *testing.T) map[string]composeService {
	t.Helper()
	body, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &compose); err != nil {
		t.Fatal(err)
	}
	return compose.Services
}

func TestOnlyTheEdgeProxyPublishesAHostPort(t *testing.T) {
	services := loadCompose(t)
	for _, name := range []string{"nginx", "ledger-auth", "ledger-mcp", "ledger-index", "ledger-admin", "ledger-frontend", "postgres"} {
		if _, ok := services[name]; !ok {
			t.Errorf("compose service %s missing", name)
		}
	}
	for name, service := range services {
		if name == "nginx" {
			if len(service.Ports) != 1 || service.Ports[0] != "8080:8080" {
				t.Errorf("nginx ports = %v, want exactly 8080:8080", service.Ports)
			}
			continue
		}
		if len(service.Ports) != 0 {
			t.Errorf("service %s publishes host ports %v", name, service.Ports)
		}
		if networks := fmt.Sprint(service.Networks); !strings.Contains(networks, "ledger-internal") {
			t.Errorf("service %s is not attached to ledger-internal: %v", name, service.Networks)
		}
	}
	admin := services["ledger-admin"]
	for _, required := range []string{"LEDGER_ADMIN_PASSWORD_HASH", "LEDGER_INTERNAL_PROXY_CIDR", "LEDGER_INDEX_URL", "LEDGER_DATABASE_URL", "LEDGER_PUBLIC_URL"} {
		if _, ok := admin.Environment[required]; !ok {
			t.Errorf("ledger-admin lacks %s", required)
		}
	}
	if _, ok := admin.Environment["LEDGER_PASSWORD_HASH"]; ok {
		t.Error("ledger-admin receives the OAuth approval password hash")
	}
	if value := fmt.Sprint(admin.Environment["LEDGER_ADMIN_PASSWORD_HASH"]); !strings.Contains(value, ":?") {
		t.Errorf("LEDGER_ADMIN_PASSWORD_HASH is not required: %s", value)
	}
	frontend := services["ledger-frontend"]
	if len(frontend.Environment) != 0 {
		t.Errorf("ledger-frontend receives environment %v; a static container must hold no configuration or secrets", frontend.Environment)
	}
	if frontend.Build == nil || !strings.Contains(fmt.Sprint(frontend.Build), "frontend") {
		t.Errorf("ledger-frontend build = %v", frontend.Build)
	}
	for _, dependency := range []string{"ledger-admin", "ledger-frontend"} {
		if _, ok := services["nginx"].DependsOn[dependency]; !ok {
			t.Errorf("nginx does not depend on %s", dependency)
		}
	}
}

func TestComposeUsesTheConfiguredPostgresPassword(t *testing.T) {
	services := loadCompose(t)
	want := "postgres://ledger:${LEDGER_POSTGRES_PASSWORD:?set LEDGER_POSTGRES_PASSWORD}@postgres:5432/ledger?sslmode=disable"
	for _, name := range []string{"ledger-auth", "ledger-mcp", "ledger-index", "ledger-admin"} {
		got := fmt.Sprint(services[name].Environment["LEDGER_DATABASE_URL"])
		if got != want {
			t.Errorf("%s LEDGER_DATABASE_URL = %q, want configured password interpolation", name, got)
		}
	}
}

func TestEdgeProxyRoutesAdminSameOriginAndKeepsInternalRoutesPrivate(t *testing.T) {
	body, err := os.ReadFile("nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	nginx := string(body)
	for _, required := range []string{
		"location = / { return 302 /admin/; }",
		"location = /admin { return 302 /admin/; }",
		"absolute_redirect off;",
		"location ^~ /admin/api",
		"proxy_pass http://ledger-admin:8084",
		"location ^~ /admin/",
		"proxy_pass http://ledger-frontend:8085",
		"location / { return 404; }",
	} {
		if !strings.Contains(nginx, required) {
			t.Errorf("nginx missing %q", required)
		}
	}
	for _, forbidden := range []string{"ledger-index", "/search", "/reindex", "add_header Set-Cookie"} {
		if strings.Contains(nginx, forbidden) {
			t.Errorf("nginx exposes %q", forbidden)
		}
	}
	adminAPI := nginx[strings.Index(nginx, "location ^~ /admin/api"):]
	adminAPI = adminAPI[:strings.Index(adminAPI, "}")]
	if !strings.Contains(adminAPI, "proxy_set_header X-Ledger-Client-IP $remote_addr") {
		t.Error("admin API location does not send the validated client address")
	}
	if strings.Count(nginx, "proxy_set_header X-Ledger-Client-IP $remote_addr") != 3 {
		t.Errorf("X-Ledger-Client-IP forwarded %d times, want auth metadata, oauth, and admin API only", strings.Count(nginx, "proxy_set_header X-Ledger-Client-IP $remote_addr"))
	}
}

func TestFrontendContainerIsStaticNonRootAndHardened(t *testing.T) {
	dockerfile, err := os.ReadFile("frontend/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	stages := strings.Split(string(dockerfile), "FROM ")
	final := stages[len(stages)-1]
	if !strings.HasPrefix(final, "nginxinc/nginx-unprivileged:") {
		t.Errorf("final stage is not the unprivileged nginx image: %q", strings.SplitN(final, "\n", 2)[0])
	}
	for _, forbidden := range []string{"USER root", "node_modules", "npm ", "EXPOSE 80\n"} {
		if strings.Contains(final, forbidden) {
			t.Errorf("final frontend stage contains %q", forbidden)
		}
	}
	if !strings.Contains(string(dockerfile), "npm ci") || !strings.Contains(string(dockerfile), "npm run build") {
		t.Error("frontend build stage must install from the lockfile and run the production build")
	}
	conf, err := os.ReadFile("frontend/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	site := string(conf)
	for _, required := range []string{
		"listen 8085;",
		"default-src 'self'",
		"frame-ancestors 'none'",
		"X-Content-Type-Options \"nosniff\"",
		"X-Frame-Options \"DENY\"",
		"Referrer-Policy",
		"try_files $uri $uri/ /admin/index.html",
		"location / { return 404; }",
	} {
		if !strings.Contains(site, required) {
			t.Errorf("frontend nginx.conf missing %q", required)
		}
	}
	for _, forbidden := range []string{"unsafe-inline", "unsafe-eval", "http://", "https://"} {
		if strings.Contains(site, forbidden) {
			t.Errorf("frontend nginx.conf contains %q", forbidden)
		}
	}
	for path, patterns := range map[string][]string{
		".dockerignore":          {"frontend"},
		"frontend/.dockerignore": {"node_modules", "dist", ".env*"},
		".gitignore":             {"node_modules/", "/frontend/dist/"},
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := map[string]bool{}
		for _, line := range strings.Split(string(body), "\n") {
			lines[strings.TrimSpace(line)] = true
		}
		for _, pattern := range patterns {
			if !lines[pattern] {
				t.Errorf("%s missing %q", path, pattern)
			}
		}
	}
	pkg, err := os.ReadFile("frontend/package.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"cdn.", "unpkg", "jsdelivr"} {
		if strings.Contains(string(pkg), forbidden) {
			t.Errorf("frontend package.json references a CDN: %q", forbidden)
		}
	}
	if _, err := os.Stat("frontend/package-lock.json"); err != nil {
		t.Error("frontend lockfile missing")
	}
}

func TestBuildToolingCoversAdminAndFrontend(t *testing.T) {
	makefile, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"go build -o bin/ledger-admin ./cmd/ledger-admin", "frontend-verify:", "images:", "npm ci", "npm run verify"} {
		if !strings.Contains(string(makefile), required) {
			t.Errorf("Makefile missing %q", required)
		}
	}
	ci, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(ci)
	for _, required := range []string{"npm ci", "npm run lint", "npm run typecheck", "npm test", "npm run build", "npm audit --audit-level=high", "docker compose", "config", "frontend/Dockerfile"} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI missing %q", required)
		}
	}
	if !regexp.MustCompile(`actions/setup-node@[0-9a-f]{40} # v\d`).MatchString(workflow) {
		t.Error("actions/setup-node is not pinned to a commit SHA")
	}
	if strings.Contains(workflow, "@v") && regexp.MustCompile(`uses: [^\n@]+@v\d`).MatchString(workflow) {
		t.Error("an action is pinned by tag instead of commit SHA")
	}
	dependabot, err := os.ReadFile(".github/dependabot.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dependabot), "package-ecosystem: npm") || !strings.Contains(string(dependabot), "directory: /frontend") {
		t.Error("dependabot does not cover the frontend npm dependencies")
	}
	env, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "LEDGER_ADMIN_PASSWORD_HASH='<argon2id-phc-hash>'") {
		t.Error(".env.example lacks the admin password hash placeholder")
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
		"`LEDGER_ADMIN_PASSWORD_HASH`",
		"ledger-admin hash-password",
		"ledger-admin revoke-sessions",
		"`/admin/`",
		"`/admin/api/`",
		"`admin_session`",
		"LEDGER_STACK_ADMIN_PASSWORD",
		"LEDGER_TEST_DATABASE_URL",
		"ledger-frontend",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README missing %q", required)
		}
	}
	security, err := os.ReadFile("SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"admin password", "admin session", "LEDGER_ADMIN_PASSWORD_HASH"} {
		if !strings.Contains(string(security), required) {
			t.Errorf("SECURITY.md missing %q", required)
		}
	}
	for _, obsolete := range []string{"request `read`", "and/or `write`", "`client`, `code`, and `token`", "docker compose run --rm index"} {
		if strings.Contains(readme, obsolete) {
			t.Errorf("README retains obsolete contract %q", obsolete)
		}
	}
}
