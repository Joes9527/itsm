package service

import (
	"context"
	"strconv"

	"itsm-backend/database"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// RequireToolCreationAuthority checks the exact approved command in the business
// transaction, before receipt allocation or any WorkItem write.
func RequireToolCreationAuthority(ctx context.Context, tx *ent.Tx, execution *database.ExecutionPolicy, identity creation.Identity, command creation.CreateWorkItemCommand) error {
	source := command.SourceReference
	isTool := identity.Channel == "ai_tool" || identity.Provider == "tool_queue" || (source != nil && source.Provider == "tool_queue")
	if !isTool {
		return nil
	}
	denied := func() error { return creation.NewPermissionDenied("approved tool creation source is required", nil) }
	if tx == nil || execution == nil || identity.Channel != "ai_tool" || identity.Provider != "tool_queue" || source == nil || source.Provider != "tool_queue" {
		return denied()
	}
	id, err := strconv.Atoi(source.EventID)
	if err != nil || id <= 0 || strconv.Itoa(id) != source.EventID {
		return denied()
	}
	inv, _, err := loadApprovedToolTx(ctx, tx, ToolJob{TenantID: identity.TenantID, InvocationID: id}, execution)
	if err != nil {
		return err
	}
	if inv.ToolName != "create_ticket" || inv.UserID != identity.ActorID {
		return denied()
	}
	expected, requester, err := toolCreationCommand(inv.Arguments, inv.ID, inv.UserID)
	if err != nil {
		return err
	}
	if requester != identity.RequesterID || command.IdempotencyKey != expected.IdempotencyKey {
		return denied()
	}
	_, expectedDigest, err := creation.CanonicalizeCommand(expected)
	if err != nil {
		return err
	}
	_, actualDigest, err := creation.CanonicalizeCommand(command)
	if err != nil {
		return err
	}
	if expectedDigest != actualDigest {
		return denied()
	}
	return nil
}
