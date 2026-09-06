package service

import (
	"context"
	"fmt"
	"itsm-backend/ent/tickettype"
)

func (s *TicketService) validateGenericSubtype(ctx context.Context, tenantID int, subtype string) error {
	switch subtype {
	case "", "ticket", "improvement":
		return nil
	}
	if s.client == nil {
		return fmt.Errorf("generic subtype configuration unavailable")
	}
	exists, err := s.client.TicketType.Query().Where(tickettype.CodeEQ(subtype), tickettype.TenantIDEQ(int64(tenantID)), tickettype.StatusEQ("active")).Exist(ctx)
	if err != nil {
		return fmt.Errorf("resolve generic subtype: %w", err)
	}
	if !exists {
		return fmt.Errorf("generic subtype is not configured")
	}
	return nil
}
