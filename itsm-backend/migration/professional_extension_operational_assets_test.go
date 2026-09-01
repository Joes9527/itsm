package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfessionalExtensionOperationalAssetsUseWorkItemAuthority(t *testing.T) {
	for _, testCase := range []struct {
		path      string
		forbidden []string
	}{
		{
			path: filepath.Join("..", "database", "rls", "migrations", "002_pilot_policies.sql"),
			forbidden: []string{
				"CREATE POLICY tenant_isolation ON changes\n    USING       (tenant_id =",
			},
		},
		{
			path: filepath.Join("..", "database", "rls", "migrations", "rls_r1_e2e.sql"),
			forbidden: []string{
				"INSERT INTO changes (work_item_id, type, risk_level, tenant_id",
			},
		},
		{
			path: filepath.Join("..", "migrations", "add_missing_indexes.sql"),
			forbidden: []string{
				"incidents (tenant_id", "incidents (assignee_id", "incidents (reporter_id",
				"incidents (created_at", "problems (tenant_id", "problems (assignee_id",
				"problems (created_at", "changes (tenant_id", "changes (assignee_id",
			},
		},
	} {
		t.Run(filepath.Base(testCase.path), func(t *testing.T) {
			contents, err := os.ReadFile(testCase.path)
			require.NoError(t, err)
			for _, obsolete := range testCase.forbidden {
				require.NotContains(t, string(contents), obsolete)
			}
		})
	}
}

func TestProfessionalExtensionOperationalAssetsKeepTenantAndSoftDeleteScope(t *testing.T) {
	policySQL, err := os.ReadFile(filepath.Join("..", "database", "rls", "migrations", "002_pilot_policies.sql"))
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(policySQL), "work_item.deleted_at IS NULL"))
	require.Contains(t, string(policySQL), "CREATE POLICY tenant_isolation_changes ON changes")
	require.NotContains(t, string(policySQL), "CREATE POLICY tenant_isolation ON changes")

	for _, path := range []string{
		filepath.Join("..", "sql", "seed_test_data.sql"),
		filepath.Join("..", "sql", "extend_test_data.sql"),
	} {
		contents, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.NotRegexp(t, `WHERE NOT EXISTS \(SELECT 1 FROM tickets WHERE ticket_number`, string(contents))
	}
}
