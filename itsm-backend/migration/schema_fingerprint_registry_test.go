package migration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaVerifierRegistryRetainsMultipleCatalogedSourceReleases(t *testing.T) {
	const verifier = "WITH records AS (SELECT 'fixture'::text AS record) SELECT string_agg(record, '') FROM records"
	files := []VerifierAssetFile{
		fixtureSourceVerifierFile(t, "source-schema/release-a.json", "release-a", "schema-a", "baseline-a", verifier),
		fixtureSourceVerifierFile(t, "source-schema/release-b.json", "release-b", "schema-b", "baseline-b", verifier),
	}
	registry, err := NewSchemaVerifierRegistry(verifier, files)
	require.NoError(t, err)

	entries := make([]ReleaseCatalogEntry, 0, len(files))
	for index, file := range files {
		ref := ReleaseAsset{Name: file.Name, SHA256: checksumSQL(string(file.Content))}
		entry := ReleaseCatalogEntry{
			ReleaseID:             []string{"release-a", "release-b"}[index],
			SchemaVersion:         []string{"schema-a", "schema-b"}[index],
			BaselineVersion:       []string{"baseline-a", "baseline-b"}[index],
			ReleaseManifestSHA256: []string{fixtureDigestA, fixtureDigestB}[index],
			SourceSchemaAsset:     ref,
		}
		entries = append(entries, entry)
		asset, err := registry.loadSource(ref)
		require.NoError(t, err)
		require.Equal(t, entry.SchemaVersion, asset.Release.SchemaVersion)
	}
	require.NoError(t, verifyReleaseCatalogVerifierAssets(entries, registry))

	// The registry owns immutable copies; a caller cannot rewrite a retained
	// historical verifier after construction.
	files[0].Content[0] = 'x'
	_, err = registry.loadSource(entries[0].SourceSchemaAsset)
	require.NoError(t, err)
}

func TestTransitionPlanningRequiresExactCommittedChecksumPrefix(t *testing.T) {
	const verifier = "WITH records AS (SELECT 'fixture'::text AS record) SELECT string_agg(record, '') FROM records"
	sourceFile := fixtureSourceVerifierFile(t, "source-schema/release-a.json", "release-a", "schema-a", "baseline-a", verifier)
	targetFile := fixtureSourceVerifierFile(t, "source-schema/release-b.json", "release-b", "schema-b", "baseline-b", verifier)
	transitionFile := fixtureTransitionVerifierFile(t, verifier)
	registry, err := NewSchemaVerifierRegistry(verifier, []VerifierAssetFile{sourceFile, targetFile, transitionFile})
	require.NoError(t, err)

	source := ReleaseCatalogEntry{
		ReleaseID:             "release-a",
		SchemaVersion:         "schema-a",
		BaselineVersion:       "baseline-a",
		ReleaseManifestSHA256: fixtureDigestA,
		SourceSchemaAsset: ReleaseAsset{
			Name: sourceFile.Name, SHA256: checksumSQL(string(sourceFile.Content)),
		},
	}
	target := ReleaseCatalogEntry{
		ReleaseID:             "release-b",
		SchemaVersion:         "schema-b",
		BaselineVersion:       "baseline-b",
		ReleaseManifestSHA256: fixtureDigestB,
		SourceSchemaAsset: ReleaseAsset{
			Name: targetFile.Name, SHA256: checksumSQL(string(targetFile.Content)),
		},
		TransitionAssets: []ReleaseAsset{{
			Name: transitionFile.Name, SHA256: checksumSQL(string(transitionFile.Content)),
		}},
	}
	available := []CatalogedMigration{
		{Migration: Migration{Version: "fixture_1", Description: "first fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN one text"},
		{Migration: Migration{Version: "fixture_2", Description: "second fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN two text"},
	}

	plan, err := registry.planTransition(source, target, nil, available)
	require.NoError(t, err)
	require.Equal(t, fixtureDigestA, plan.ExpectedFingerprint)
	require.Equal(t, []string{"fixture_1", "fixture_2"}, catalogedMigrationVersions(plan.Pending))

	plan, err = registry.planTransition(source, target, []Migration{{
		Version: "fixture_1", Checksum: checksumSQL(available[0].SQL),
	}}, available)
	require.NoError(t, err)
	require.Equal(t, fixtureDigestC, plan.ExpectedFingerprint)
	require.Equal(t, []string{"fixture_2"}, catalogedMigrationVersions(plan.Pending))

	_, err = registry.planTransition(source, target, []Migration{{
		Version: "fixture_1", Checksum: fixtureDigestB,
	}}, available)
	require.ErrorContains(t, err, "checksum")

	_, err = registry.planTransition(source, target, []Migration{{
		Version: "fixture_2", Checksum: checksumSQL(available[1].SQL),
	}}, available)
	require.ErrorContains(t, err, "continuous prefix")

	_, err = registry.planTransition(source, target, []Migration{
		{Version: "fixture_2", Checksum: checksumSQL(available[1].SQL)},
		{Version: "fixture_1", Checksum: checksumSQL(available[0].SQL)},
	}, available)
	require.ErrorContains(t, err, "exact sequence")
}

const (
	fixtureDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fixtureDigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fixtureDigestC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func fixtureSourceVerifierFile(
	t *testing.T,
	name string,
	releaseID string,
	schemaVersion string,
	baselineVersion string,
	verifier string,
) VerifierAssetFile {
	t.Helper()
	asset := catalogFingerprintAsset{
		Kind:          "source",
		AssetID:       name,
		FormatVersion: catalogFingerprintFormatVersion,
		Release: catalogReleaseIdentity{
			ReleaseID:       releaseID,
			SchemaVersion:   schemaVersion,
			BaselineVersion: baselineVersion,
		},
		Verifier:       ReleaseAsset{Name: catalogFingerprintVerifierName, SHA256: checksumSQL(verifier)},
		Platform:       releasePlatformRequirement{PostgresMajor: 17, VectorVersion: "0.8.6"},
		ManagedSchemas: []string{"public"},
		Extensions: catalogExtensionInventory{
			Empty:     []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
			Installed: []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
		},
		Phases: catalogFingerprintPhases{
			Empty:          fixtureDigestA,
			Prepared:       fixtureDigestA,
			EntSchema:      fixtureDigestA,
			CurrentRelease: map[string]string{"release-a": fixtureDigestA, "release-b": fixtureDigestB}[releaseID],
		},
	}
	content, err := json.Marshal(asset)
	require.NoError(t, err)
	return VerifierAssetFile{Name: name, Content: content}
}

func fixtureTransitionVerifierFile(t *testing.T, verifier string) VerifierAssetFile {
	t.Helper()
	const name = "transition-schema/release-a--release-b.json"
	asset := catalogTransitionAsset{
		Kind:          "transition",
		AssetID:       name,
		FormatVersion: catalogTransitionFormatVersion,
		Source: catalogTransitionSource{
			ReleaseID:             "release-a",
			SchemaVersion:         "schema-a",
			BaselineVersion:       "baseline-a",
			ReleaseManifestSHA256: fixtureDigestA,
		},
		Target: catalogReleaseIdentity{
			ReleaseID:       "release-b",
			SchemaVersion:   "schema-b",
			BaselineVersion: "baseline-b",
		},
		Verifier:       ReleaseAsset{Name: catalogFingerprintVerifierName, SHA256: checksumSQL(verifier)},
		Platform:       releasePlatformRequirement{PostgresMajor: 17, VectorVersion: "0.8.6"},
		ManagedSchemas: []string{"public"},
		Migrations: []catalogTransitionMigration{
			{
				Version:     "fixture_1",
				SQLSHA256:   checksumSQL("ALTER TABLE probe ADD COLUMN one text"),
				Fingerprint: fixtureDigestC,
				Extensions:  []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
			},
			{
				Version:     "fixture_2",
				SQLSHA256:   checksumSQL("ALTER TABLE probe ADD COLUMN two text"),
				Fingerprint: fixtureDigestB,
				Extensions:  []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
			},
		},
	}
	content, err := json.Marshal(asset)
	require.NoError(t, err)
	return VerifierAssetFile{Name: name, Content: content}
}

func catalogedMigrationVersions(items []CatalogedMigration) []string {
	versions := make([]string, 0, len(items))
	for _, item := range items {
		versions = append(versions, item.Migration.Version)
	}
	return versions
}
