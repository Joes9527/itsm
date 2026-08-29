package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// prepareIncidentProblemRelationMigration refuses the new partial unique index
// when existing live incident investigation relations violate its invariant.
func prepareIncidentProblemRelationMigration(ctx context.Context, db *sql.DB, logger *zap.SugaredLogger) error {
	if db == nil {
		return nil
	}

	tableExists, err := workItemRelationsTableExists(ctx, db)
	if err != nil {
		return fmt.Errorf("inspect work_item_relations table: %w", err)
	}
	if !tableExists {
		if logger != nil {
			logger.Debugw("incident/problem relation preflight skipped", "reason", "work_item_relations table does not exist")
		}
		return nil
	}

	groups, err := db.QueryContext(ctx, `
		SELECT tenant_id, source_work_item_id
		FROM work_item_relations
		WHERE deleted_at IS NULL AND relation_type = 'investigated_by'
		GROUP BY tenant_id, source_work_item_id
		HAVING COUNT(*) > 1
		ORDER BY tenant_id, source_work_item_id
	`)
	if err != nil {
		return fmt.Errorf("find duplicate live investigated_by relations: %w", err)
	}
	defer groups.Close()

	type duplicateGroup struct {
		tenantID int
		sourceID int
	}
	var duplicates []duplicateGroup
	for groups.Next() {
		var group duplicateGroup
		if err := groups.Scan(&group.tenantID, &group.sourceID); err != nil {
			return fmt.Errorf("scan duplicate live investigated_by relation group: %w", err)
		}
		duplicates = append(duplicates, group)
	}
	if err := groups.Err(); err != nil {
		return fmt.Errorf("iterate duplicate live investigated_by relation groups: %w", err)
	}
	if err := groups.Close(); err != nil {
		return fmt.Errorf("close duplicate live investigated_by relation groups: %w", err)
	}

	var conflicts []string
	for _, group := range duplicates {
		rows, err := db.QueryContext(ctx, `
			SELECT id, target_work_item_id
			FROM work_item_relations
			WHERE tenant_id = $1
			  AND source_work_item_id = $2
			  AND deleted_at IS NULL
			  AND relation_type = 'investigated_by'
			ORDER BY id
		`, group.tenantID, group.sourceID)
		if err != nil {
			return fmt.Errorf("find duplicate relation details for tenant_id=%d source_work_item_id=%d: %w", group.tenantID, group.sourceID, err)
		}

		var relationIDs, targetIDs []int
		for rows.Next() {
			var relationID, targetID int
			if err := rows.Scan(&relationID, &targetID); err != nil {
				rows.Close()
				return fmt.Errorf("scan duplicate relation details for tenant_id=%d source_work_item_id=%d: %w", group.tenantID, group.sourceID, err)
			}
			relationIDs = append(relationIDs, relationID)
			targetIDs = append(targetIDs, targetID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate duplicate relation details for tenant_id=%d source_work_item_id=%d: %w", group.tenantID, group.sourceID, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close duplicate relation details for tenant_id=%d source_work_item_id=%d: %w", group.tenantID, group.sourceID, err)
		}

		conflicts = append(conflicts, fmt.Sprintf(
			"tenant_id=%d source_work_item_id=%d target_work_item_ids=%v relation_ids=%v",
			group.tenantID,
			group.sourceID,
			targetIDs,
			relationIDs,
		))
	}
	if len(conflicts) > 0 {
		return fmt.Errorf(
			"duplicate live investigated_by relations must be resolved before schema migration: %s",
			strings.Join(conflicts, "; "),
		)
	}

	return nil
}

func workItemRelationsTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT 1 FROM work_item_relations LIMIT 0`)
	if err != nil {
		if isMissingTableError(err) {
			return false, nil
		}
		return false, err
	}
	return true, rows.Close()
}

type sqlStateError interface {
	SQLState() string
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}

	var stateErr sqlStateError
	if errors.As(err, &stateErr) && stateErr.SQLState() == "42P01" {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table: work_item_relations")
}
