package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TicketLifecycleService remains the sole owner of generic Ticket lifecycle tests.
// The obsolete TicketCoreService suites were removed with that parallel service.
func TestTicketLifecycleService_StatusTransitions_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		targetStatus string
		setup        func(t *testing.T, ctx context.Context, client *ent.Client, tenantID, userID int) *ent.Ticket
		wantError    bool
		checkResult  func(t *testing.T, ticket *ent.Ticket)
	}{
		{
			name:         "open -> resolved",
			targetStatus: "resolved",
			setup: func(t *testing.T, ctx context.Context, client *ent.Client, tenantID, userID int) *ent.Ticket {
				ticketEntity, err := client.Ticket.Create().
					SetTitle("Test Ticket").
					SetDescription("Desc").
					SetStatus("open").
					SetPriority("medium").
					SetTicketNumber("TKT-001").
					SetRequesterID(userID).
					SetTenantID(tenantID).
					Save(ctx)
				require.NoError(t, err)
				return ticketEntity
			},
			checkResult: func(t *testing.T, ticketEntity *ent.Ticket) {
				assert.Equal(t, "resolved", ticketEntity.Status)
			},
		},
		{
			name:         "open -> closed is rejected",
			targetStatus: "closed",
			setup: func(t *testing.T, ctx context.Context, client *ent.Client, tenantID, userID int) *ent.Ticket {
				ticketEntity, err := client.Ticket.Create().
					SetTitle("Test Ticket").
					SetDescription("Desc").
					SetStatus("open").
					SetPriority("medium").
					SetTicketNumber("TKT-002").
					SetRequesterID(userID).
					SetTenantID(tenantID).
					Save(ctx)
				require.NoError(t, err)
				return ticketEntity
			},
			wantError: true,
		},
		{
			name:         "resolved -> closed",
			targetStatus: "closed",
			setup: func(t *testing.T, ctx context.Context, client *ent.Client, tenantID, userID int) *ent.Ticket {
				ticketEntity, err := client.Ticket.Create().
					SetTitle("Test Ticket").
					SetDescription("Desc").
					SetStatus("resolved").
					SetPriority("medium").
					SetTicketNumber("TKT-003").
					SetRequesterID(userID).
					SetTenantID(tenantID).
					SetResolution("已解决").
					Save(ctx)
				require.NoError(t, err)
				return ticketEntity
			},
			checkResult: func(t *testing.T, ticketEntity *ent.Ticket) {
				assert.Equal(t, "closed", ticketEntity.Status)
			},
		},
		{
			name:         "closed -> open is rejected",
			targetStatus: "open",
			setup: func(t *testing.T, ctx context.Context, client *ent.Client, tenantID, userID int) *ent.Ticket {
				ticketEntity, err := client.Ticket.Create().
					SetTitle("Test Ticket").
					SetDescription("Desc").
					SetStatus("closed").
					SetPriority("medium").
					SetTicketNumber("TKT-004").
					SetRequesterID(userID).
					SetTenantID(tenantID).
					Save(ctx)
				require.NoError(t, err)
				return ticketEntity
			},
			wantError: true,
		},
		{
			name:         "open -> cancelled",
			targetStatus: "cancelled",
			setup: func(t *testing.T, ctx context.Context, client *ent.Client, tenantID, userID int) *ent.Ticket {
				ticketEntity, err := client.Ticket.Create().
					SetTitle("Test Ticket").
					SetDescription("Desc").
					SetStatus("open").
					SetPriority("low").
					SetTicketNumber("TKT-005").
					SetRequesterID(userID).
					SetTenantID(tenantID).
					Save(ctx)
				require.NoError(t, err)
				return ticketEntity
			},
			checkResult: func(t *testing.T, ticketEntity *ent.Ticket) {
				assert.Equal(t, "cancelled", ticketEntity.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := enttest.Open(t, "sqlite3", testDSN())
			defer client.Close()

			lifecycleService := NewTicketLifecycleService(client, zaptest.NewLogger(t).Sugar())
			ctx := context.Background()
			tenant := createTicketLifecycleTestTenant(t, ctx, client, tt.name)
			user := createTicketLifecycleTestUser(t, ctx, client, tenant.ID, "lifecycle_user")
			ticketEntity := tt.setup(t, ctx, client, tenant.ID, user.ID)

			var err error
			switch tt.targetStatus {
			case "resolved":
				_, err = lifecycleService.ResolveTicket(ctx, ticketEntity.ID, "测试解决", tenant.ID, user.ID)
			case "closed":
				_, err = lifecycleService.CloseTicket(ctx, ticketEntity.ID, "测试关闭", tenant.ID, user.ID)
			case "cancelled", "open", "in_progress":
				_, err = lifecycleService.UpdateTicketStatus(ctx, ticketEntity.ID, tt.targetStatus, tenant.ID, user.ID)
			}

			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			updated, err := client.Ticket.Get(ctx, ticketEntity.ID)
			require.NoError(t, err)
			if tt.checkResult != nil {
				tt.checkResult(t, updated)
			}
		})
	}
}

func TestTicketLifecycleService_IsValidStatusTransition_TableDriven(t *testing.T) {
	tests := []struct {
		name          string
		currentStatus string
		newStatus     string
		want          bool
	}{
		{"open to in_progress", "open", "in_progress", true},
		{"open to closed", "open", "closed", false},
		{"open to cancelled", "open", "cancelled", true},
		{"in_progress to resolved", "in_progress", "resolved", true},
		{"in_progress to open", "in_progress", "open", false},
		{"resolved to closed", "resolved", "closed", true},
		{"resolved to open", "resolved", "open", true},
		{"resolved to in_progress", "resolved", "in_progress", true},
		{"closed to open", "closed", "open", false},
		{"closed to in_progress", "closed", "in_progress", false},
		{"cancelled to open", "cancelled", "open", false},
		{"cancelled to resolved", "cancelled", "resolved", false},
		{"open to resolved", "open", "resolved", true},
	}

	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	service := NewTicketLifecycleService(client, zaptest.NewLogger(t).Sugar())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, service.isValidStatusTransition(tt.currentStatus, tt.newStatus))
		})
	}
}

func TestTicketLifecycleService_GetEscalatedPriority_TableDriven(t *testing.T) {
	tests := []struct {
		name            string
		currentPriority string
		want            string
	}{
		{"low escalates to medium", "low", "medium"},
		{"medium escalates to high", "medium", "high"},
		{"high escalates to critical", "high", "critical"},
		{"critical stays critical", "critical", "critical"},
		{"unknown stays unknown", "unknown", "unknown"},
	}

	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	service := NewTicketLifecycleService(client, zaptest.NewLogger(t).Sugar())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, service.getEscalatedPriority(tt.currentPriority))
		})
	}
}

func createTicketLifecycleTestTenant(t *testing.T, ctx context.Context, client *ent.Client, suffix string) *ent.Tenant {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("Test Tenant " + suffix).
		SetCode("test_" + suffix).
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	return tenant
}

func createTicketLifecycleTestUser(t *testing.T, ctx context.Context, client *ent.Client, tenantID int, suffix string) *ent.User {
	t.Helper()
	user, err := client.User.Create().
		SetUsername("user_" + suffix).
		SetEmail(suffix + "@example.com").
		SetName("Test User " + suffix).
		SetPasswordHash("hashedpassword").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return user
}
