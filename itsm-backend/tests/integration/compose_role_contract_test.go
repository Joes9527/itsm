//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalComposeSeparatesMigrationAndRuntimeDatabasePrincipals(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))

	for _, composeName := range []string{"docker-compose.yml", "docker-compose.dev.yml"} {
		t.Run(composeName, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(repositoryRoot, composeName))
			require.NoError(t, err)
			text := string(content)
			for _, required := range []string{
				"ITSM_MIGRATION_DB_USER",
				"ITSM_RUNTIME_DB_USER",
				"ITSM_BOOTSTRAP_DB_USER",
				"ITSM_BOOTSTRAP_MODE",
				"/docker-entrypoint-initdb.d/10-itsm-roles.sh:ro",
				"itsm-role-provision",
			} {
				require.True(t, strings.Contains(text, required), "Compose contract is missing %s", required)
			}
			require.True(t, strings.Contains(text, "DB_USER=${ITSM_MIGRATION_DB_USER"))
			require.True(t, strings.Contains(text, "DB_USER=${ITSM_RUNTIME_DB_USER"))
			require.False(t, strings.Contains(text, "DB_USER=itsm_user"))
			require.False(t, strings.Contains(text, "DB_PASSWORD=dev123"))

			postgres := composeServiceBlock(t, text, "postgres")
			require.Contains(t, postgres, "POSTGRES_USER: ${ITSM_MIGRATION_DB_USER")
			init := composeServiceBlock(t, text, "itsm-init")
			require.Contains(t, init, "DB_USER=${ITSM_MIGRATION_DB_USER")
			require.Contains(t, init, "ITSM_BOOTSTRAP_MODE=${ITSM_BOOTSTRAP_MODE:-upgrade}")
			api := composeServiceBlock(t, text, "itsm-backend")
			require.Contains(t, api, "DB_USER=${ITSM_RUNTIME_DB_USER")
			require.Contains(t, api, "ITSM_AUTO_MIGRATE=false")
			if composeName == "docker-compose.dev.yml" {
				worker := composeServiceBlock(t, text, "itsm-worker")
				require.Contains(t, worker, "DB_USER=${ITSM_RUNTIME_DB_USER")
			}
			requireComposeRenders(t, repositoryRoot, composeName)
		})
	}

	roleScript, err := os.ReadFile(filepath.Join(repositoryRoot, "scripts", "postgres", "10-itsm-roles.sh"))
	require.NoError(t, err)
	require.Contains(t, string(roleScript), "CREATE ROLE")
	require.Contains(t, string(roleScript), "NOSUPERUSER NOBYPASSRLS")
	require.Contains(t, string(roleScript), "ITSM_BOOTSTRAP_DB_USER")

	t.Run("mixed-case role fails before database access without disclosure", func(t *testing.T) {
		command := exec.Command("sh", filepath.Join(repositoryRoot, "scripts", "postgres", "10-itsm-roles.sh"))
		command.Env = append(os.Environ(),
			"ITSM_MIGRATION_DB_USER=MixedCaseSensitiveRole",
			"ITSM_MIGRATION_DB_PASSWORD=test-only-migration",
			"ITSM_RUNTIME_DB_USER=runtime_role",
			"ITSM_RUNTIME_DB_PASSWORD=test-only-runtime",
			"ITSM_BOOTSTRAP_DB_USER=bootstrap_role",
			"POSTGRES_USER=bootstrap_role",
			"POSTGRES_DB=itsm",
		)
		output, err := command.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(output), "invalid migration database role")
		require.NotContains(t, string(output), "MixedCaseSensitiveRole")
	})
}

func requireComposeRenders(t *testing.T, repositoryRoot, composeName string) {
	t.Helper()
	command := exec.Command("docker", "compose", "-f", filepath.Join(repositoryRoot, composeName), "config", "--format", "json")
	command.Env = append(os.Environ(),
		"ITSM_MIGRATION_DB_PASSWORD=test-only-migration",
		"ITSM_RUNTIME_DB_PASSWORD=test-only-runtime",
		"ITSM_BOOTSTRAP_DB_PASSWORD=test-only-bootstrap",
		"KAF_WEBHOOK_URL=http://worker.invalid",
		"KAF_WEBHOOK_SECRET=test-only-worker",
		"LLM_API_KEY=",
	)
	if _, err := command.Output(); err != nil {
		t.Fatal("documented Compose startup workflow does not render")
	}
}

func TestTrackedExamplesContainNoCredentialShapedLLMDefaultsAndDocumentSteadyUpgrade(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	credentialShape := regexp.MustCompile(`(^|[^A-Za-z0-9])sk-[A-Za-z0-9_-]{20,}`)
	for _, name := range []string{"docker-compose.dev.yml", ".env.example", ".env.dev.example"} {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, name))
		require.NoError(t, err)
		require.False(t, credentialShape.Match(content), "%s contains a credential-shaped LLM default", name)
	}

	for _, name := range []string{".env.example", ".env.dev.example"} {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, name))
		require.NoError(t, err)
		text := string(content)
		for _, required := range []string{
			"ITSM_MIGRATION_DB_USER=itsm_migration",
			"ITSM_MIGRATION_DB_PASSWORD=",
			"ITSM_RUNTIME_DB_USER=itsm_runtime",
			"ITSM_RUNTIME_DB_PASSWORD=",
			"ITSM_BOOTSTRAP_DB_USER=itsm_migration",
			"ITSM_BOOTSTRAP_MODE=upgrade",
		} {
			require.Contains(t, text, required)
		}
		require.NotContains(t, text, "DB_PASSWORD=dev123")
	}

	readme, err := os.ReadFile(filepath.Join(repositoryRoot, "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(readme), "ITSM_BOOTSTRAP_MODE=fresh docker compose run --rm itsm-init")
	require.Contains(t, string(readme), "docker compose --profile bootstrap run --rm itsm-role-provision")
}

func composeServiceBlock(t *testing.T, content, service string) string {
	t.Helper()
	marker := "  " + service + ":"
	lines := strings.Split(content, "\n")
	start := -1
	for index, line := range lines {
		if line == marker {
			start = index
			break
		}
	}
	require.NotEqual(t, -1, start, "compose service %s must exist", service)
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func TestReleasePlatformDocumentationPinsMajorAndExtensionUpgradeBoundary(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	for _, name := range []string{
		"README.md",
		"docs/database.md",
		"docs/runbooks/production-initialization.md",
		"itsm-backend/README.md",
	} {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, name))
		require.NoError(t, err)
		text := string(content)
		require.Contains(t, text, "PostgreSQL 17")
		require.Contains(t, text, "pgvector 0.8.6")
		require.Contains(t, text, "pg_dump")
		require.Contains(t, text, "pg_upgrade")
	}
	runbook, err := os.ReadFile(filepath.Join(repositoryRoot, "docs", "pg-upgrade-runbook.md"))
	require.NoError(t, err)
	for _, required := range []string{
		"pg_extension_update_paths('vector')",
		"ALTER EXTENSION vector UPDATE TO '0.8.6'",
		"indisvalid",
		"vector_rows",
		"Do not attempt an in-place pgvector downgrade",
	} {
		require.Contains(t, string(runbook), required)
	}
}
