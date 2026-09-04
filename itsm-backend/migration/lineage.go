package migration

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
)

const (
	immutableUpgradeLineage  = "immutable_upgrade_lineage"
	historicalValidationOnly = "historical_validation_only"
)

var (
	gitObjectHashPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)

	//go:embed lineage_manifest.json
	lineageManifestJSON []byte
)

// LineageManifest records the immutable provenance of published migrations.
type LineageManifest struct {
	Entries []LineageEntry `json:"entries"`
}

// LineageEntry identifies the only published checksum accepted for a ledger version.
type LineageEntry struct {
	LedgerVersion    string `json:"ledgerVersion"`
	LogicalFile      string `json:"logicalFile"`
	GitCommit        string `json:"gitCommit"`
	GitBlob          string `json:"gitBlob"`
	SQLSHA256        string `json:"sqlSha256"`
	Catalog          string `json:"catalog"`
	Executable       bool   `json:"executable"`
	ForwardMigration string `json:"forwardMigration"`
}

type encodedLineageManifest struct {
	Entries []encodedLineageEntry `json:"entries"`
}

type encodedLineageEntry struct {
	LedgerVersion    string  `json:"ledgerVersion"`
	LogicalFile      string  `json:"logicalFile"`
	GitCommit        string  `json:"gitCommit"`
	GitBlob          string  `json:"gitBlob"`
	SQLSHA256        string  `json:"sqlSha256"`
	Catalog          string  `json:"catalog"`
	Executable       *bool   `json:"executable"`
	ForwardMigration *string `json:"forwardMigration"`
}

// LoadLineageManifest loads and validates the embedded published lineage.
func LoadLineageManifest() (LineageManifest, error) {
	return parseLineageManifest(lineageManifestJSON)
}

func parseLineageManifest(data []byte) (LineageManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var encoded encodedLineageManifest
	if err := decoder.Decode(&encoded); err != nil {
		return LineageManifest{}, fmt.Errorf("decode lineage manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return LineageManifest{}, err
	}
	if len(encoded.Entries) == 0 {
		return LineageManifest{}, fmt.Errorf("lineage manifest must contain entries")
	}

	manifest := LineageManifest{Entries: make([]LineageEntry, 0, len(encoded.Entries))}
	seen := make(map[string]struct{}, len(encoded.Entries))
	for index, encodedEntry := range encoded.Entries {
		entry, err := validateLineageEntry(encodedEntry)
		if err != nil {
			return LineageManifest{}, fmt.Errorf("lineage entry %d: %w", index, err)
		}
		if _, duplicate := seen[entry.LedgerVersion]; duplicate {
			return LineageManifest{}, fmt.Errorf("duplicate lineage version %q", entry.LedgerVersion)
		}
		seen[entry.LedgerVersion] = struct{}{}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode lineage manifest: unexpected trailing JSON value")
		}
		return fmt.Errorf("decode lineage manifest: %w", err)
	}
	return nil
}

func validateLineageEntry(encoded encodedLineageEntry) (LineageEntry, error) {
	if encoded.LedgerVersion == "" || encoded.LogicalFile == "" || encoded.Catalog == "" ||
		encoded.Executable == nil || encoded.ForwardMigration == nil {
		return LineageEntry{}, fmt.Errorf("all lineage fields must be present")
	}
	if !gitObjectHashPattern.MatchString(encoded.GitCommit) {
		return LineageEntry{}, fmt.Errorf("gitCommit for %q must be a lowercase Git object hash", encoded.LedgerVersion)
	}
	if !gitObjectHashPattern.MatchString(encoded.GitBlob) {
		return LineageEntry{}, fmt.Errorf("gitBlob for %q must be a lowercase Git object hash", encoded.LedgerVersion)
	}
	if !sha256Pattern.MatchString(encoded.SQLSHA256) {
		return LineageEntry{}, fmt.Errorf("sqlSha256 for %q must be a lowercase SHA-256", encoded.LedgerVersion)
	}
	if encoded.Catalog != immutableUpgradeLineage && encoded.Catalog != historicalValidationOnly {
		return LineageEntry{}, fmt.Errorf("unknown lineage catalog %q", encoded.Catalog)
	}
	if encoded.Catalog == historicalValidationOnly && *encoded.Executable {
		return LineageEntry{}, fmt.Errorf("validation-only lineage %q cannot be executable", encoded.LedgerVersion)
	}

	return LineageEntry{
		LedgerVersion:    encoded.LedgerVersion,
		LogicalFile:      encoded.LogicalFile,
		GitCommit:        encoded.GitCommit,
		GitBlob:          encoded.GitBlob,
		SQLSHA256:        encoded.SQLSHA256,
		Catalog:          encoded.Catalog,
		Executable:       *encoded.Executable,
		ForwardMigration: *encoded.ForwardMigration,
	}, nil
}

// PublishedLineage returns the immutable lineage entry for a ledger version.
func PublishedLineage(version string) (LineageEntry, bool) {
	manifest, err := LoadLineageManifest()
	if err != nil {
		return LineageEntry{}, false
	}
	for _, entry := range manifest.Entries {
		if entry.LedgerVersion == version {
			return entry, true
		}
	}
	return LineageEntry{}, false
}

// ValidateLedgerLineage validates applied rows against their one published checksum.
func ValidateLedgerLineage(applied []Migration) error {
	manifest, err := LoadLineageManifest()
	if err != nil {
		return fmt.Errorf("load lineage manifest: %w", err)
	}
	entries := make(map[string]LineageEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries[entry.LedgerVersion] = entry
	}

	seen := make(map[string]struct{}, len(applied))
	for _, migration := range applied {
		entry, published := entries[migration.Version]
		if !published {
			return fmt.Errorf("migration ledger contains unknown version %q", migration.Version)
		}
		if _, duplicate := seen[migration.Version]; duplicate {
			return fmt.Errorf("migration ledger contains duplicate version %q", migration.Version)
		}
		if migration.Checksum != entry.SQLSHA256 {
			return fmt.Errorf("migration checksum mismatch for %s: applied=%s published=%s", migration.Version, migration.Checksum, entry.SQLSHA256)
		}
		seen[migration.Version] = struct{}{}
	}
	return nil
}
