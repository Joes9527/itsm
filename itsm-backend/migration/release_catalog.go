package migration

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const minimumSupportedUpgradeSchemaVersion = "028_schema_release_state"

// ReleaseCatalogEntry is an immutable, explicitly enumerated fresh-baseline
// boundary. CoveredMigrations is a set, not a numeric interval: a migration
// published later with a lower number is never silently swallowed by a head.
type ReleaseCatalogEntry struct {
	ReleaseID             string       `json:"releaseId"`
	SchemaVersion         string       `json:"schemaVersion"`
	BaselineVersion       string       `json:"baselineVersion"`
	ReleaseManifestSHA256 string       `json:"releaseManifestSha256"`
	BaselineAsset         ReleaseAsset `json:"baselineAsset"`
	SourceSchemaAsset     ReleaseAsset `json:"sourceSchemaAsset"`
	CoveredMigrations     []string     `json:"coveredMigrations"`
}

type releaseCatalog struct {
	Entries         []ReleaseCatalogEntry `json:"entries"`
	PublicationGate publicationGate       `json:"publicationGate"`
}

type publicationGate struct {
	MissingAllocatedMigrations []string `json:"missingAllocatedMigrations"`
}

//go:embed release_catalog.json
var releaseCatalogJSON []byte

func loadReleaseCatalog() (releaseCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(releaseCatalogJSON))
	decoder.DisallowUnknownFields()
	var catalog releaseCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return releaseCatalog{}, fmt.Errorf("decode release catalog: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return releaseCatalog{}, fmt.Errorf("decode release catalog: %w", err)
	}
	if len(catalog.Entries) == 0 {
		return releaseCatalog{}, fmt.Errorf("release catalog entries are required")
	}
	seenEntries := make(map[string]struct{}, len(catalog.Entries))
	for index := range catalog.Entries {
		entry := &catalog.Entries[index]
		identity := entry.ReleaseID + "\x00" + entry.SchemaVersion + "\x00" + entry.BaselineVersion
		if strings.TrimSpace(entry.ReleaseID) == "" || strings.TrimSpace(entry.SchemaVersion) == "" ||
			strings.TrimSpace(entry.BaselineVersion) == "" {
			return releaseCatalog{}, fmt.Errorf("release catalog entry %d identity is incomplete", index)
		}
		if _, duplicate := seenEntries[identity]; duplicate {
			return releaseCatalog{}, fmt.Errorf("release catalog contains duplicate entry")
		}
		seenEntries[identity] = struct{}{}
		if !sha256Pattern.MatchString(entry.ReleaseManifestSHA256) ||
			strings.TrimSpace(entry.BaselineAsset.Name) == "" ||
			!sha256Pattern.MatchString(entry.BaselineAsset.SHA256) ||
			strings.TrimSpace(entry.SourceSchemaAsset.Name) == "" ||
			!sha256Pattern.MatchString(entry.SourceSchemaAsset.SHA256) {
			return releaseCatalog{}, fmt.Errorf("release catalog entry %d artifact identity is invalid", index)
		}
		if len(entry.CoveredMigrations) == 0 {
			return releaseCatalog{}, fmt.Errorf("release catalog entry %d covered migrations are required", index)
		}
		seenCovered := make(map[string]struct{}, len(entry.CoveredMigrations))
		for _, version := range entry.CoveredMigrations {
			if strings.TrimSpace(version) == "" {
				return releaseCatalog{}, fmt.Errorf("release catalog entry %d has empty covered migration", index)
			}
			if _, duplicate := seenCovered[version]; duplicate {
				return releaseCatalog{}, fmt.Errorf("release catalog entry %d has duplicate covered migration", index)
			}
			seenCovered[version] = struct{}{}
		}
		entry.CoveredMigrations = append([]string(nil), entry.CoveredMigrations...)
	}
	seenGate := make(map[string]struct{}, len(catalog.PublicationGate.MissingAllocatedMigrations))
	for _, version := range catalog.PublicationGate.MissingAllocatedMigrations {
		if strings.TrimSpace(version) == "" {
			return releaseCatalog{}, fmt.Errorf("release publication gate contains empty migration")
		}
		if _, duplicate := seenGate[version]; duplicate {
			return releaseCatalog{}, fmt.Errorf("release publication gate contains duplicate migration")
		}
		seenGate[version] = struct{}{}
	}
	catalog.PublicationGate.MissingAllocatedMigrations = append(
		[]string(nil), catalog.PublicationGate.MissingAllocatedMigrations...,
	)
	return catalog, nil
}

// CurrentReleaseCatalogEntry returns a defensive copy of the immutable entry
// selected by the compiled release identity.
func CurrentReleaseCatalogEntry() (ReleaseCatalogEntry, error) {
	catalog, err := loadReleaseCatalog()
	if err != nil {
		return ReleaseCatalogEntry{}, err
	}
	for _, entry := range catalog.Entries {
		if entry.ReleaseID == currentReleaseID && entry.SchemaVersion == currentSchemaVersion &&
			entry.BaselineVersion == currentBaselineVersion {
			entry.CoveredMigrations = append([]string(nil), entry.CoveredMigrations...)
			return entry, nil
		}
	}
	return ReleaseCatalogEntry{}, fmt.Errorf("compiled release has no immutable release catalog entry")
}

// CatalogEntryForSchemaState resolves only an exact immutable state marker.
// There is intentionally no fallback to schema-version ordering or a numeric
// head because such inference would hide later-published lower numbers.
func CatalogEntryForSchemaState(state SchemaState) (ReleaseCatalogEntry, error) {
	catalog, err := loadReleaseCatalog()
	if err != nil {
		return ReleaseCatalogEntry{}, fmt.Errorf("load upgrade release catalog: %w", err)
	}
	if state.ID != 1 {
		return ReleaseCatalogEntry{}, fmt.Errorf("unsupported upgrade source: minimum cataloged release is %s", minimumSupportedUpgradeSchemaVersion)
	}
	for _, entry := range catalog.Entries {
		if state.ReleaseID == entry.ReleaseID && state.SchemaVersion == entry.SchemaVersion &&
			state.BaselineVersion == entry.BaselineVersion &&
			state.ReleaseManifestChecksum == entry.ReleaseManifestSHA256 {
			entry.CoveredMigrations = append([]string(nil), entry.CoveredMigrations...)
			return entry, nil
		}
	}
	return ReleaseCatalogEntry{}, fmt.Errorf("unsupported upgrade source: minimum cataloged release is %s", minimumSupportedUpgradeSchemaVersion)
}

func sameReleaseCatalogEntry(left, right ReleaseCatalogEntry) bool {
	return left.ReleaseID == right.ReleaseID && left.SchemaVersion == right.SchemaVersion &&
		left.BaselineVersion == right.BaselineVersion &&
		left.ReleaseManifestSHA256 == right.ReleaseManifestSHA256 &&
		left.BaselineAsset == right.BaselineAsset &&
		left.SourceSchemaAsset == right.SourceSchemaAsset &&
		slices.Equal(left.CoveredMigrations, right.CoveredMigrations)
}

// ValidateCurrentReleaseArtifact proves the release manifest, current fresh
// baseline, full source-schema fingerprint/verifier, and every executable
// migration against separately pinned digests. It performs no database access
// and is run before either bootstrap takes a lock or executes SQL.
func ValidateCurrentReleaseArtifact(release ReleaseManifest) error {
	entry, err := CurrentReleaseCatalogEntry()
	if err != nil {
		return fmt.Errorf("validate immutable release catalog: %w", err)
	}
	if release.ReleaseID != entry.ReleaseID || release.SchemaVersion != entry.SchemaVersion ||
		release.BaselineVersion != entry.BaselineVersion {
		return fmt.Errorf("immutable release catalog manifest identity mismatch")
	}
	if entry.BaselineAsset.Name != CurrentBaselineAssetName ||
		entry.BaselineAsset.SHA256 != checksumSQL(CurrentBaselineSQL()) {
		return fmt.Errorf("immutable release catalog baseline identity mismatch")
	}
	if _, err := loadCurrentCatalogFingerprintAsset(); err != nil {
		return fmt.Errorf("immutable release catalog source schema identity mismatch: %w", err)
	}
	if entry.SourceSchemaAsset != currentSourceSchemaAsset() {
		return fmt.Errorf("immutable release catalog source schema identity mismatch")
	}
	baselineMatches, sourceSchemaMatches := 0, 0
	for _, asset := range release.Assets {
		if asset.Name == entry.BaselineAsset.Name && asset.SHA256 == entry.BaselineAsset.SHA256 {
			baselineMatches++
		}
		if asset.Name == entry.SourceSchemaAsset.Name && asset.SHA256 == entry.SourceSchemaAsset.SHA256 {
			sourceSchemaMatches++
		}
	}
	if baselineMatches != 1 {
		return fmt.Errorf("immutable release catalog baseline asset manifest mismatch")
	}
	if sourceSchemaMatches != 1 {
		return fmt.Errorf("immutable release catalog source schema asset manifest mismatch")
	}
	checksum, err := release.Checksum()
	if err != nil {
		return fmt.Errorf("validate immutable release catalog manifest: %w", err)
	}
	if checksum != entry.ReleaseManifestSHA256 {
		return fmt.Errorf("immutable release catalog manifest identity mismatch")
	}
	if err := validateMigrationCatalog(RegisteredMigrations, LegacyMigrations, GetMigrationSQL); err != nil {
		return fmt.Errorf("validate immutable release catalog migration stream: %w", err)
	}
	registered := make(map[string]struct{}, len(RegisteredMigrations))
	for _, migration := range RegisteredMigrations {
		if err := validateActiveMigration(migration); err != nil {
			return fmt.Errorf("validate immutable release catalog migration: %w", err)
		}
		registered[migration.Version] = struct{}{}
	}
	for _, version := range entry.CoveredMigrations {
		if _, exists := registered[version]; !exists {
			return fmt.Errorf("immutable release catalog covered migration is unavailable")
		}
	}
	return nil
}

// ValidateCurrentReleasePublicationGate is deliberately separate from runtime
// artifact validation. The 028 boundary can be tested internally, but it must
// not be declared production-releasable until all allocated integration
// migrations named by this gate have been merged and a new immutable entry is
// published.
func ValidateCurrentReleasePublicationGate() error {
	catalog, err := loadReleaseCatalog()
	if err != nil {
		return err
	}
	if len(catalog.PublicationGate.MissingAllocatedMigrations) == 0 {
		return nil
	}
	return fmt.Errorf(
		"release publication gate is closed: missing allocated migrations %s",
		strings.Join(catalog.PublicationGate.MissingAllocatedMigrations, ", "),
	)
}

// PlanMigrationsByExplicitCoverage subtracts only migrations named by the
// baseline entry and committed ledger. Numeric comparison with a schema head
// is intentionally absent: later-published 023 is pending even behind 028.
func PlanMigrationsByExplicitCoverage(
	available []Migration,
	appliedVersions []string,
	coveredVersions []string,
) ([]Migration, error) {
	availableByVersion := make(map[string]struct{}, len(available))
	for _, migration := range available {
		if strings.TrimSpace(migration.Version) == "" {
			return nil, fmt.Errorf("available migration version is required")
		}
		if _, duplicate := availableByVersion[migration.Version]; duplicate {
			return nil, fmt.Errorf("duplicate available migration %q", migration.Version)
		}
		availableByVersion[migration.Version] = struct{}{}
	}
	covered := make(map[string]struct{}, len(coveredVersions))
	for _, version := range coveredVersions {
		if _, duplicate := covered[version]; duplicate {
			return nil, fmt.Errorf("duplicate covered migration %q", version)
		}
		if _, exists := availableByVersion[version]; !exists {
			return nil, fmt.Errorf("unknown covered migration %q", version)
		}
		covered[version] = struct{}{}
	}
	applied := make(map[string]struct{}, len(appliedVersions))
	for _, version := range appliedVersions {
		if _, duplicate := applied[version]; duplicate {
			return nil, fmt.Errorf("duplicate applied migration %q", version)
		}
		if _, exists := availableByVersion[version]; !exists {
			return nil, fmt.Errorf("unknown applied migration %q", version)
		}
		applied[version] = struct{}{}
	}
	pending := make([]Migration, 0, len(available))
	for _, migration := range available {
		_, isCovered := covered[migration.Version]
		_, isApplied := applied[migration.Version]
		if !isCovered && !isApplied {
			pending = append(pending, migration)
		}
	}
	return pending, nil
}
