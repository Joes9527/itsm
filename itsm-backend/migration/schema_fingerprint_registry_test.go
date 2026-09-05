package migration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaVerifierRegistryRetainsMultipleCatalogedSourceReleases(t *testing.T) {
	const (
		verifierV1 = "WITH records AS (SELECT 'fixture-v1'::text AS record) SELECT string_agg(record, '') FROM records"
		verifierV2 = "WITH records AS (SELECT 'fixture-v2'::text AS record) SELECT string_agg(record, '') FROM records"
	)
	verifiers := []VerifierAssetFile{
		{Name: "catalog-verifier/postgres-v1.sql", Content: []byte(verifierV1)},
		{Name: "catalog-verifier/postgres-v2.sql", Content: []byte(verifierV2)},
	}
	files := []VerifierAssetFile{
		fixtureSourceVerifierFile(t, "source-schema/release-a.json", "release-a", "schema-a", "baseline-a", verifiers[0]),
		fixtureSourceVerifierFile(t, "source-schema/release-b.json", "release-b", "schema-b", "baseline-b", verifiers[1]),
	}
	registry, err := NewSchemaVerifierRegistry(verifiers[:1], files[:1])
	require.NoError(t, err)
	registry, err = registry.WithVerifierAssets(verifiers[1:], files[1:])
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

	query, err := registry.loadVerifier(ReleaseAsset{
		Name: verifiers[0].Name, SHA256: checksumSQL(verifierV1),
	})
	require.NoError(t, err)
	require.Equal(t, verifierV1, query)
	verifiers[0].Content[0] = 'x'
	query, err = registry.loadVerifier(ReleaseAsset{
		Name: "catalog-verifier/postgres-v1.sql", SHA256: checksumSQL(verifierV1),
	})
	require.NoError(t, err)
	require.Equal(t, verifierV1, query, "publishing verifier v2 must not replace verifier v1")
}

func TestPublishingNextVerifierRetainsCurrent028SourceIdentity(t *testing.T) {
	registry, err := loadEmbeddedSchemaVerifierRegistry()
	require.NoError(t, err)
	currentRef := currentSourceSchemaAsset()
	currentSource, err := registry.loadSource(currentRef)
	require.NoError(t, err)
	currentQuery, err := registry.loadVerifier(currentSource.Verifier)
	require.NoError(t, err)

	nextVerifier := VerifierAssetFile{
		Name:    "catalog-verifier/postgres-v3.sql",
		Content: []byte(postgresCatalogFingerprintSQL + "\n-- immutable next-release verifier"),
	}
	nextSource := fixtureSourceVerifierFile(
		t,
		"source-schema/release-b.json",
		"release-b",
		"schema-b",
		"baseline-b",
		nextVerifier,
	)
	registry, err = registry.WithVerifierAssets(
		[]VerifierAssetFile{nextVerifier},
		[]VerifierAssetFile{nextSource},
	)
	require.NoError(t, err)

	retained028, err := registry.loadSource(currentRef)
	require.NoError(t, err)
	require.Equal(t, currentSource, retained028)
	retainedQuery, err := registry.loadVerifier(currentSource.Verifier)
	require.NoError(t, err)
	require.Equal(t, currentQuery, retainedQuery)
	_, err = registry.loadVerifier(ReleaseAsset{
		Name: "catalog-verifier/postgres-v1.sql", SHA256: checksumSQL(mustEmbeddedVerifier(t, "sql/catalog/postgres-v1.sql")),
	})
	require.NoError(t, err, "publishing verifier v2 for 028 must retain verifier v1")
	_, err = registry.loadSource(ReleaseAsset{
		Name: nextSource.Name, SHA256: checksumSQL(string(nextSource.Content)),
	})
	require.NoError(t, err)
}

func mustEmbeddedVerifier(t *testing.T, name string) string {
	t.Helper()
	content, err := embeddedSchemaVerifierAssets.ReadFile(name)
	require.NoError(t, err)
	return string(content)
}

func TestTransitionPlanningRequiresExactCommittedChecksumPrefix(t *testing.T) {
	const (
		verifierV1 = "WITH records AS (SELECT 'fixture-v1'::text AS record) SELECT string_agg(record, '') FROM records"
		verifierV2 = "WITH records AS (SELECT 'fixture-v2'::text AS record) SELECT string_agg(record, '') FROM records"
	)
	verifierFiles := []VerifierAssetFile{
		{Name: "catalog-verifier/postgres-v1.sql", Content: []byte(verifierV1)},
		{Name: "catalog-verifier/postgres-v2.sql", Content: []byte(verifierV2)},
	}
	sourceFile := fixtureSourceVerifierFile(t, "source-schema/release-a.json", "release-a", "schema-a", "baseline-a", verifierFiles[0])
	targetFile := fixtureSourceVerifierFile(t, "source-schema/release-b.json", "release-b", "schema-b", "baseline-b", verifierFiles[1])
	transitionFile := fixtureTransitionVerifierFile(t, verifierFiles[1], []string{"fixture_1", "fixture_2"})
	registry, err := NewSchemaVerifierRegistry(verifierFiles, []VerifierAssetFile{sourceFile, targetFile, transitionFile})
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
		CoveredMigrations: []string{"fixture_1", "fixture_2"},
	}
	available := []CatalogedMigration{
		{Migration: Migration{Version: "fixture_1", Description: "first fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN one text"},
		{Migration: Migration{Version: "fixture_2", Description: "second fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN two text"},
	}

	plan, err := registry.planTransition(source, target, nil, available)
	require.NoError(t, err)
	require.Equal(t, fixtureDigestA, plan.ExpectedFingerprint)
	require.Equal(t, ReleaseAsset{Name: verifierFiles[0].Name, SHA256: checksumSQL(verifierV1)}, plan.ExpectedVerifier)
	require.Equal(t, []string{"fixture_1", "fixture_2"}, catalogedMigrationVersions(plan.Pending))

	plan, err = registry.planTransition(source, target, []Migration{{
		Version: "fixture_1", Checksum: checksumSQL(available[0].SQL),
	}}, available)
	require.NoError(t, err)
	require.Equal(t, fixtureDigestC, plan.ExpectedFingerprint)
	require.Equal(t, ReleaseAsset{Name: verifierFiles[1].Name, SHA256: checksumSQL(verifierV2)}, plan.ExpectedVerifier)
	require.Equal(t, []string{"fixture_2"}, catalogedMigrationVersions(plan.Pending))

	_, err = registry.planTransition(source, target, []Migration{{
		Version: "fixture_1", Checksum: fixtureDigestB,
	}}, available)
	require.ErrorContains(t, err, "checksum")

	_, err = registry.planTransition(source, target, []Migration{{
		Version: "fixture_2", Checksum: checksumSQL(available[1].SQL),
	}}, available)
	require.ErrorContains(t, err, "continuous prefix")

	plan, err = registry.planTransition(source, target, []Migration{
		{Version: "fixture_2", Checksum: checksumSQL(available[1].SQL)},
		{Version: "fixture_1", Checksum: checksumSQL(available[0].SQL)},
	}, available)
	require.NoError(t, err, "ledger row timestamps/order are not transition authority")
	require.Empty(t, plan.Pending)
}

func TestReleaseCatalogRequiresExactOrderedTransitionCoverageDelta(t *testing.T) {
	const verifier = "WITH records AS (SELECT 'fixture'::text AS record) SELECT string_agg(record, '') FROM records"
	verifierFile := VerifierAssetFile{Name: catalogFingerprintVerifierName, Content: []byte(verifier)}
	sourceFile := fixtureSourceVerifierFile(t, "source-schema/release-a.json", "release-a", "schema-a", "baseline-a", verifierFile)
	targetFile := fixtureSourceVerifierFile(t, "source-schema/release-b.json", "release-b", "schema-b", "baseline-b", verifierFile)
	migrations := []CatalogedMigration{
		{Migration: Migration{Version: "base", Description: "baseline"}, SHA256: fixtureDigestA},
		{Migration: Migration{Version: "fixture_1", Description: "first fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN one text"},
		{Migration: Migration{Version: "fixture_2", Description: "second fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN two text"},
		{Migration: Migration{Version: "fixture_3", Description: "uncovered fixture change"}, SQL: "ALTER TABLE probe ADD COLUMN three text"},
	}

	for _, tc := range []struct {
		name     string
		sequence []string
	}{
		{name: "missing", sequence: []string{"fixture_2"}},
		{name: "extra", sequence: []string{"fixture_1", "fixture_2", "fixture_3"}},
		{name: "reordered", sequence: []string{"fixture_2", "fixture_1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transitionFile := fixtureTransitionVerifierFile(t, verifierFile, tc.sequence)
			registry, err := NewSchemaVerifierRegistry(
				[]VerifierAssetFile{verifierFile},
				[]VerifierAssetFile{sourceFile, targetFile, transitionFile},
			)
			require.NoError(t, err)
			entries := []ReleaseCatalogEntry{
				{
					ReleaseID: "release-a", SchemaVersion: "schema-a", BaselineVersion: "baseline-a",
					ReleaseManifestSHA256: fixtureDigestA,
					SourceSchemaAsset:     ReleaseAsset{Name: sourceFile.Name, SHA256: checksumSQL(string(sourceFile.Content))},
					BaselineAsset:         ReleaseAsset{Name: "baseline.sql", SHA256: fixtureDigestA},
					CoveredMigrations:     []string{"base"},
				},
				{
					ReleaseID: "release-b", SchemaVersion: "schema-b", BaselineVersion: "baseline-b",
					ReleaseManifestSHA256: fixtureDigestB,
					SourceSchemaAsset:     ReleaseAsset{Name: targetFile.Name, SHA256: checksumSQL(string(targetFile.Content))},
					BaselineAsset:         ReleaseAsset{Name: "baseline.sql", SHA256: fixtureDigestB},
					CoveredMigrations:     []string{"base", "fixture_1", "fixture_2"},
					TransitionAssets:      []ReleaseAsset{{Name: transitionFile.Name, SHA256: checksumSQL(string(transitionFile.Content))}},
				},
			}
			_, err = NewUpgradeReleaseCatalog(entries, registry, migrations)
			require.ErrorContains(t, err, "exact ordered target coverage delta")
		})
	}
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
	verifier VerifierAssetFile,
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
		Verifier:       ReleaseAsset{Name: verifier.Name, SHA256: checksumSQL(string(verifier.Content))},
		Platform:       releasePlatformRequirement{PostgresMajor: 17, VectorVersion: "0.8.6"},
		ManagedSchemas: []string{"public"},
		Extensions: catalogExtensionInventory{
			Empty:     []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
			Installed: []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
		},
		Phases: catalogFingerprintPhases{
			Empty:                       fixtureDigestA,
			Prepared:                    fixtureDigestA,
			EntSchema:                   fixtureDigestA,
			CurrentReleasePrePrivileges: map[string]string{"release-a": fixtureDigestA, "release-b": fixtureDigestB}[releaseID],
			CurrentRelease:              map[string]string{"release-a": fixtureDigestA, "release-b": fixtureDigestB}[releaseID],
		},
	}
	content, err := json.Marshal(asset)
	require.NoError(t, err)
	return VerifierAssetFile{Name: name, Content: content}
}

func fixtureTransitionVerifierFile(t *testing.T, verifier VerifierAssetFile, sequence []string) VerifierAssetFile {
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
		Verifier:       ReleaseAsset{Name: verifier.Name, SHA256: checksumSQL(string(verifier.Content))},
		Platform:       releasePlatformRequirement{PostgresMajor: 17, VectorVersion: "0.8.6"},
		ManagedSchemas: []string{"public"},
	}
	for index, version := range sequence {
		fingerprint := fixtureDigestC
		if index == len(sequence)-1 {
			fingerprint = fixtureDigestB
		}
		asset.Migrations = append(asset.Migrations, catalogTransitionMigration{
			Version: version,
			SQLSHA256: checksumSQL(map[string]string{
				"fixture_1": "ALTER TABLE probe ADD COLUMN one text",
				"fixture_2": "ALTER TABLE probe ADD COLUMN two text",
				"fixture_3": "ALTER TABLE probe ADD COLUMN three text",
			}[version]),
			Fingerprint: fingerprint,
			Extensions:  []catalogExtensionIdentity{{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"}},
		})
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
