//go:build integration

package integration

import (
	"os"
	"path/filepath"
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
				"ITSM_BOOTSTRAP_MODE",
				"/docker-entrypoint-initdb.d/10-itsm-roles.sh:ro",
			} {
				require.Contains(t, text, required)
			}
			require.Contains(t, text, "DB_USER=${ITSM_MIGRATION_DB_USER")
			require.Contains(t, text, "DB_USER=${ITSM_RUNTIME_DB_USER")
			require.NotContains(t, text, "DB_USER=itsm_user")
			require.NotContains(t, text, "DB_PASSWORD=dev123")

			postgres := composeServiceBlock(t, text, "postgres")
			require.Contains(t, postgres, "POSTGRES_USER: ${ITSM_MIGRATION_DB_USER")
			init := composeServiceBlock(t, text, "itsm-init")
			require.Contains(t, init, "DB_USER=${ITSM_MIGRATION_DB_USER")
			require.Contains(t, init, "ITSM_BOOTSTRAP_MODE=${ITSM_BOOTSTRAP_MODE:-fresh}")
			api := composeServiceBlock(t, text, "itsm-backend")
			require.Contains(t, api, "DB_USER=${ITSM_RUNTIME_DB_USER")
			require.Contains(t, api, "ITSM_AUTO_MIGRATE=false")
			if composeName == "docker-compose.dev.yml" {
				worker := composeServiceBlock(t, text, "itsm-worker")
				require.Contains(t, worker, "DB_USER=${ITSM_RUNTIME_DB_USER")
			}
		})
	}

	roleScript, err := os.ReadFile(filepath.Join(repositoryRoot, "scripts", "postgres", "10-itsm-roles.sh"))
	require.NoError(t, err)
	require.Contains(t, string(roleScript), "CREATE ROLE")
	require.Contains(t, string(roleScript), "NOSUPERUSER NOBYPASSRLS")
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
}
