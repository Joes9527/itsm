package service

import (
	"context"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/ticketnotification"
)

// Historical notification_sent is an immutable imported fact. Version 1 owns
// no second mutable sent flag: only linked delivery states determine the result.
func (s *SLAAlertService) projectAlertNotificationStatus(ctx context.Context, histories []*ent.SLAAlertHistory, tenantID int) (map[int]bool, error) {
	result := make(map[int]bool, len(histories))
	tracked := make(map[int]int)
	ids := make([]int, 0, len(histories))
	for _, history := range histories {
		if history.TenantID != tenantID {
			return nil, fmt.Errorf("SLA notification projection tenant mismatch")
		}
		if history.NotificationTrackingVersion == nil {
			result[history.ID] = history.NotificationSent
			continue
		}
		if *history.NotificationTrackingVersion != 1 {
			return nil, fmt.Errorf("unsupported SLA notification tracking version")
		}
		ids = append(ids, history.ID)
		tracked[history.ID] = history.TicketID
		result[history.ID] = false
	}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.client.TicketNotification.Query().Where(ticketnotification.TenantIDEQ(tenantID), ticketnotification.SLAAlertHistoryIDIn(ids...)).Select(ticketnotification.FieldSLAAlertHistoryID, ticketnotification.FieldTicketID, ticketnotification.FieldStatus).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("SLA notification delivery projection: %w", err)
	}
	seen := make(map[int]bool)
	for _, row := range rows {
		if row.SLAAlertHistoryID == nil {
			return nil, fmt.Errorf("missing SLA notification owner")
		}
		id := *row.SLAAlertHistoryID
		if tracked[id] != row.TicketID {
			return nil, fmt.Errorf("SLA notification projection WorkItem mismatch")
		}
		delivered := row.Status == "sent" || row.Status == "read"
		if !seen[id] {
			result[id] = delivered
			seen[id] = true
		} else {
			result[id] = result[id] && delivered
		}
	}
	return result, nil
}
