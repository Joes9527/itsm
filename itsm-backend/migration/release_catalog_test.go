package migration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseReleaseCatalogRetainsMultipleEntriesAndExactTransitionRefs(t *testing.T) {
	first, err := CurrentReleaseCatalogEntry()
	require.NoError(t, err)
	second := first
	second.ReleaseID = "fixture-release-v2"
	second.SchemaVersion = "fixture-schema-v2"
	second.BaselineVersion = "fixture-baseline-v2"
	second.ReleaseManifestSHA256 = strings.Repeat("b", 64)
	second.SourceSchemaAsset = ReleaseAsset{Name: "source-schema/fixture-v2.json", SHA256: strings.Repeat("c", 64)}
	second.TransitionAssets = []ReleaseAsset{{Name: "transition-schema/028--fixture-v2.json", SHA256: strings.Repeat("d", 64)}}
	payload, err := json.Marshal(releaseCatalog{Entries: []ReleaseCatalogEntry{first, second}})
	require.NoError(t, err)

	parsed, err := parseReleaseCatalog(payload)
	require.NoError(t, err)
	require.Len(t, parsed.Entries, 2)
	require.Equal(t, second.TransitionAssets, parsed.Entries[1].TransitionAssets)

	second.TransitionAssets = append(second.TransitionAssets, second.TransitionAssets[0])
	payload, err = json.Marshal(releaseCatalog{Entries: []ReleaseCatalogEntry{first, second}})
	require.NoError(t, err)
	_, err = parseReleaseCatalog(payload)
	require.ErrorContains(t, err, "duplicate transition")
}

func TestCurrentReleaseCatalogPinsManifestBaselineAndExplicitCoverage(t *testing.T) {
	entry, err := CurrentReleaseCatalogEntry()
	require.NoError(t, err)

	require.Equal(t, "itsm-v1.1", entry.ReleaseID)
	require.Equal(t, "028_schema_release_state", entry.SchemaVersion)
	require.Equal(t, "2026-09-04", entry.BaselineVersion)
	require.Equal(t, "5decb8eeb0ddfabf00ad46fde9d8e6021dafa7f07dd660bdad22b105e8fadeee", entry.ReleaseManifestSHA256)
	require.Equal(t, ReleaseAsset{
		Name:   CurrentBaselineAssetName,
		SHA256: "28ebe2d2096542d556eb94fcb42806235ebae1a33d7b6b0db5844890643d6773",
	}, entry.BaselineAsset)
	require.Equal(t, ReleaseAsset{
		Name:   CurrentSourceSchemaAssetName,
		SHA256: "93a8d7dffa303f16394f9e4b67f9b616644ab5777ace8a3c69cf1ba1ce846143",
	}, entry.SourceSchemaAsset)
	require.Equal(t, []string{
		"007_add_change_execution_tables",
		"008_add_initialization_ledger",
		"009_enable_rls_tenant_isolation",
		"011_add_tool_invocation_tenant_id",
		"012_drop_service_catalog_item",
		"013_service_request_delegates_to_ticket",
		"014_drop_legacy_approval_workflow",
		"015_process_instance_running_unique_guard",
		"016_add_service_request_contact_fields",
		"017_drop_ticket_type_legacy_approval_fields",
		"018_convert_legacy_serial_ids_to_identity",
		"019_kaf_execution_integrity_rls",
		"020_work_item_number_allocator",
		"021_add_callback_optional_declared",
		"022_drop_professional_extension_shared_fields",
		"026_reconcile_change_execution_tenants",
		"027_reconcile_current_rls_policies",
		"028_schema_release_state",
	}, entry.CoveredMigrations)
	require.NotContains(t, entry.CoveredMigrations, "023_unmerged")
	require.NotContains(t, entry.CoveredMigrations, "024_unmerged")
	require.NotContains(t, entry.CoveredMigrations, "025_unmerged")
}

func TestCatalogEntryForSchemaStateRequiresExactMinimumSupportedRelease(t *testing.T) {
	entry, err := CurrentReleaseCatalogEntry()
	require.NoError(t, err)
	state := SchemaState{
		ID:                      1,
		ReleaseID:               entry.ReleaseID,
		SchemaVersion:           entry.SchemaVersion,
		BaselineVersion:         entry.BaselineVersion,
		ReleaseManifestChecksum: entry.ReleaseManifestSHA256,
		UpdatedAt:               time.Now(),
	}
	resolved, err := CatalogEntryForSchemaState(state)
	require.NoError(t, err)
	require.Equal(t, entry, resolved)

	for name, mutate := range map[string]func(*SchemaState){
		"release":  func(value *SchemaState) { value.ReleaseID = "legacy" },
		"schema":   func(value *SchemaState) { value.SchemaVersion = "027_reconcile_current_rls_policies" },
		"baseline": func(value *SchemaState) { value.BaselineVersion = "legacy" },
		"manifest": func(value *SchemaState) { value.ReleaseManifestChecksum = strings.Repeat("a", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := state
			mutate(&invalid)
			_, err := CatalogEntryForSchemaState(invalid)
			require.ErrorContains(t, err, "unsupported upgrade source")
			require.NotContains(t, err.Error(), invalid.ReleaseManifestChecksum)
		})
	}
}

func TestCurrentReleaseArtifactValidationUsesPinnedCatalogIdentity(t *testing.T) {
	require.NoError(t, ValidateCurrentReleaseArtifact(CurrentRelease()))

	tampered := CurrentRelease()
	tampered.Assets[0].SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	err := ValidateCurrentReleaseArtifact(tampered)
	require.ErrorContains(t, err, "immutable release catalog")
}

func TestCurrentReleaseArtifactValidationRejectsSelfConsistentBaselineTampering(t *testing.T) {
	original := currentBaselineSQL
	t.Cleanup(func() { currentBaselineSQL = original })
	currentBaselineSQL += "\n-- locally regenerated but not cataloged\n"

	regenerated := CurrentRelease()
	err := ValidateCurrentReleaseArtifact(regenerated)
	require.ErrorContains(t, err, "baseline identity mismatch")
}

func TestCurrentReleaseArtifactValidationRejectsSelfConsistentSourceVerifierTampering(t *testing.T) {
	content, err := embeddedSchemaVerifierAssets.ReadFile("sql/source/028_schema_release_state.json")
	require.NoError(t, err)
	content = append(content, []byte("\n ")...)
	registry, err := NewSchemaVerifierRegistry(
		[]VerifierAssetFile{{Name: catalogFingerprintVerifierName, Content: []byte(postgresCatalogFingerprintSQL)}},
		[]VerifierAssetFile{{Name: CurrentSourceSchemaAssetName, Content: content}},
	)
	require.NoError(t, err)
	entry, err := CurrentReleaseCatalogEntry()
	require.NoError(t, err)
	require.ErrorContains(t, verifyReleaseCatalogVerifierAssets([]ReleaseCatalogEntry{entry}, registry), "identity mismatch")
}

func TestCurrentReleaseArtifactValidationRejectsCatalogQueryTampering(t *testing.T) {
	content, err := embeddedSchemaVerifierAssets.ReadFile("sql/source/028_schema_release_state.json")
	require.NoError(t, err)
	_, err = NewSchemaVerifierRegistry(
		[]VerifierAssetFile{{
			Name:    catalogFingerprintVerifierName,
			Content: []byte(postgresCatalogFingerprintSQL + "\n-- unpinned verifier change"),
		}},
		[]VerifierAssetFile{{Name: CurrentSourceSchemaAssetName, Content: content}},
	)
	require.ErrorContains(t, err, "query identity mismatch")
}

func TestReleasePublicationGateNamesUnmergedAllocatedVersions(t *testing.T) {
	err := ValidateCurrentReleasePublicationGate()
	require.Error(t, err)
	require.ErrorContains(t, err, "023")
	require.ErrorContains(t, err, "024")
	require.ErrorContains(t, err, "025")
	require.NotContains(t, err.Error(), "029")
	require.NotContains(t, err.Error(), "030")
}

func TestPlanMigrationsByExplicitCoverageNeverInfersCoverageFromHead(t *testing.T) {
	available := []Migration{
		{Version: "007_first"},
		{Version: "023_late_published_lower_number"},
		{Version: "028_baseline_head"},
		{Version: "029_next_release"},
	}
	covered := []string{"007_first", "028_baseline_head"}

	pending, err := PlanMigrationsByExplicitCoverage(available, nil, covered)
	require.NoError(t, err)
	require.Equal(t, []Migration{available[1], available[3]}, pending)

	pending, err = PlanMigrationsByExplicitCoverage(available, []string{"023_late_published_lower_number"}, covered)
	require.NoError(t, err)
	require.Equal(t, []Migration{available[3]}, pending)
}

func TestPlanMigrationsByExplicitCoverageRejectsAmbiguousSets(t *testing.T) {
	available := []Migration{{Version: "007_first"}, {Version: "028_head"}}

	_, err := PlanMigrationsByExplicitCoverage(available, nil, []string{"007_first", "007_first"})
	require.ErrorContains(t, err, "duplicate covered")

	_, err = PlanMigrationsByExplicitCoverage(available, []string{"unknown"}, []string{"007_first"})
	require.ErrorContains(t, err, "unknown applied")

	_, err = PlanMigrationsByExplicitCoverage(available, nil, []string{"unknown"})
	require.ErrorContains(t, err, "unknown covered")
}
