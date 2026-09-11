package migration

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func controlledReceipts(ms []Migration) []Migration {
	out := append([]Migration(nil), ms...)
	for i := range out {
		out[i].Checksum = checksumSQL(GetMigrationSQL(out[i].Version))
		if out[i].Version == WorkItemPrepareVersion || out[i].Version == WorkItemRetireVersion {
			revision, digest := ControlledCatalogRevision, "evidence-digest"
			out[i].CatalogRevision, out[i].EvidenceDigest = &revision, &digest
		}
	}
	return out
}
func controlledOrdinary(c []MigrationDefinition) []Migration {
	var out []Migration
	for _, d := range c {
		if d.Stage == StageOrdinary {
			out = append(out, d.Migration)
		}
	}
	return out
}
func controlledStage(c []MigrationDefinition, s MigrationStage) Migration {
	for _, d := range c {
		if d.Stage == s {
			return d.Migration
		}
	}
	panic("missing stage")
}
func TestControlledPlanUnknownStage(t *testing.T) {
	_, err := PlanMigrations([]MigrationDefinition{{Migration: Migration{Version: "test", Description: "test"}, Stage: MigrationStage("unregistered")}}, nil, OpUp, nil)
	require.ErrorContains(t, err, "unknown stage")
}
func TestControlledPlanOldHistoryCannotHideRetiredHoles(t *testing.T) {
	for _, gap := range []string{"022_drop_professional_extension_shared_fields", "027_work_item_identity_field_retirement"} {
		var applied []Migration
		for _, m := range RegisteredMigrations {
			if m.Version != gap {
				applied = append(applied, m)
			}
		}
		p, err := PlanMigrations(ControlledMigrationCatalog(), controlledReceipts(applied), OpPrepare, nil)
		require.ErrorContains(t, err, "continuous prefix")
		require.Empty(t, p.Executable)
	}
}
func TestControlledPlanTransition(t *testing.T) {
	c := ControlledMigrationCatalog()
	prepare, retire := controlledStage(c, StagePrepare), controlledStage(c, StageRetire)
	ordinary := controlledOrdinary(c)
	prefix := controlledReceipts(ordinary[:14])
	p, err := PlanMigrations(c, prefix, OpUp, nil)
	require.NoError(t, err)
	require.Empty(t, p.Executable)
	require.Equal(t, []Migration{prepare, retire}, p.PendingManual)
	p, err = PlanMigrations(c, prefix, OpPrepare, nil)
	require.NoError(t, err)
	require.Equal(t, []Migration{prepare}, p.Executable)
	// A genuine old complete history still needs P, without fictitious 022/027 writes.
	p, err = PlanMigrations(c, controlledReceipts(RegisteredMigrations), OpPrepare, nil)
	require.NoError(t, err)
	require.Equal(t, []Migration{prepare}, p.Executable)
	prefix = append(prefix, controlledReceipts([]Migration{prepare})...)
	p, err = PlanMigrations(c, prefix, OpUp, nil)
	require.NoError(t, err)
	require.Equal(t, ordinary[14:], p.Executable)
	require.Equal(t, []Migration{retire}, p.PendingManual)
	_, err = PlanMigrations(c, prefix, OpRetire, nil)
	require.Error(t, err)
	full := append(controlledReceipts(ordinary), controlledReceipts([]Migration{prepare})...)
	p, err = PlanMigrations(c, full, OpUp, nil)
	require.NoError(t, err)
	require.Empty(t, p.Executable)
	require.Equal(t, []Migration{retire}, p.PendingManual)
	p, err = PlanMigrations(c, full, OpRetire, nil)
	require.NoError(t, err)
	require.Equal(t, []Migration{retire}, p.Executable)
}
func TestControlledPlanRejectsInvalidReceipts(t *testing.T) {
	c := ControlledMigrationCatalog()
	base := controlledReceipts(controlledOrdinary(c)[:14])
	prep := controlledReceipts([]Migration{controlledStage(c, StagePrepare)})[0]
	for _, tc := range []struct {
		name   string
		change func([]Migration) []Migration
	}{
		{"unknown", func(m []Migration) []Migration { return append(m, Migration{Version: "unknown"}) }},
		{"duplicate", func(m []Migration) []Migration { return append(m, m[0]) }},
		{"checksum", func(m []Migration) []Migration { m[0].Checksum = "changed"; return m }},
		{"P missing prerequisite", func(m []Migration) []Migration { return append(m[:13], prep) }},
		{"new ordinary hole", func(m []Migration) []Migration {
			return append(append(m, prep), controlledReceipts(controlledOrdinary(c)[15:16])...)
		}},
		{"P no revision", func(m []Migration) []Migration { p := prep; p.CatalogRevision = nil; return append(m, p) }},
		{"P wrong revision", func(m []Migration) []Migration { p := prep; r := "wrong"; p.CatalogRevision = &r; return append(m, p) }},
		{"P no evidence", func(m []Migration) []Migration { p := prep; p.EvidenceDigest = nil; return append(m, p) }},
		{"R without P", func(m []Migration) []Migration {
			return append(m, controlledReceipts([]Migration{controlledStage(c, StageRetire)})...)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := PlanMigrations(c, tc.change(append([]Migration(nil), base...)), OpUp, nil)
			require.Error(t, e)
			require.Empty(t, p.Executable)
		})
	}
}
func TestControlledPlanRejectsCatalogAndOperationBypass(t *testing.T) {
	c := ControlledMigrationCatalog()
	for _, op := range []MigrationOperation{OpUp, OpPrepare, OpRetire, OpReset} {
		_, err := PlanMigrations(c, nil, op, []string{c[0].Migration.Version})
		require.Error(t, err)
	}
	_, err := PlanMigrations(c, nil, MigrationOperation("unknown"), nil)
	require.Error(t, err)
	for _, mutate := range []func([]MigrationDefinition) []MigrationDefinition{
		func(c []MigrationDefinition) []MigrationDefinition { return c[1:] },
		func(c []MigrationDefinition) []MigrationDefinition { c[0], c[1] = c[1], c[0]; return c },
		func(c []MigrationDefinition) []MigrationDefinition {
			c[0].Migration.RollbackSQL = "DROP TABLE tickets"
			return c
		},
		func(c []MigrationDefinition) []MigrationDefinition { c[14].Requires = nil; return c },
		func(c []MigrationDefinition) []MigrationDefinition { c[14].Stage = StageOrdinary; return c },
	} {
		_, err := PlanMigrations(mutate(ControlledMigrationCatalog()), nil, OpUp, nil)
		require.Error(t, err)
	}
}
func TestControlledPlanDownPreflightsWholeRequest(t *testing.T) {
	c := ControlledMigrationCatalog()
	ordinary := controlledOrdinary(c)
	applied := controlledReceipts(ordinary[:14])
	// Latest ordinary reversible migration can be rolled back, using canonical SQL.
	applied[13].RollbackSQL = "untrusted ledger SQL"
	p, err := PlanMigrations(c, applied, OpDown, []string{ordinary[13].Version})
	require.NoError(t, err)
	require.Equal(t, []Migration{ordinary[13]}, p.Executable)
	for _, requested := range [][]string{{ordinary[12].Version}, {ordinary[13].Version, ordinary[12].Version, ordinary[11].Version}, {ordinary[13].Version, ordinary[13].Version}, {"unknown"}, nil} {
		p, err = PlanMigrations(c, applied, OpDown, requested)
		require.Error(t, err)
		require.Empty(t, p.Executable)
	}
	applied = append(applied, controlledReceipts([]Migration{controlledStage(c, StagePrepare)})...)
	p, err = PlanMigrations(c, applied, OpDown, []string{ordinary[13].Version})
	require.Error(t, err)
	require.Empty(t, p.Executable)
	p, err = PlanMigrations(c, applied, OpReset, nil)
	require.Error(t, err)
	require.Empty(t, p.Executable)
}

func TestControlledPlanRetirementRequiresPForCompleteOldHistory(t *testing.T) {
	p, err := PlanMigrations(ControlledMigrationCatalog(), controlledReceipts(RegisteredMigrations), OpRetire, nil)
	require.Error(t, err)
	require.Empty(t, p.Executable)
}

func TestControlledPlanReversibleSuffixUsesReverseCatalogOrder(t *testing.T) {
	c := ControlledMigrationCatalog()
	ordinary := controlledOrdinary(c)
	p, err := PlanMigrations(c, controlledReceipts(ordinary[:14]), OpDown, []string{ordinary[12].Version, ordinary[13].Version})
	require.NoError(t, err)
	require.Equal(t, []Migration{ordinary[13], ordinary[12]}, p.Executable)
}

func TestControlledPlanStopsBeforePreparation(t *testing.T) {
	c := ControlledMigrationCatalog()
	p, err := PlanMigrations(c, nil, OpUp, nil)
	require.NoError(t, err)
	require.Equal(t, controlledOrdinary(c)[:14], p.Executable)
	require.Len(t, p.PendingManual, 2)
	p, err = PlanMigrations(c, nil, OpPrepare, nil)
	require.Error(t, err)
	require.Empty(t, p.Executable)
}

func TestControlledPlanOldPartialHistoryCannotContinuePastP(t *testing.T) {
	// Old 023 receipt must not permit 024 until preparation is committed.
	p, err := PlanMigrations(ControlledMigrationCatalog(), controlledReceipts(RegisteredMigrations[:16]), OpUp, nil)
	require.NoError(t, err)
	require.Empty(t, p.Executable)
	require.Len(t, p.PendingManual, 2)
}

func TestControlledPlanEveryLegalOldPrefixConvertsWithoutInventingReceipts(t *testing.T) {
	c := ControlledMigrationCatalog()
	for end := 14; end <= len(RegisteredMigrations); end++ {
		applied := controlledReceipts(RegisteredMigrations[:end])
		before := append([]Migration(nil), applied...)
		p, err := PlanMigrations(c, applied, OpPrepare, nil)
		require.NoError(t, err)
		require.Equal(t, []Migration{controlledStage(c, StagePrepare)}, p.Executable)
		require.Equal(t, before, applied)
		applied = append(applied, controlledReceipts(p.Executable)...)
		p, err = PlanMigrations(c, applied, OpUp, nil)
		require.NoError(t, err)
		for _, m := range p.Executable {
			require.NotEqual(t, "022_drop_professional_extension_shared_fields", m.Version)
			require.NotEqual(t, "027_work_item_identity_field_retirement", m.Version)
		}
		require.Equal(t, []Migration{controlledStage(c, StageRetire)}, p.PendingManual)
	}
}
