package migration

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	CurrentSourceSchemaAssetName    = "source-schema/028_schema_release_state.json"
	catalogFingerprintVerifierName  = "catalog-verifier/postgres-v1.sql"
	catalogFingerprintFormatVersion = 1
)

//go:embed sql/catalog/postgres-v1.sql
var postgresCatalogFingerprintSQL string

//go:embed sql/source/028_schema_release_state.json
var currentSourceSchemaAssetJSON []byte

type catalogFingerprintPhases struct {
	Empty          string `json:"empty"`
	Prepared       string `json:"prepared"`
	EntSchema      string `json:"entSchema"`
	CurrentRelease string `json:"currentRelease"`
}

type catalogExtensionIdentity struct {
	Name    string `json:"name"`
	Schema  string `json:"schema"`
	Version string `json:"version"`
}

type catalogExtensionInventory struct {
	Empty     []catalogExtensionIdentity `json:"empty"`
	Installed []catalogExtensionIdentity `json:"installed"`
}

type catalogFingerprintAsset struct {
	FormatVersion int                       `json:"formatVersion"`
	SchemaVersion string                    `json:"schemaVersion"`
	Verifier      ReleaseAsset              `json:"verifier"`
	Extensions    catalogExtensionInventory `json:"extensions"`
	Phases        catalogFingerprintPhases  `json:"phases"`
}

func currentSourceSchemaAsset() ReleaseAsset {
	return ReleaseAsset{
		Name:   CurrentSourceSchemaAssetName,
		SHA256: checksumSQL(string(currentSourceSchemaAssetJSON)),
	}
}

func loadCurrentCatalogFingerprintAsset() (catalogFingerprintAsset, error) {
	decoder := json.NewDecoder(bytes.NewReader(currentSourceSchemaAssetJSON))
	decoder.DisallowUnknownFields()
	var asset catalogFingerprintAsset
	if err := decoder.Decode(&asset); err != nil {
		return catalogFingerprintAsset{}, fmt.Errorf("decode source schema fingerprint asset: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return catalogFingerprintAsset{}, fmt.Errorf("decode source schema fingerprint asset: %w", err)
	}
	if asset.FormatVersion != catalogFingerprintFormatVersion ||
		asset.SchemaVersion != currentSchemaVersion {
		return catalogFingerprintAsset{}, fmt.Errorf("source schema fingerprint asset identity mismatch")
	}
	if asset.Verifier.Name != catalogFingerprintVerifierName ||
		asset.Verifier.SHA256 != checksumSQL(postgresCatalogFingerprintSQL) {
		return catalogFingerprintAsset{}, fmt.Errorf("source schema fingerprint verifier identity mismatch")
	}
	for _, digest := range []string{
		asset.Phases.Empty,
		asset.Phases.Prepared,
		asset.Phases.EntSchema,
		asset.Phases.CurrentRelease,
	} {
		if !sha256Pattern.MatchString(digest) {
			return catalogFingerprintAsset{}, fmt.Errorf("source schema phase fingerprint is invalid")
		}
	}
	for name, inventory := range map[string][]catalogExtensionIdentity{
		"empty":     asset.Extensions.Empty,
		"installed": asset.Extensions.Installed,
	} {
		if len(inventory) == 0 {
			return catalogFingerprintAsset{}, fmt.Errorf("source schema %s extension inventory is required", name)
		}
		previous := ""
		for _, extension := range inventory {
			if strings.TrimSpace(extension.Name) == "" || strings.TrimSpace(extension.Schema) == "" ||
				strings.TrimSpace(extension.Version) == "" || extension.Name <= previous {
				return catalogFingerprintAsset{}, fmt.Errorf("source schema %s extension inventory is invalid", name)
			}
			previous = extension.Name
		}
	}
	return asset, nil
}

func readCatalogExtensionInventory(ctx context.Context, db DBTX) ([]catalogExtensionIdentity, error) {
	var encoded string
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(
			jsonb_agg(
				jsonb_build_object(
					'name', extension_record.extname,
					'schema', namespace.nspname,
					'version', extension_record.extversion
				)
				ORDER BY extension_record.extname
			)::text,
			'[]'
		)
		FROM pg_extension extension_record
		JOIN pg_namespace namespace ON namespace.oid = extension_record.extnamespace
	`).Scan(&encoded); err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL extension inventory: %w", err)
	}
	var inventory []catalogExtensionIdentity
	if err := json.Unmarshal([]byte(encoded), &inventory); err != nil {
		return nil, fmt.Errorf("decode PostgreSQL extension inventory: %w", err)
	}
	return inventory, nil
}

func expectedFreshPhaseExtensions(asset catalogFingerprintAsset, phase freshTargetPhase) []catalogExtensionIdentity {
	if phase == freshTargetEmpty {
		return asset.Extensions.Empty
	}
	return asset.Extensions.Installed
}

func verifyCatalogExtensions(
	ctx context.Context,
	db DBTX,
	expected []catalogExtensionIdentity,
) error {
	actual, err := readCatalogExtensionInventory(ctx, db)
	if err != nil {
		return err
	}
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("PostgreSQL extension inventory mismatch")
	}
	return nil
}

func catalogFingerprint(ctx context.Context, db DBTX) (string, error) {
	if db == nil {
		return "", fmt.Errorf("catalog fingerprint database is required")
	}
	var canonical string
	if err := db.QueryRowContext(ctx, postgresCatalogFingerprintSQL).Scan(&canonical); err != nil {
		return "", fmt.Errorf("inspect PostgreSQL catalog fingerprint: %w", err)
	}
	return checksumSQL(canonical), nil
}

func expectedFreshPhaseFingerprint(asset catalogFingerprintAsset, phase freshTargetPhase) (string, string, error) {
	switch phase {
	case freshTargetEmpty:
		return "empty", asset.Phases.Empty, nil
	case freshTargetPrepared:
		return "prepared", asset.Phases.Prepared, nil
	case freshTargetEntSchema:
		return "Ent schema", asset.Phases.EntSchema, nil
	case freshTargetCurrentRelease:
		return "current release", asset.Phases.CurrentRelease, nil
	default:
		return "", "", fmt.Errorf("unknown fresh target phase")
	}
}

func verifyFreshPhaseCatalog(ctx context.Context, db DBTX, phase freshTargetPhase) error {
	asset, err := loadCurrentCatalogFingerprintAsset()
	if err != nil {
		return err
	}
	phaseName, expected, err := expectedFreshPhaseFingerprint(asset, phase)
	if err != nil {
		return err
	}
	if err := verifyCatalogExtensions(ctx, db, expectedFreshPhaseExtensions(asset, phase)); err != nil {
		return fmt.Errorf("fresh target catalog does not match verified %s phase", phaseName)
	}
	actual, err := catalogFingerprint(ctx, db)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("fresh target catalog does not match verified %s phase", phaseName)
	}
	return nil
}

// VerifyCatalogedUpgradeSourceSchema applies the immutable, checksum-pinned,
// read-only full-schema fingerprint owned by an exact release-catalog entry.
// It deliberately returns only the source release category on mismatch.
func VerifyCatalogedUpgradeSourceSchema(ctx context.Context, db DBTX, entry ReleaseCatalogEntry) error {
	if db == nil {
		return fmt.Errorf("upgrade source database is required")
	}
	resolved, err := CatalogEntryForSchemaState(SchemaState{
		ID:                      1,
		ReleaseID:               entry.ReleaseID,
		SchemaVersion:           entry.SchemaVersion,
		BaselineVersion:         entry.BaselineVersion,
		ReleaseManifestChecksum: entry.ReleaseManifestSHA256,
	})
	if err != nil || !sameReleaseCatalogEntry(resolved, entry) {
		return fmt.Errorf("unsupported upgrade source: no full-schema verifier is registered")
	}
	asset, err := loadCurrentCatalogFingerprintAsset()
	if err != nil || entry.SourceSchemaAsset != currentSourceSchemaAsset() ||
		asset.SchemaVersion != entry.SchemaVersion {
		return fmt.Errorf("unsupported upgrade source: no full-schema verifier is registered")
	}
	if err := verifyCatalogExtensions(ctx, db, asset.Extensions.Installed); err != nil {
		return fmt.Errorf("unsupported upgrade source: schema does not match cataloged release %s", entry.SchemaVersion)
	}
	actual, err := catalogFingerprint(ctx, db)
	if err != nil {
		return fmt.Errorf("inspect cataloged upgrade source category")
	}
	if actual != asset.Phases.CurrentRelease {
		return fmt.Errorf("unsupported upgrade source: schema does not match cataloged release %s", entry.SchemaVersion)
	}
	return nil
}
