package migration

import (
	"testing"

	entmigrate "itsm-backend/ent/migrate"
	"itsm-backend/pkg/seeder"

	"github.com/stretchr/testify/require"
)

func TestReleaseManifestChecksumIsStable(t *testing.T) {
	first, err := CurrentRelease().Checksum()
	require.NoError(t, err)
	second, err := CurrentRelease().Checksum()
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Regexp(t, `^[0-9a-f]{64}$`, first)
}

func TestReleaseManifestChecksumCanonicalizesAssetAndComponentOrder(t *testing.T) {
	first := ReleaseManifest{
		ReleaseID:            "release-a",
		SchemaVersion:        "028_schema_release_state",
		BaselineVersion:      "baseline-a",
		EntSchemaFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Assets: []ReleaseAsset{
			{Name: "z.sql", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			{Name: "a.sql", SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		},
		SeedComponents: []SeedComponent{
			{Name: "z-seed", Version: "2"},
			{Name: "a-seed", Version: "1"},
		},
	}
	second := first
	second.Assets = []ReleaseAsset{first.Assets[1], first.Assets[0]}
	second.SeedComponents = []SeedComponent{first.SeedComponents[1], first.SeedComponents[0]}

	firstChecksum, err := first.Checksum()
	require.NoError(t, err)
	secondChecksum, err := second.Checksum()
	require.NoError(t, err)
	require.Equal(t, firstChecksum, secondChecksum)
}

func TestCurrentReleaseUsesCompiledEntSchemaAndSeederExports(t *testing.T) {
	release := CurrentRelease()
	require.Equal(t, "028_schema_release_state", release.SchemaVersion)
	require.NotEmpty(t, release.ReleaseID)
	require.NotEmpty(t, release.BaselineVersion)
	require.Regexp(t, `^[0-9a-f]{64}$`, release.EntSchemaFingerprint)
	require.Greater(t, len(release.Assets), len(RegisteredMigrations), "fresh baseline is a release asset independent of upgrade lineage")

	require.Len(t, release.SeedComponents, len(seeder.ProductionComponentNames))
	actual := make(map[string]string, len(release.SeedComponents))
	for _, component := range release.SeedComponents {
		actual[component.Name] = component.Version
	}
	for _, name := range seeder.ProductionComponentNames {
		require.Equal(t, seeder.CurrentTenantTemplateVersion, actual[name])
	}
}

func TestCurrentReleaseFingerprintSurvivesEntPlannerDescriptorMutation(t *testing.T) {
	before := CurrentRelease().EntSchemaFingerprint
	column := entmigrate.Tables[0].Columns[0]
	originalAttr := column.Attr
	column.Attr = originalAttr + " planner-mutated"
	t.Cleanup(func() { column.Attr = originalAttr })

	require.Equal(t, before, CurrentRelease().EntSchemaFingerprint)
}

func TestReleaseManifestChecksumRejectsIncompleteOrDuplicateEntries(t *testing.T) {
	valid := func() ReleaseManifest {
		return ReleaseManifest{
			ReleaseID:            "release-a",
			SchemaVersion:        "028_schema_release_state",
			BaselineVersion:      "baseline-a",
			EntSchemaFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Assets:               []ReleaseAsset{{Name: "asset-a", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
			SeedComponents:       []SeedComponent{{Name: "seed-a", Version: "1"}},
		}
	}

	tests := map[string]ReleaseManifest{
		"missing assets":      func() ReleaseManifest { value := valid(); value.Assets = nil; return value }(),
		"missing components":  func() ReleaseManifest { value := valid(); value.SeedComponents = nil; return value }(),
		"missing release":     func() ReleaseManifest { value := valid(); value.ReleaseID = ""; return value }(),
		"missing schema":      func() ReleaseManifest { value := valid(); value.SchemaVersion = ""; return value }(),
		"missing baseline":    func() ReleaseManifest { value := valid(); value.BaselineVersion = ""; return value }(),
		"bad Ent fingerprint": func() ReleaseManifest { value := valid(); value.EntSchemaFingerprint = "bad"; return value }(),
		"bad asset checksum":  func() ReleaseManifest { value := valid(); value.Assets[0].SHA256 = "bad"; return value }(),
		"duplicate asset": func() ReleaseManifest {
			value := valid()
			value.Assets = append(value.Assets, value.Assets[0])
			return value
		}(),
		"duplicate seed": func() ReleaseManifest {
			value := valid()
			value.SeedComponents = append(value.SeedComponents, value.SeedComponents[0])
			return value
		}(),
		"missing seed version":   func() ReleaseManifest { value := valid(); value.SeedComponents[0].Version = ""; return value }(),
		"missing asset identity": func() ReleaseManifest { value := valid(); value.Assets[0].Name = ""; return value }(),
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := manifest.Checksum()
			require.Error(t, err)
		})
	}
}
