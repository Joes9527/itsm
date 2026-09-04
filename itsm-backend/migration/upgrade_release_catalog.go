package migration

import (
	"context"
	"fmt"
	"strings"
)

// UpgradeReleaseCatalog is a validated immutable view of release entries,
// checksum-pinned verifier assets, and executable or historical migrations.
type UpgradeReleaseCatalog struct {
	entries    []ReleaseCatalogEntry
	registry   *SchemaVerifierRegistry
	migrations map[string]CatalogedMigration
	ordered    []CatalogedMigration
}

// PlannedCatalogedMigration can only be produced by PlanCatalogedUpgrade. Its
// unexported SQL/checksum fields prevent callers from forging a migration that
// bypasses the transition-prefix registry.
type PlannedCatalogedMigration struct {
	descriptor Migration
	sql        string
	sha256     string
}

func (migration PlannedCatalogedMigration) Version() string { return migration.descriptor.Version }

func (migration PlannedCatalogedMigration) Descriptor() Migration { return migration.descriptor }

// NewUpgradeReleaseCatalog validates all retained source and transition assets
// before the catalog can participate in planning.
func NewUpgradeReleaseCatalog(
	entries []ReleaseCatalogEntry,
	registry *SchemaVerifierRegistry,
	migrations []CatalogedMigration,
) (*UpgradeReleaseCatalog, error) {
	if len(entries) == 0 || registry == nil {
		return nil, fmt.Errorf("upgrade release catalog entries and verifier registry are required")
	}
	catalog := &UpgradeReleaseCatalog{
		entries:    make([]ReleaseCatalogEntry, 0, len(entries)),
		registry:   registry,
		migrations: make(map[string]CatalogedMigration, len(migrations)),
		ordered:    make([]CatalogedMigration, 0, len(migrations)),
	}
	seenEntries := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		identity := entry.ReleaseID + "\x00" + entry.SchemaVersion + "\x00" + entry.BaselineVersion
		if strings.TrimSpace(entry.ReleaseID) == "" || strings.TrimSpace(entry.SchemaVersion) == "" ||
			strings.TrimSpace(entry.BaselineVersion) == "" || !sha256Pattern.MatchString(entry.ReleaseManifestSHA256) ||
			strings.TrimSpace(entry.BaselineAsset.Name) == "" || !sha256Pattern.MatchString(entry.BaselineAsset.SHA256) ||
			strings.TrimSpace(entry.SourceSchemaAsset.Name) == "" || !sha256Pattern.MatchString(entry.SourceSchemaAsset.SHA256) {
			return nil, fmt.Errorf("upgrade release catalog entry is invalid")
		}
		if _, duplicate := seenEntries[identity]; duplicate {
			return nil, fmt.Errorf("upgrade release catalog entry is duplicated")
		}
		seenEntries[identity] = struct{}{}
		entry.CoveredMigrations = append([]string(nil), entry.CoveredMigrations...)
		entry.TransitionAssets = append([]ReleaseAsset(nil), entry.TransitionAssets...)
		catalog.entries = append(catalog.entries, entry)
	}
	for _, item := range migrations {
		version := strings.TrimSpace(item.Migration.Version)
		if version == "" || strings.TrimSpace(item.Migration.Description) == "" {
			return nil, fmt.Errorf("cataloged migration is incomplete")
		}
		if _, duplicate := catalog.migrations[version]; duplicate {
			return nil, fmt.Errorf("cataloged migration %q is duplicated", version)
		}
		checksum := item.SHA256
		if strings.TrimSpace(item.SQL) != "" {
			actual := checksumSQL(item.SQL)
			if checksum != "" && checksum != actual {
				return nil, fmt.Errorf("cataloged migration %q checksum mismatch", version)
			}
			checksum = actual
		}
		if !sha256Pattern.MatchString(checksum) {
			return nil, fmt.Errorf("cataloged migration %q checksum is invalid", version)
		}
		item.SHA256 = checksum
		catalog.migrations[version] = item
		catalog.ordered = append(catalog.ordered, item)
	}
	for _, entry := range catalog.entries {
		seenCovered := make(map[string]struct{}, len(entry.CoveredMigrations))
		for _, version := range entry.CoveredMigrations {
			if _, duplicate := seenCovered[version]; duplicate {
				return nil, fmt.Errorf("release catalog contains duplicate covered migration %q", version)
			}
			seenCovered[version] = struct{}{}
			if _, exists := catalog.migrations[version]; !exists {
				return nil, fmt.Errorf("release catalog covered migration %q is unavailable", version)
			}
		}
	}
	if err := verifyReleaseCatalogVerifierAssets(catalog.entries, registry); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (catalog *UpgradeReleaseCatalog) entryForState(state SchemaState) (ReleaseCatalogEntry, error) {
	if catalog == nil || state.ID != 1 {
		return ReleaseCatalogEntry{}, fmt.Errorf("unsupported upgrade source release")
	}
	for _, entry := range catalog.entries {
		if state.ReleaseID == entry.ReleaseID && state.SchemaVersion == entry.SchemaVersion &&
			state.BaselineVersion == entry.BaselineVersion &&
			state.ReleaseManifestChecksum == entry.ReleaseManifestSHA256 {
			return entry, nil
		}
	}
	return ReleaseCatalogEntry{}, fmt.Errorf("unsupported upgrade source release")
}

func (catalog *UpgradeReleaseCatalog) entryForTarget(target ReleaseManifest) (ReleaseCatalogEntry, error) {
	if catalog == nil {
		return ReleaseCatalogEntry{}, fmt.Errorf("upgrade target release catalog is required")
	}
	checksum, err := target.Checksum()
	if err != nil {
		return ReleaseCatalogEntry{}, fmt.Errorf("validate upgrade target release: %w", err)
	}
	for _, entry := range catalog.entries {
		if target.ReleaseID == entry.ReleaseID && target.SchemaVersion == entry.SchemaVersion &&
			target.BaselineVersion == entry.BaselineVersion && checksum == entry.ReleaseManifestSHA256 {
			return entry, nil
		}
	}
	return ReleaseCatalogEntry{}, fmt.Errorf("upgrade target release is not cataloged")
}

// PlanCatalogedUpgrade validates the exact source release, full committed
// ledger, and source-or-prefix fingerprint before returning remaining work.
// Every operation before the return is read-only.
func (m *Migrator) PlanCatalogedUpgrade(
	ctx context.Context,
	target ReleaseManifest,
	catalog *UpgradeReleaseCatalog,
) ([]PlannedCatalogedMigration, error) {
	if m == nil || m.db == nil || catalog == nil {
		return nil, fmt.Errorf("cataloged upgrade migration store is required")
	}
	if err := VerifySchemaStateStorage(ctx, m.db); err != nil {
		return nil, fmt.Errorf("verify cataloged upgrade state: %w", err)
	}
	state, err := ReadSchemaState(ctx, m.db)
	if err != nil {
		return nil, fmt.Errorf("read cataloged upgrade state: %w", err)
	}
	source, err := catalog.entryForState(state)
	if err != nil {
		return nil, err
	}
	targetEntry, err := catalog.entryForTarget(target)
	if err != nil {
		return nil, err
	}
	applied, err := m.GetAppliedMigrations(ctx)
	if err != nil {
		return nil, fmt.Errorf("read cataloged migration history: %w", err)
	}
	relevant := make([]Migration, 0, len(applied))
	for _, item := range applied {
		cataloged, exists := catalog.migrations[item.Version]
		if !exists || item.Checksum != cataloged.SHA256 {
			return nil, fmt.Errorf("cataloged migration ledger checksum or version mismatch")
		}
		if cataloged.SQL != "" {
			relevant = append(relevant, item)
		}
	}
	plan, err := catalog.registry.planTransition(source, targetEntry, relevant, catalog.ordered)
	if err != nil {
		return nil, err
	}
	if err := catalog.registry.verifyFingerprint(
		ctx,
		m.db,
		plan.ExpectedFingerprint,
		plan.ExpectedVerifier,
		plan.ExpectedExtensions,
		plan.ExpectedPlatform,
		true,
		plan.ManagedSchemas,
	); err != nil {
		return nil, fmt.Errorf("cataloged upgrade source or transition prefix fingerprint mismatch")
	}
	result := make([]PlannedCatalogedMigration, 0, len(plan.Pending))
	for _, item := range plan.Pending {
		result = append(result, PlannedCatalogedMigration{
			descriptor: item.Migration,
			sql:        item.SQL,
			sha256:     item.SHA256,
		})
	}
	return result, nil
}

// ApplyCatalogedMigration applies only a value emitted by the exact
// transition planner and records its pinned checksum in the same transaction.
func (m *Migrator) ApplyCatalogedMigration(ctx context.Context, migration PlannedCatalogedMigration) error {
	if m == nil || m.db == nil || strings.TrimSpace(migration.descriptor.Version) == "" ||
		strings.TrimSpace(migration.sql) == "" || !sha256Pattern.MatchString(migration.sha256) ||
		checksumSQL(migration.sql) != migration.sha256 {
		return fmt.Errorf("planned cataloged migration is invalid")
	}
	return m.applyMigrationSQL(ctx, migration.descriptor, migration.sql, migration.sha256)
}
