package migration

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DBTX is implemented by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// SchemaState is the single authoritative database release marker.
type SchemaState struct {
	ID                      int16
	ReleaseID               string
	SchemaVersion           string
	BaselineVersion         string
	ReleaseManifestChecksum string
	UpdatedAt               time.Time
}

// ReadSchemaState reads the only valid schema-state row.
func ReadSchemaState(ctx context.Context, db DBTX) (SchemaState, error) {
	if db == nil {
		return SchemaState{}, fmt.Errorf("schema state store is required")
	}
	var state SchemaState
	var updatedAt any
	err := db.QueryRowContext(ctx, `
		SELECT id, release_id, schema_version, baseline_version,
		       release_manifest_checksum, updated_at
		FROM schema_state
		WHERE id = 1
	`).Scan(
		&state.ID,
		&state.ReleaseID,
		&state.SchemaVersion,
		&state.BaselineVersion,
		&state.ReleaseManifestChecksum,
		&updatedAt,
	)
	if err != nil {
		return SchemaState{}, fmt.Errorf("read schema state: %w", err)
	}
	state.UpdatedAt, err = normalizeSchemaStateTime(updatedAt)
	if err != nil {
		return SchemaState{}, err
	}
	return state, nil
}

func normalizeSchemaStateTime(value any) (time.Time, error) {
	switch value := value.(type) {
	case time.Time:
		return value, nil
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05"} {
			parsed, err := time.Parse(layout, value)
			if err == nil {
				return parsed, nil
			}
		}
	case []byte:
		return normalizeSchemaStateTime(string(value))
	}
	return time.Time{}, fmt.Errorf("read schema state: invalid update timestamp")
}

// PromoteSchemaState atomically inserts or replaces the singleton marker with
// a checksum recomputed from the supplied, validated release manifest.
func PromoteSchemaState(ctx context.Context, db DBTX, release ReleaseManifest) error {
	if db == nil {
		return fmt.Errorf("schema state store is required")
	}
	checksum, err := release.Checksum()
	if err != nil {
		return fmt.Errorf("validate release manifest for schema promotion: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO schema_state
			(id, release_id, schema_version, baseline_version, release_manifest_checksum)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			release_id = EXCLUDED.release_id,
			schema_version = EXCLUDED.schema_version,
			baseline_version = EXCLUDED.baseline_version,
			release_manifest_checksum = EXCLUDED.release_manifest_checksum,
			updated_at = CURRENT_TIMESTAMP
	`, release.ReleaseID, release.SchemaVersion, release.BaselineVersion, checksum)
	if err != nil {
		return fmt.Errorf("promote schema state: %w", err)
	}
	return nil
}

// VerifySchemaState requires an exact match to the supplied release identity.
func VerifySchemaState(state SchemaState, release ReleaseManifest) error {
	checksum, err := release.Checksum()
	if err != nil {
		return fmt.Errorf("validate required release manifest: %w", err)
	}
	if state.ID != 1 {
		return fmt.Errorf("schema state singleton mismatch")
	}
	if state.ReleaseID != release.ReleaseID {
		return fmt.Errorf("schema state release mismatch")
	}
	if state.SchemaVersion != release.SchemaVersion {
		return fmt.Errorf("schema state version mismatch")
	}
	if state.BaselineVersion != release.BaselineVersion {
		return fmt.Errorf("schema state baseline mismatch")
	}
	if state.ReleaseManifestChecksum != checksum {
		return fmt.Errorf("schema state manifest checksum mismatch")
	}
	return nil
}
