package migration

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublishedLineageHasOneChecksumPerVersion(t *testing.T) {
	manifest, err := LoadLineageManifest()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, entry := range manifest.Entries {
		require.False(t, seen[entry.LedgerVersion])
		require.Regexp(t, `^[0-9a-f]{64}$`, entry.SQLSHA256)
		seen[entry.LedgerVersion] = true
	}
}

func TestHistoricalContact015IsValidationOnly(t *testing.T) {
	entry, ok := PublishedLineage("015_add_service_request_contact_fields")
	require.True(t, ok)
	require.False(t, entry.Executable)
	require.Equal(t, "016_add_service_request_contact_fields", entry.ForwardMigration)
}

func TestLineageManifestRejectsIncompleteDuplicateAndMalformedEntries(t *testing.T) {
	const validEntry = `{
		"ledgerVersion":"007_add_change_execution_tables",
		"logicalFile":"itsm-backend/migration/migrations.go",
		"gitCommit":"a5370db83d89d22ac8b9a2b75e6d7afcb5b40d7b",
		"gitBlob":"d30270e1425a57d5182366115b6dc74282524c9f",
		"sqlSha256":"1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a",
		"catalog":"immutable_upgrade_lineage",
		"executable":true,
		"forwardMigration":"023_reconcile_change_execution_tenants"
	}`

	tests := map[string]string{
		"missing field":              `{"entries":[{}]}`,
		"duplicate version":          fmt.Sprintf(`{"entries":[%s,%s]}`, validEntry, validEntry),
		"malformed git commit":       `{"entries":[{"ledgerVersion":"007","logicalFile":"migrations.go","gitCommit":"wrong","gitBlob":"d30270e1425a57d5182366115b6dc74282524c9f","sqlSha256":"1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a","catalog":"immutable_upgrade_lineage","executable":true,"forwardMigration":""}]}`,
		"malformed git blob":         `{"entries":[{"ledgerVersion":"007","logicalFile":"migrations.go","gitCommit":"a5370db83d89d22ac8b9a2b75e6d7afcb5b40d7b","gitBlob":"wrong","sqlSha256":"1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a","catalog":"immutable_upgrade_lineage","executable":true,"forwardMigration":""}]}`,
		"malformed SQL checksum":     `{"entries":[{"ledgerVersion":"007","logicalFile":"migrations.go","gitCommit":"a5370db83d89d22ac8b9a2b75e6d7afcb5b40d7b","gitBlob":"d30270e1425a57d5182366115b6dc74282524c9f","sqlSha256":"wrong","catalog":"immutable_upgrade_lineage","executable":true,"forwardMigration":""}]}`,
		"executable validation-only": `{"entries":[{"ledgerVersion":"015_old","logicalFile":"migrations.go","gitCommit":"a5370db83d89d22ac8b9a2b75e6d7afcb5b40d7b","gitBlob":"d30270e1425a57d5182366115b6dc74282524c9f","sqlSha256":"1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a","catalog":"historical_validation_only","executable":true,"forwardMigration":"016_new"}]}`,
	}

	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseLineageManifest([]byte(manifest))
			require.Error(t, err)
		})
	}
}

func TestValidateLedgerLineageAcceptsOnlyPublishedChecksum(t *testing.T) {
	const publishedChecksum = "1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a"
	const alternateChecksum = "d6ac16b327fd49b417da1b3fc5fff118469aaf15b8e00b95d23ea5022e2117dd"

	require.NoError(t, ValidateLedgerLineage([]Migration{{
		Version:  "007_add_change_execution_tables",
		Checksum: publishedChecksum,
	}}))
	require.ErrorContains(t, ValidateLedgerLineage([]Migration{{
		Version:  "007_add_change_execution_tables",
		Checksum: alternateChecksum,
	}}), "checksum mismatch")
}

func TestValidateLedgerLineageAcceptsHistoricalValidationOnlyVersion(t *testing.T) {
	entry, ok := PublishedLineage("015_add_service_request_contact_fields")
	require.True(t, ok)

	require.NoError(t, ValidateLedgerLineage([]Migration{{
		Version:  entry.LedgerVersion,
		Checksum: entry.SQLSHA256,
	}}))
}
