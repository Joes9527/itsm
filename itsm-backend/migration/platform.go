package migration

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lib/pq"
)

type releasePlatformRequirement struct {
	PostgresMajor int    `json:"postgresMajor"`
	VectorVersion string `json:"vectorVersion"`
}

type platformSnapshot struct {
	PostgresMajor           int
	AvailableVectorVersions []string
	InstalledVectorVersion  string
}

func validateReleasePlatform(actual platformSnapshot, required releasePlatformRequirement, requireInstalled bool) error {
	if required.PostgresMajor <= 0 || strings.TrimSpace(required.VectorVersion) == "" {
		return fmt.Errorf("release platform requirement is invalid")
	}
	if actual.PostgresMajor != required.PostgresMajor ||
		!slices.Contains(actual.AvailableVectorVersions, required.VectorVersion) ||
		(actual.InstalledVectorVersion != "" && actual.InstalledVectorVersion != required.VectorVersion) ||
		(requireInstalled && actual.InstalledVectorVersion != required.VectorVersion) {
		return fmt.Errorf(
			"unsupported PostgreSQL platform: release requires PostgreSQL major %d and pgvector %s",
			required.PostgresMajor,
			required.VectorVersion,
		)
	}
	return nil
}

// verifyReleasePlatform is the read-only platform gate shared by fresh and
// upgrade planning. It validates server compatibility and extension package
// availability before a bootstrap can execute schema SQL.
func verifyReleasePlatform(
	ctx context.Context,
	db DBTX,
	required releasePlatformRequirement,
	requireInstalled bool,
) error {
	if db == nil {
		return fmt.Errorf("release platform database is required")
	}
	var serverVersionNumber int
	var available pq.StringArray
	var installed string
	if err := db.QueryRowContext(ctx, `
		SELECT current_setting('server_version_num')::integer,
		       COALESCE((
		           SELECT array_agg(version ORDER BY version)
		           FROM pg_available_extension_versions
		           WHERE name = 'vector'
		       ), ARRAY[]::text[]),
		       COALESCE((
		           SELECT extversion FROM pg_extension WHERE extname = 'vector'
		       ), '')
	`).Scan(&serverVersionNumber, &available, &installed); err != nil {
		return fmt.Errorf("inspect PostgreSQL platform: %w", err)
	}
	return validateReleasePlatform(platformSnapshot{
		PostgresMajor:           serverVersionNumber / 10000,
		AvailableVectorVersions: append([]string(nil), available...),
		InstalledVectorVersion:  installed,
	}, required, requireInstalled)
}

// VerifyCurrentReleasePlatformAvailability performs the current release's
// read-only server-major and exact pgvector package availability preflight.
// It does not require the extension to be installed yet.
func VerifyCurrentReleasePlatformAvailability(ctx context.Context, db DBTX) error {
	asset, err := loadCurrentCatalogFingerprintAsset()
	if err != nil {
		return fmt.Errorf("load current release platform requirement: %w", err)
	}
	return verifyReleasePlatform(ctx, db, asset.Platform, false)
}
