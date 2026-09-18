//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/service/workitemcutover"
)

// Execute the compiled CLI: go run wraps the child's exit 2 as exit 1.
func TestWorkItemCutoverCLIExitCodesAndReadOnly(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "check_workitem_cutover")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/check_workitem_cutover")
	build.Dir = root
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "CLI build failed: %s", buildOutput)
	for _, tc := range []struct {
		name   string
		legacy bool
		exit   int
	}{{"legacy_dependencies", true, 2}, {"canonical", false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCutoverFixture(t)
			if tc.legacy {
				instance := f.instance(t, "change", "change:1", 1, "running")
				f.pendingCallback(t, instance)
				f.binding(t, "change", true)
			} else {
				id := f.canonicalChange(t, "CHG-CLI")
				key, err := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, id)
				require.NoError(t, err)
				f.instance(t, dto.RecordClassChangeRequest, key, id, "running")
			}
			var schema string
			require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
			dsn := migrationEntryTarget(t)
			password, _ := dsn.User.Password()
			configDir := t.TempDir()
			// LoadConfig reads config.yaml from cwd, resolves these variables and applies
			// DB_SCHEMA explicitly. No production .env or credential file is loaded.
			configText := "database:\n  host: \"\u0024{DB_HOST}\"\n  port: \"\u0024{DB_PORT}\"\n  user: \"\u0024{DB_USER}\"\n  dbname: \"\u0024{DB_NAME}\"\n  sslmode: disable\n"
			require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configText), 0o600))
			before := f.databaseDigest(t)
			command := exec.CommandContext(f.ctx, binary, "-json", "-tenant-id="+strconv.Itoa(f.tenant.ID))
			command.Dir = configDir
			command.Env = []string{"DB_HOST=" + dsn.Hostname(), "DB_PORT=" + dsn.Port(), "DB_USER=" + dsn.User.Username(), "DB_PASSWORD=" + password, "DB_NAME=" + strings.TrimPrefix(dsn.Path, "/"), "DB_SCHEMA=" + schema, "RLS_MODE=enforce"}
			// Keep secrets exclusively in cmd.Env; redact even unexpected diagnostic output.
			output, runErr := command.CombinedOutput()
			diagnostic := string(output)
			if password != "" {
				diagnostic = strings.ReplaceAll(diagnostic, password, "[REDACTED]")
			}
			exit := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				require.True(t, errors.As(runErr, &exitErr), "CLI execution failed without exit status")
				exit = exitErr.ExitCode()
			}
			require.Equal(t, tc.exit, exit, "unexpected CLI exit: %s", diagnostic)
			// Logger output is stderr; the report remains the final JSON object on stdout.
			start := strings.Index(diagnostic, "{\n")
			require.GreaterOrEqual(t, start, 0, "CLI did not emit a JSON report")
			var report workitemcutover.Report
			require.NoError(t, json.Unmarshal([]byte(diagnostic[start:]), &report))
			require.Equal(t, !tc.legacy, report.Switchable)
			if tc.legacy {
				require.NotEmpty(t, report.Blockers)
			} else {
				require.Empty(t, report.Blockers)
			}
			require.Equal(t, before, f.databaseDigest(t), "CLI must not change dependency contents")
			t.Logf("compiled CLI exit=%d; tenant-scoped pre/post digest unchanged", exit)
		})
	}
}
