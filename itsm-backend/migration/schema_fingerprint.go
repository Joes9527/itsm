package migration

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

const (
	CurrentSourceSchemaAssetName    = "source-schema/028_schema_release_state.json"
	catalogFingerprintVerifierName  = "catalog-verifier/postgres-v2.sql"
	catalogFingerprintFormatVersion = 1
)

//go:embed sql/catalog/postgres-v2.sql
var postgresCatalogFingerprintSQL string

//go:embed sql/catalog sql/source sql/transition
var embeddedSchemaVerifierAssets embed.FS

type catalogFingerprintPhases struct {
	Empty                       string `json:"empty"`
	Prepared                    string `json:"prepared"`
	EntSchema                   string `json:"entSchema"`
	CurrentReleasePrePrivileges string `json:"currentReleasePrePrivileges"`
	CurrentRelease              string `json:"currentRelease"`
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
	Kind           string                     `json:"kind"`
	AssetID        string                     `json:"assetId"`
	FormatVersion  int                        `json:"formatVersion"`
	Release        catalogReleaseIdentity     `json:"release"`
	Verifier       ReleaseAsset               `json:"verifier"`
	Platform       releasePlatformRequirement `json:"platform"`
	ManagedSchemas []string                   `json:"managedSchemas"`
	Extensions     catalogExtensionInventory  `json:"extensions"`
	Phases         catalogFingerprintPhases   `json:"phases"`
}

type catalogReleaseIdentity struct {
	ReleaseID       string `json:"releaseId"`
	SchemaVersion   string `json:"schemaVersion"`
	BaselineVersion string `json:"baselineVersion"`
}

func (identity catalogReleaseIdentity) valid() bool {
	return strings.TrimSpace(identity.ReleaseID) != "" && strings.TrimSpace(identity.SchemaVersion) != "" &&
		strings.TrimSpace(identity.BaselineVersion) != ""
}

func (identity catalogReleaseIdentity) matches(entry ReleaseCatalogEntry) bool {
	return identity.ReleaseID == entry.ReleaseID && identity.SchemaVersion == entry.SchemaVersion &&
		identity.BaselineVersion == entry.BaselineVersion
}

func currentSourceSchemaAsset() ReleaseAsset {
	content, err := embeddedSchemaVerifierAssets.ReadFile("sql/source/028_schema_release_state.json")
	if err != nil {
		panic(fmt.Sprintf("read current source schema verifier asset: %v", err))
	}
	return ReleaseAsset{
		Name:   CurrentSourceSchemaAssetName,
		SHA256: checksumSQL(string(content)),
	}
}

func currentCatalogVerifierAsset() ReleaseAsset {
	asset, err := loadCurrentCatalogFingerprintAsset()
	if err != nil {
		panic(fmt.Sprintf("read current catalog verifier asset: %v", err))
	}
	return asset.Verifier
}

func loadCurrentCatalogFingerprintAsset() (catalogFingerprintAsset, error) {
	registry, err := loadEmbeddedSchemaVerifierRegistry()
	if err != nil {
		return catalogFingerprintAsset{}, err
	}
	asset, err := registry.loadSource(currentSourceSchemaAsset())
	if err != nil {
		return catalogFingerprintAsset{}, err
	}
	if !asset.Release.matches(ReleaseCatalogEntry{
		ReleaseID: currentReleaseID, SchemaVersion: currentSchemaVersion, BaselineVersion: currentBaselineVersion,
	}) {
		return catalogFingerprintAsset{}, fmt.Errorf("source schema fingerprint asset identity mismatch")
	}
	return asset, nil
}

func loadEmbeddedSchemaVerifierRegistry() (*SchemaVerifierRegistry, error) {
	var verifierFiles []VerifierAssetFile
	err := fs.WalkDir(embeddedSchemaVerifierAssets, "sql/catalog", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(filePath) != ".sql" {
			return nil
		}
		content, err := embeddedSchemaVerifierAssets.ReadFile(filePath)
		if err != nil {
			return err
		}
		verifierFiles = append(verifierFiles, VerifierAssetFile{
			Name: path.Join("catalog-verifier", path.Base(filePath)), Content: content,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load embedded catalog verifier registry: %w", err)
	}
	var files []VerifierAssetFile
	for _, root := range []string{"sql/source", "sql/transition"} {
		err = fs.WalkDir(embeddedSchemaVerifierAssets, root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || path.Ext(filePath) != ".json" {
				return nil
			}
			content, err := embeddedSchemaVerifierAssets.ReadFile(filePath)
			if err != nil {
				return err
			}
			prefix := "source-schema"
			if root == "sql/transition" {
				prefix = "transition-schema"
			}
			files = append(files, VerifierAssetFile{Name: path.Join(prefix, path.Base(filePath)), Content: content})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("load embedded schema verifier registry: %w", err)
		}
	}
	return NewSchemaVerifierRegistry(verifierFiles, files)
}

// EmbeddedSchemaVerifierRegistry returns an immutable registry containing all
// source and transition assets retained by this binary.
func EmbeddedSchemaVerifierRegistry() (*SchemaVerifierRegistry, error) {
	return loadEmbeddedSchemaVerifierRegistry()
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

func catalogFingerprintWithSQL(ctx context.Context, db DBTX, verifierSQL string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("catalog fingerprint database is required")
	}
	var canonical string
	var queryErr error
	if strings.Contains(verifierSQL, "$1") {
		roles, err := schemaStateRolesFromContext(ctx)
		if err != nil {
			return "", fmt.Errorf("catalog fingerprint role categories are unavailable: %w", err)
		}
		if err := verifyCatalogRoleBoundary(ctx, db, roles); err != nil {
			return "", err
		}
		queryErr = db.QueryRowContext(
			ctx, verifierSQL, roles.MigrationRole, roles.RuntimeRole, roles.BootstrapRole,
		).Scan(&canonical)
	} else {
		queryErr = db.QueryRowContext(ctx, verifierSQL).Scan(&canonical)
	}
	if queryErr != nil {
		return "", fmt.Errorf("inspect PostgreSQL catalog fingerprint: %w", queryErr)
	}
	return checksumSQL(canonical), nil
}

// verifyCatalogRoleBoundary fixes the three deployment-specific principals
// into stable verifier categories. The exact names never enter an immutable
// fingerprint or an error. PostgreSQL 17 supplies the recursive role-option
// semantics used by the checksum-pinned v2 verifier itself.
func verifyCatalogRoleBoundary(ctx context.Context, db DBTX, roles SchemaStateRoles) error {
	if err := validateSchemaStateRoles(roles); err != nil {
		return err
	}
	var executorMatches, migrationExists, runtimeExists, bootstrapExists bool
	var runtimeSuper, runtimeBypass, bootstrapSuper bool
	var extraSuperusers int64
	if err := db.QueryRowContext(ctx, `
		SELECT current_user = $1,
		       EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1),
		       EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $2),
		       EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $3),
		       COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = $2), false),
		       COALESCE((SELECT rolbypassrls FROM pg_roles WHERE rolname = $2), false),
		       COALESCE((SELECT rolsuper FROM pg_roles WHERE rolname = $3), false),
		       (SELECT count(*) FROM pg_roles WHERE rolsuper AND rolname <> $3)
	`, roles.MigrationRole, roles.RuntimeRole, roles.BootstrapRole).Scan(
		&executorMatches,
		&migrationExists,
		&runtimeExists,
		&bootstrapExists,
		&runtimeSuper,
		&runtimeBypass,
		&bootstrapSuper,
		&extraSuperusers,
	); err != nil {
		return fmt.Errorf("inspect catalog role categories: database operation failed")
	}
	if !executorMatches || !migrationExists {
		return fmt.Errorf("catalog migration role boundary is invalid")
	}
	if !runtimeExists || runtimeSuper || runtimeBypass {
		return fmt.Errorf("catalog runtime role boundary is invalid")
	}
	if !bootstrapExists || !bootstrapSuper || extraSuperusers != 0 {
		return fmt.Errorf("catalog bootstrap DBA boundary is invalid")
	}
	return nil
}

func catalogFingerprint(ctx context.Context, db DBTX) (string, error) {
	return catalogFingerprintWithSQL(ctx, db, postgresCatalogFingerprintSQL)
}

// CatalogFingerprint exposes the read-only canonical hash for release tooling
// and external integration fixtures. Production validation compares it only
// with immutable embedded assets.
func CatalogFingerprint(ctx context.Context, db DBTX) (string, error) {
	registry, err := loadEmbeddedSchemaVerifierRegistry()
	if err != nil {
		return "", err
	}
	asset, err := registry.loadSource(currentSourceSchemaAsset())
	if err != nil {
		return "", err
	}
	query, err := registry.loadVerifier(asset.Verifier)
	if err != nil {
		return "", err
	}
	return catalogFingerprintWithSQL(ctx, db, query)
}

func verifyManagedSchemas(ctx context.Context, db DBTX, expected []string) error {
	if len(expected) != 1 {
		return fmt.Errorf("managed schema inventory is invalid")
	}
	var current string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()::text`).Scan(&current); err != nil {
		return fmt.Errorf("inspect managed schema: %w", err)
	}
	if current != expected[0] {
		return fmt.Errorf("managed schema boundary mismatch")
	}
	return nil
}

func (registry *SchemaVerifierRegistry) verifyFingerprint(
	ctx context.Context,
	db DBTX,
	expected string,
	verifier ReleaseAsset,
	extensions []catalogExtensionIdentity,
	platform releasePlatformRequirement,
	requireInstalled bool,
	managedSchemas []string,
) error {
	if registry == nil {
		return fmt.Errorf("schema verifier registry is required")
	}
	if err := verifyReleasePlatform(ctx, db, platform, requireInstalled); err != nil {
		return err
	}
	if err := verifyManagedSchemas(ctx, db, managedSchemas); err != nil {
		return err
	}
	if err := verifyCatalogExtensions(ctx, db, extensions); err != nil {
		return err
	}
	query, err := registry.loadVerifier(verifier)
	if err != nil {
		return err
	}
	actual, err := catalogFingerprintWithSQL(ctx, db, query)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("schema fingerprint mismatch")
	}
	return nil
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
	registry, err := loadEmbeddedSchemaVerifierRegistry()
	if err != nil {
		return err
	}
	asset, err := registry.loadSource(currentSourceSchemaAsset())
	if err != nil {
		return err
	}
	phaseName, expected, err := expectedFreshPhaseFingerprint(asset, phase)
	if err != nil {
		return err
	}
	verify := func(fingerprint string) error {
		return registry.verifyFingerprint(
			ctx,
			db,
			fingerprint,
			asset.Verifier,
			expectedFreshPhaseExtensions(asset, phase),
			asset.Platform,
			phase != freshTargetEmpty,
			asset.ManagedSchemas,
		)
	}
	if phase == freshTargetCurrentRelease {
		if err := verify(asset.Phases.CurrentReleasePrePrivileges); err == nil {
			return nil
		}
	}
	if err := verify(
		expected,
	); err != nil {
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
	registry, err := loadEmbeddedSchemaVerifierRegistry()
	if err != nil {
		return fmt.Errorf("unsupported upgrade source: no full-schema verifier is registered")
	}
	asset, err := registry.loadSource(entry.SourceSchemaAsset)
	if err != nil || !asset.Release.matches(entry) {
		return fmt.Errorf("unsupported upgrade source: no full-schema verifier is registered")
	}
	if err := registry.verifyFingerprint(
		ctx,
		db,
		asset.Phases.CurrentRelease,
		asset.Verifier,
		asset.Extensions.Installed,
		asset.Platform,
		true,
		asset.ManagedSchemas,
	); err != nil {
		return fmt.Errorf("unsupported upgrade source: schema does not match cataloged release %s", entry.SchemaVersion)
	}
	return nil
}
