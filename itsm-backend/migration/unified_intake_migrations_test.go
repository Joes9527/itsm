package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnifiedIntakeMigrationsRegisteredInOrder guards the migration numbering
// contract for the unified intake reconciliation: 023_unified_intake_rls and
// 025_external_identity_version must both be registered, and ordered after
// 020_work_item_number_allocator with 023 preceding 025. The 024 slot is
// deliberately left open here for Task 14 to fill in later (it retires
// service_catalogs.itsm_type and must land in the same commit as the code
// that stops reading that column) — do not duplicate a second version of
// this test there; Task 14 extends this same test with 024's position.
func TestUnifiedIntakeMigrationsRegisteredInOrder(t *testing.T) {
	migrations := PostSchemaMigrations()
	var versions []string
	for _, m := range migrations {
		versions = append(versions, m.Version)
	}
	require.Contains(t, versions, "023_unified_intake_rls")
	require.Contains(t, versions, "025_external_identity_version")
	idx020 := indexOf(versions, "020_work_item_number_allocator")
	idx023 := indexOf(versions, "023_unified_intake_rls")
	idx025 := indexOf(versions, "025_external_identity_version")
	require.True(t, idx020 < idx023 && idx023 < idx025)
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
