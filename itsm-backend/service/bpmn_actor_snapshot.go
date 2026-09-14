package service

import (
	"context"
	"fmt"

	"itsm-backend/database"
	"itsm-backend/ent"
)

// SetDirectorySnapshot supplies the existing restricted identity capability.
// Business transactions remain bound to their selected tenant.
func (e *CustomProcessEngine) SetDirectorySnapshot(directory database.DirectorySnapshot) {
	e.directory = directory
	e.participationResolver.directory = directory
}

func (e *CustomProcessEngine) requireActorSnapshot(ctx context.Context, tx *ent.Tx) error {
	if e.directory == nil {
		return nil
	}
	if tx == nil {
		return fmt.Errorf("BPMN actor requires an owning transaction")
	}
	rows, err := tx.QueryContext(ctx, "SHOW transaction_isolation")
	if err != nil {
		return err
	}
	defer rows.Close()
	var isolation string
	if !rows.Next() {
		return fmt.Errorf("BPMN transaction isolation unavailable")
	}
	if err = rows.Scan(&isolation); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if isolation != "repeatable read" && isolation != "serializable" {
		return fmt.Errorf("BPMN actor requires repeatable read or serializable owning transaction")
	}
	return rows.Close()
}
