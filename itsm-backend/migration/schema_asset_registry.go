package migration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// VerifierAssetFile is one checksum-addressed source or transition verifier
// asset. NewSchemaVerifierRegistry copies Content before retaining it.
type VerifierAssetFile struct {
	Name    string
	Content []byte
}

// SchemaVerifierRegistry retains all immutable verifier assets required to
// validate historical release sources and committed transition prefixes.
type SchemaVerifierRegistry struct {
	verifierSQL string
	files       []VerifierAssetFile
	sources     map[string]catalogFingerprintAsset
	transitions map[string]catalogTransitionAsset
}

const catalogTransitionFormatVersion = 1

// CatalogedMigration couples the ledger metadata with the exact SQL bytes
// whose checksum is pinned by a transition verifier asset.
type CatalogedMigration struct {
	Migration Migration
	SQL       string
	SHA256    string
}

type catalogTransitionAsset struct {
	Kind           string                       `json:"kind"`
	AssetID        string                       `json:"assetId"`
	FormatVersion  int                          `json:"formatVersion"`
	Source         catalogTransitionSource      `json:"source"`
	Target         catalogReleaseIdentity       `json:"target"`
	Verifier       ReleaseAsset                 `json:"verifier"`
	Platform       releasePlatformRequirement   `json:"platform"`
	ManagedSchemas []string                     `json:"managedSchemas"`
	Migrations     []catalogTransitionMigration `json:"migrations"`
}

type catalogTransitionSource struct {
	ReleaseID             string `json:"releaseId"`
	SchemaVersion         string `json:"schemaVersion"`
	BaselineVersion       string `json:"baselineVersion"`
	ReleaseManifestSHA256 string `json:"releaseManifestSha256"`
}

func (source catalogTransitionSource) valid() bool {
	return strings.TrimSpace(source.ReleaseID) != "" && strings.TrimSpace(source.SchemaVersion) != "" &&
		strings.TrimSpace(source.BaselineVersion) != "" && sha256Pattern.MatchString(source.ReleaseManifestSHA256)
}

func (source catalogTransitionSource) matches(entry ReleaseCatalogEntry) bool {
	return source.ReleaseID == entry.ReleaseID && source.SchemaVersion == entry.SchemaVersion &&
		source.BaselineVersion == entry.BaselineVersion && source.ReleaseManifestSHA256 == entry.ReleaseManifestSHA256
}

type catalogTransitionMigration struct {
	Version     string                     `json:"version"`
	SQLSHA256   string                     `json:"sqlSha256"`
	Fingerprint string                     `json:"fingerprint"`
	Extensions  []catalogExtensionIdentity `json:"extensions"`
}

type catalogTransitionPlan struct {
	ExpectedFingerprint string
	ExpectedExtensions  []catalogExtensionIdentity
	ExpectedPlatform    releasePlatformRequirement
	ManagedSchemas      []string
	Pending             []CatalogedMigration
}

type verifierAssetEnvelope struct {
	Kind string `json:"kind"`
}

func schemaVerifierRegistryKey(ref ReleaseAsset) string {
	return ref.Name + "\x00" + ref.SHA256
}

// NewSchemaVerifierRegistry validates and indexes an immutable set of files by
// logical asset ID and exact content checksum.
func NewSchemaVerifierRegistry(verifierSQL string, files []VerifierAssetFile) (*SchemaVerifierRegistry, error) {
	if err := validateCatalogVerifierSQL(verifierSQL); err != nil {
		return nil, err
	}
	registry := &SchemaVerifierRegistry{
		verifierSQL: verifierSQL,
		files:       make([]VerifierAssetFile, 0, len(files)),
		sources:     make(map[string]catalogFingerprintAsset),
		transitions: make(map[string]catalogTransitionAsset),
	}
	seenNames := make(map[string]struct{}, len(files))
	for index, file := range files {
		name := strings.TrimSpace(file.Name)
		if name == "" || len(file.Content) == 0 {
			return nil, fmt.Errorf("schema verifier asset %d is incomplete", index)
		}
		if _, duplicate := seenNames[name]; duplicate {
			return nil, fmt.Errorf("duplicate schema verifier asset ID %q", name)
		}
		seenNames[name] = struct{}{}
		content := append([]byte(nil), file.Content...)
		registry.files = append(registry.files, VerifierAssetFile{Name: name, Content: append([]byte(nil), content...)})
		var envelope verifierAssetEnvelope
		if err := json.Unmarshal(content, &envelope); err != nil {
			return nil, fmt.Errorf("decode schema verifier asset %q: %w", name, err)
		}
		ref := ReleaseAsset{Name: name, SHA256: checksumSQL(string(content))}
		switch envelope.Kind {
		case "source":
			asset, err := decodeSourceSchemaVerifierAsset(content, name, verifierSQL)
			if err != nil {
				return nil, err
			}
			registry.sources[schemaVerifierRegistryKey(ref)] = asset
		case "transition":
			asset, err := decodeTransitionSchemaVerifierAsset(content, name, verifierSQL)
			if err != nil {
				return nil, err
			}
			registry.transitions[schemaVerifierRegistryKey(ref)] = asset
		default:
			return nil, fmt.Errorf("schema verifier asset %q has unsupported kind", name)
		}
	}
	if len(registry.sources) == 0 {
		return nil, fmt.Errorf("schema verifier registry requires a source asset")
	}
	return registry, nil
}

// WithAssets returns a new immutable registry containing retained historical
// assets plus additional release assets. An existing asset ID cannot be
// replaced with different bytes.
func (registry *SchemaVerifierRegistry) WithAssets(files []VerifierAssetFile) (*SchemaVerifierRegistry, error) {
	if registry == nil {
		return nil, fmt.Errorf("schema verifier registry is required")
	}
	combined := make([]VerifierAssetFile, 0, len(registry.files)+len(files))
	for _, file := range registry.files {
		combined = append(combined, VerifierAssetFile{Name: file.Name, Content: append([]byte(nil), file.Content...)})
	}
	for _, file := range files {
		combined = append(combined, VerifierAssetFile{Name: file.Name, Content: append([]byte(nil), file.Content...)})
	}
	return NewSchemaVerifierRegistry(registry.verifierSQL, combined)
}

func validateCatalogVerifierSQL(verifierSQL string) error {
	normalized := strings.ToUpper(strings.TrimSpace(verifierSQL))
	if !strings.HasPrefix(normalized, "WITH ") || strings.Contains(normalized, ";") {
		return fmt.Errorf("catalog verifier must be one read-only WITH statement")
	}
	for _, mutation := range []string{
		" INSERT ", " UPDATE ", " DELETE ", " CREATE ", " ALTER ", " DROP ",
		" GRANT ", " REVOKE ", " TRUNCATE ", " CALL ",
	} {
		if strings.Contains(" "+normalized+" ", mutation) {
			return fmt.Errorf("catalog verifier must be read-only")
		}
	}
	return nil
}

func decodeSourceSchemaVerifierAsset(content []byte, name, verifierSQL string) (catalogFingerprintAsset, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var asset catalogFingerprintAsset
	if err := decoder.Decode(&asset); err != nil {
		return catalogFingerprintAsset{}, fmt.Errorf("decode source schema verifier asset %q: %w", name, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return catalogFingerprintAsset{}, fmt.Errorf("decode source schema verifier asset %q: %w", name, err)
	}
	if err := validateSourceSchemaVerifierAsset(asset, name, verifierSQL); err != nil {
		return catalogFingerprintAsset{}, err
	}
	return asset, nil
}

func validateSourceSchemaVerifierAsset(asset catalogFingerprintAsset, name, verifierSQL string) error {
	if asset.Kind != "source" || asset.AssetID != name || asset.FormatVersion != catalogFingerprintFormatVersion ||
		!asset.Release.valid() {
		return fmt.Errorf("source schema verifier asset %q identity mismatch", name)
	}
	if asset.Verifier.Name != catalogFingerprintVerifierName ||
		asset.Verifier.SHA256 != checksumSQL(verifierSQL) {
		return fmt.Errorf("source schema verifier asset %q query identity mismatch", name)
	}
	if asset.Platform.PostgresMajor <= 0 || strings.TrimSpace(asset.Platform.VectorVersion) == "" {
		return fmt.Errorf("source schema verifier asset %q platform is invalid", name)
	}
	if len(asset.ManagedSchemas) != 1 || asset.ManagedSchemas[0] != "public" {
		return fmt.Errorf("source schema verifier asset %q managed schemas are invalid", name)
	}
	for _, digest := range []string{
		asset.Phases.Empty,
		asset.Phases.Prepared,
		asset.Phases.EntSchema,
		asset.Phases.CurrentRelease,
	} {
		if !sha256Pattern.MatchString(digest) {
			return fmt.Errorf("source schema verifier asset %q phase fingerprint is invalid", name)
		}
	}
	for inventoryName, inventory := range map[string][]catalogExtensionIdentity{
		"empty": asset.Extensions.Empty, "installed": asset.Extensions.Installed,
	} {
		if err := validateExtensionInventory(inventory); err != nil {
			return fmt.Errorf("source schema verifier asset %q %s extensions are invalid", name, inventoryName)
		}
	}
	return nil
}

func validateExtensionInventory(inventory []catalogExtensionIdentity) error {
	if len(inventory) == 0 {
		return fmt.Errorf("extension inventory is required")
	}
	previous := ""
	for _, extension := range inventory {
		if strings.TrimSpace(extension.Name) == "" || strings.TrimSpace(extension.Schema) == "" ||
			strings.TrimSpace(extension.Version) == "" || extension.Name <= previous {
			return fmt.Errorf("extension inventory is invalid")
		}
		previous = extension.Name
	}
	return nil
}

func (registry *SchemaVerifierRegistry) loadSource(ref ReleaseAsset) (catalogFingerprintAsset, error) {
	if registry == nil || !sha256Pattern.MatchString(ref.SHA256) || strings.TrimSpace(ref.Name) == "" {
		return catalogFingerprintAsset{}, fmt.Errorf("source schema verifier reference is invalid")
	}
	asset, ok := registry.sources[schemaVerifierRegistryKey(ref)]
	if !ok {
		return catalogFingerprintAsset{}, fmt.Errorf("source schema verifier asset is not registered")
	}
	return asset, nil
}

func verifyReleaseCatalogVerifierAssets(entries []ReleaseCatalogEntry, registry *SchemaVerifierRegistry) error {
	if registry == nil {
		return fmt.Errorf("schema verifier registry is required")
	}
	for _, entry := range entries {
		asset, err := registry.loadSource(entry.SourceSchemaAsset)
		if err != nil || !asset.Release.matches(entry) {
			return fmt.Errorf("release catalog source schema verifier identity mismatch")
		}
		for _, ref := range entry.TransitionAssets {
			transition, err := registry.loadTransition(ref)
			if err != nil || !transition.Target.matches(entry) {
				return fmt.Errorf("release catalog transition verifier identity mismatch")
			}
			sourceFound := false
			for _, candidate := range entries {
				if transition.Source.matches(candidate) {
					sourceFound = true
					break
				}
			}
			if !sourceFound {
				return fmt.Errorf("release catalog transition source is not retained")
			}
			targetSource, err := registry.loadSource(entry.SourceSchemaAsset)
			if err != nil || len(transition.Migrations) == 0 {
				return fmt.Errorf("release catalog transition target verifier identity mismatch")
			}
			final := transition.Migrations[len(transition.Migrations)-1]
			if final.Fingerprint != targetSource.Phases.CurrentRelease ||
				!equalExtensionInventory(final.Extensions, targetSource.Extensions.Installed) ||
				transition.Platform != targetSource.Platform {
				return fmt.Errorf("release catalog transition target verifier identity mismatch")
			}
		}
	}
	return nil
}

func decodeTransitionSchemaVerifierAsset(content []byte, name, verifierSQL string) (catalogTransitionAsset, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var asset catalogTransitionAsset
	if err := decoder.Decode(&asset); err != nil {
		return catalogTransitionAsset{}, fmt.Errorf("decode transition schema verifier asset %q: %w", name, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return catalogTransitionAsset{}, fmt.Errorf("decode transition schema verifier asset %q: %w", name, err)
	}
	if asset.Kind != "transition" || asset.AssetID != name ||
		asset.FormatVersion != catalogTransitionFormatVersion || !asset.Source.valid() || !asset.Target.valid() ||
		(asset.Source.ReleaseID == asset.Target.ReleaseID &&
			asset.Source.SchemaVersion == asset.Target.SchemaVersion &&
			asset.Source.BaselineVersion == asset.Target.BaselineVersion) {
		return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q identity mismatch", name)
	}
	if asset.Verifier.Name != catalogFingerprintVerifierName ||
		asset.Verifier.SHA256 != checksumSQL(verifierSQL) {
		return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q query identity mismatch", name)
	}
	if asset.Platform.PostgresMajor <= 0 || strings.TrimSpace(asset.Platform.VectorVersion) == "" {
		return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q platform is invalid", name)
	}
	if len(asset.ManagedSchemas) != 1 || asset.ManagedSchemas[0] != "public" || len(asset.Migrations) == 0 {
		return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q inventory is invalid", name)
	}
	seenVersions := make(map[string]struct{}, len(asset.Migrations))
	for _, migration := range asset.Migrations {
		if strings.TrimSpace(migration.Version) == "" || !sha256Pattern.MatchString(migration.SQLSHA256) ||
			!sha256Pattern.MatchString(migration.Fingerprint) {
			return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q migration is invalid", name)
		}
		if _, duplicate := seenVersions[migration.Version]; duplicate {
			return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q migration is duplicated", name)
		}
		seenVersions[migration.Version] = struct{}{}
		if err := validateExtensionInventory(migration.Extensions); err != nil {
			return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset %q extensions are invalid", name)
		}
	}
	return asset, nil
}

func (registry *SchemaVerifierRegistry) loadTransition(ref ReleaseAsset) (catalogTransitionAsset, error) {
	if registry == nil {
		return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier registry is required")
	}
	asset, ok := registry.transitions[schemaVerifierRegistryKey(ref)]
	if !ok {
		return catalogTransitionAsset{}, fmt.Errorf("transition schema verifier asset is not registered")
	}
	return asset, nil
}

func equalExtensionInventory(left, right []catalogExtensionIdentity) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameCatalogReleaseIdentity(left, right ReleaseCatalogEntry) bool {
	return left.ReleaseID == right.ReleaseID && left.SchemaVersion == right.SchemaVersion &&
		left.BaselineVersion == right.BaselineVersion &&
		left.ReleaseManifestSHA256 == right.ReleaseManifestSHA256
}

func (registry *SchemaVerifierRegistry) planTransition(
	source ReleaseCatalogEntry,
	target ReleaseCatalogEntry,
	applied []Migration,
	available []CatalogedMigration,
) (catalogTransitionPlan, error) {
	sourceAsset, err := registry.loadSource(source.SourceSchemaAsset)
	if err != nil || !sourceAsset.Release.matches(source) {
		return catalogTransitionPlan{}, fmt.Errorf("upgrade source verifier is not registered")
	}
	targetAsset, err := registry.loadSource(target.SourceSchemaAsset)
	if err != nil || !targetAsset.Release.matches(target) {
		return catalogTransitionPlan{}, fmt.Errorf("upgrade target verifier is not registered")
	}

	covered := relationSet(source.CoveredMigrations...)
	committed := make(map[string]Migration, len(applied))
	for _, item := range applied {
		if _, duplicate := committed[item.Version]; duplicate {
			return catalogTransitionPlan{}, fmt.Errorf("migration ledger contains duplicate version %q", item.Version)
		}
		committed[item.Version] = item
	}

	if sameCatalogReleaseIdentity(source, target) {
		for version := range committed {
			if _, baselineCovered := covered[version]; !baselineCovered {
				return catalogTransitionPlan{}, fmt.Errorf("migration ledger contains a transition outside the cataloged release")
			}
		}
		return catalogTransitionPlan{
			ExpectedFingerprint: sourceAsset.Phases.CurrentRelease,
			ExpectedExtensions:  append([]catalogExtensionIdentity(nil), sourceAsset.Extensions.Installed...),
			ExpectedPlatform:    sourceAsset.Platform,
			ManagedSchemas:      append([]string(nil), sourceAsset.ManagedSchemas...),
		}, nil
	}

	var transition *catalogTransitionAsset
	for _, ref := range target.TransitionAssets {
		candidate, loadErr := registry.loadTransition(ref)
		if loadErr != nil {
			return catalogTransitionPlan{}, loadErr
		}
		if candidate.Source.matches(source) && candidate.Target.matches(target) {
			if transition != nil {
				return catalogTransitionPlan{}, fmt.Errorf("multiple transition verifiers match the upgrade releases")
			}
			candidateCopy := candidate
			transition = &candidateCopy
		}
	}
	if transition == nil {
		return catalogTransitionPlan{}, fmt.Errorf("no transition verifier matches the exact upgrade releases")
	}
	final := transition.Migrations[len(transition.Migrations)-1]
	if final.Fingerprint != targetAsset.Phases.CurrentRelease ||
		!equalExtensionInventory(final.Extensions, targetAsset.Extensions.Installed) ||
		transition.Platform != targetAsset.Platform {
		return catalogTransitionPlan{}, fmt.Errorf("transition verifier does not converge on the target release")
	}

	availableByVersion := make(map[string]CatalogedMigration, len(available))
	for _, item := range available {
		version := strings.TrimSpace(item.Migration.Version)
		if version == "" || strings.TrimSpace(item.Migration.Description) == "" {
			return catalogTransitionPlan{}, fmt.Errorf("cataloged migration is incomplete")
		}
		if _, duplicate := availableByVersion[version]; duplicate {
			return catalogTransitionPlan{}, fmt.Errorf("duplicate cataloged migration %q", version)
		}
		availableByVersion[version] = item
	}
	transitionVersions := make(map[string]struct{}, len(transition.Migrations))
	for _, expected := range transition.Migrations {
		item, exists := availableByVersion[expected.Version]
		if !exists || checksumSQL(item.SQL) != expected.SQLSHA256 {
			return catalogTransitionPlan{}, fmt.Errorf("transition migration %q checksum does not match the executable catalog", expected.Version)
		}
		transitionVersions[expected.Version] = struct{}{}
	}
	for version := range committed {
		if _, baselineCovered := covered[version]; baselineCovered {
			continue
		}
		if _, inTransition := transitionVersions[version]; !inTransition {
			return catalogTransitionPlan{}, fmt.Errorf("migration ledger contains a transition outside the exact cataloged sequence")
		}
	}

	prefixLength := 0
	gap := false
	for index, expected := range transition.Migrations {
		item, exists := committed[expected.Version]
		if !exists {
			gap = true
			continue
		}
		if item.Checksum != expected.SQLSHA256 {
			return catalogTransitionPlan{}, fmt.Errorf("committed transition migration %q checksum mismatch", expected.Version)
		}
		if gap {
			return catalogTransitionPlan{}, fmt.Errorf("committed transition is not a continuous prefix")
		}
		prefixLength = index + 1
	}
	observedPrefix := make([]string, 0, prefixLength)
	for _, item := range applied {
		if _, baselineCovered := covered[item.Version]; baselineCovered {
			continue
		}
		observedPrefix = append(observedPrefix, item.Version)
	}
	for index, version := range observedPrefix {
		if index >= prefixLength || version != transition.Migrations[index].Version {
			return catalogTransitionPlan{}, fmt.Errorf("committed transition does not match the exact sequence")
		}
	}

	plan := catalogTransitionPlan{
		ExpectedFingerprint: sourceAsset.Phases.CurrentRelease,
		ExpectedExtensions:  append([]catalogExtensionIdentity(nil), sourceAsset.Extensions.Installed...),
		ExpectedPlatform:    sourceAsset.Platform,
		ManagedSchemas:      append([]string(nil), sourceAsset.ManagedSchemas...),
	}
	if prefixLength > 0 {
		prefix := transition.Migrations[prefixLength-1]
		plan.ExpectedFingerprint = prefix.Fingerprint
		plan.ExpectedExtensions = append([]catalogExtensionIdentity(nil), prefix.Extensions...)
		plan.ExpectedPlatform = transition.Platform
		plan.ManagedSchemas = append([]string(nil), transition.ManagedSchemas...)
	}
	for _, expected := range transition.Migrations[prefixLength:] {
		plan.Pending = append(plan.Pending, availableByVersion[expected.Version])
	}
	return plan, nil
}
