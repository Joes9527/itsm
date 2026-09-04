package migration

import (
	"context"
	"fmt"
)

// VerifyCatalogedUpgradeSourceCompatibility is the frozen, read-only safety
// preflight for a cataloged source that is older than the target release. It
// checks the concrete legacy shapes known to make forward-only planning unsafe;
// target-release invariants still run after all pending migrations and before
// privileges or promotion.
func VerifyCatalogedUpgradeSourceCompatibility(
	ctx context.Context,
	db DBTX,
	entry ReleaseCatalogEntry,
) error {
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
	if err != nil || !sameReleaseCatalogEntry(resolved, entry) ||
		entry.SchemaVersion != minimumSupportedUpgradeSchemaVersion {
		return fmt.Errorf("unsupported upgrade source: no compatibility verifier is registered")
	}
	var ticketIndexCount, roleTenantColumnCount int64
	if err := db.QueryRowContext(ctx, `
		/* cataloged_028_upgrade_source_compatibility */
		WITH ticket_index AS (
			SELECT index_record.indexrelid
			FROM pg_index index_record
			JOIN pg_class index_relation ON index_relation.oid = index_record.indexrelid
			JOIN pg_class table_relation ON table_relation.oid = index_record.indrelid
			JOIN pg_namespace namespace ON namespace.oid = table_relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND table_relation.relname = 'ticket_ccs'
			  AND index_relation.relname = 'ticketcc_tenant_id_ticket_id_user_id'
			  AND index_record.indisunique
			  AND index_record.indisvalid
			  AND index_record.indisready
			  AND index_record.indexprs IS NULL
			  AND regexp_replace(pg_get_expr(index_record.indpred, index_record.indrelid), '[()[:space:]]', '', 'g') = 'is_active'
			  AND ARRAY(
				SELECT attribute.attname::text
				FROM unnest(index_record.indkey) WITH ORDINALITY key_column(attnum, ordinal)
				JOIN pg_attribute attribute
				  ON attribute.attrelid = table_relation.oid
				 AND attribute.attnum = key_column.attnum
				ORDER BY key_column.ordinal
			  ) = ARRAY['tenant_id', 'ticket_id', 'user_id']::text[]
		), role_tenant_column AS (
			SELECT attribute.attnum
			FROM pg_attribute attribute
			JOIN pg_class relation ON relation.oid = attribute.attrelid
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND relation.relname = 'role_permissions'
			  AND relation.relkind IN ('r', 'p')
			  AND attribute.attname = 'tenant_id'
			  AND format_type(attribute.atttypid, attribute.atttypmod) = 'bigint'
			  AND attribute.attnotnull
			  AND NOT attribute.attisdropped
		)
		SELECT (SELECT COUNT(*) FROM ticket_index),
		       (SELECT COUNT(*) FROM role_tenant_column)
	`).Scan(&ticketIndexCount, &roleTenantColumnCount); err != nil {
		return fmt.Errorf("inspect cataloged upgrade source category")
	}
	if ticketIndexCount != 1 || roleTenantColumnCount != 1 {
		return fmt.Errorf("unsupported upgrade source: schema does not match cataloged release %s", entry.SchemaVersion)
	}
	return nil
}
