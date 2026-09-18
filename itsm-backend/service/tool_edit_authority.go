package service

import (
	"context"
	"strconv"
	"strings"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

// Tool edits retain the approved target, version, fields and operation identity.
// Authority must be checked in the owning transaction, including receipt replay.
func requireToolEditAuthority(ctx context.Context, tx *ent.Tx, execution *database.ExecutionPolicy, command dto.TicketEditCommand) error {
	const prefix = "tool:update_ticket:"
	if command.Meta.Source != "ai_tool" && !strings.HasPrefix(command.Meta.OperationID, prefix) {
		return nil
	}
	denied := func() error { return creation.NewPermissionDenied("approved tool edit source is required", nil) }
	if command.Meta.Source != "ai_tool" || !strings.HasPrefix(command.Meta.OperationID, prefix) {
		return denied()
	}
	rawID := strings.TrimPrefix(command.Meta.OperationID, prefix)
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 || strconv.Itoa(id) != rawID {
		return denied()
	}
	inv, _, err := loadApprovedToolTx(ctx, tx, ToolJob{TenantID: command.Meta.TenantID, InvocationID: id}, execution)
	if err != nil {
		return err
	}
	if inv.ToolName != "update_ticket" || inv.UserID != command.Meta.ActorID {
		return denied()
	}
	expected, err := toolEditCommand(inv.Arguments, inv.ID, inv.UserID, inv.TenantID)
	if err != nil {
		return err
	}
	expectedDigest, err := workitemmutation.Digest(expected)
	if err != nil {
		return err
	}
	actualDigest, err := workitemmutation.Digest(command)
	if err != nil {
		return err
	}
	if expectedDigest != actualDigest {
		return denied()
	}
	return nil
}
