package service

import (
	"context"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/tickettype"
)

func validateGenericSubtype(ctx context.Context, client *ent.Client, tenantID int, subtype string) error {
	switch subtype {
	case "", "ticket", "improvement":
		return nil
	}
	if client == nil {
		return fmt.Errorf("generic subtype configuration unavailable")
	}
	exists, err := client.TicketType.Query().Where(tickettype.CodeEQ(subtype), tickettype.TenantIDEQ(int64(tenantID)), tickettype.StatusEQ("active")).Exist(ctx)
	if err != nil {
		return fmt.Errorf("resolve generic subtype: %w", err)
	}
	if !exists {
		return fmt.Errorf("generic subtype is not configured")
	}
	return nil
}
