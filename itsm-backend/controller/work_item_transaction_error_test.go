package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	repository "itsm-backend/repository/ticket"
	"itsm-backend/service"
)

func TestBPMNSerializationConflictIsRetryableWithoutDatabaseDetail(t *testing.T) {
	record := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(record)
	respondBPMNError(ctx, fmt.Errorf("complete: %w", &pq.Error{Code: "40001", Message: "private-database-detail"}), "complete")
	require.Equal(t, 409, record.Code)
	require.Contains(t, record.Body.String(), `"retryable":true`)
	require.NotContains(t, record.Body.String(), "private-database-detail")
	require.NotContains(t, record.Body.String(), "currentVersion")
}

func TestTicketAssignmentAndMixedUpdateSerializationConflict(t *testing.T) {
	for _, operation := range []string{"assign", "update"} {
		t.Run(operation, func(t *testing.T) {
			router, client, controller := setupTestTicketController(t)
			tenant, actor := createTestTenantAndUserForTicket(t, client)
			ctx := tenantctx.WithTenantID(context.Background(), tenant.ID)
			actor = client.User.UpdateOne(actor).SetRole("super_admin").SaveX(ctx)
			next := client.User.Create().SetTenantID(tenant.ID).SetUsername("next").SetName("Next").SetEmail("next@example.test").SetPasswordHash("unused").SetRole("super_admin").SaveX(ctx)
			item := client.Ticket.Create().SetTitle("Before").SetTicketNumber("SERIAL-1").SetRequesterID(actor.ID).SetAssigneeID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
			logger := zap.NewNop().Sugar()
			controller.ticketService = service.NewTicketService(&service.TicketServiceConfig{Client: client, Repository: repository.NewEntRepository(client, logger), Logger: logger, SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{})})
			router.PUT("/api/v1/tickets/:id/assign", controller.AssignTicket)
			client.Ticket.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
					return nil, fmt.Errorf("assignment: %w", &pq.Error{Code: "40001", Message: "private-database-detail"})
				})
			})
			path := "/api/v1/tickets/" + strconv.Itoa(item.ID)
			payload := fmt.Sprintf(`{"assigneeId":%d}`, next.ID)
			if operation == "assign" {
				path += "/assign"
			} else {
				payload = fmt.Sprintf(`{"title":"After","assigneeId":%d,"version":%d}`, next.ID, item.Version)
			}
			request := httptest.NewRequest("PUT", path, bytes.NewBufferString(payload)).WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Test-Tenant", strconv.Itoa(tenant.ID))
			request.Header.Set("X-Test-User", strconv.Itoa(actor.ID))
			request.Header.Set("X-Test-Role", "super_admin")
			record := httptest.NewRecorder()
			router.ServeHTTP(record, request)
			require.Equal(t, 409, record.Code, record.Body.String())
			require.Contains(t, record.Body.String(), `"retryable":true`)
			require.NotContains(t, record.Body.String(), "private-database-detail")
			saved := client.Ticket.GetX(ctx, item.ID)
			require.Equal(t, "Before", saved.Title)
			require.Equal(t, actor.ID, saved.AssigneeID)
		})
	}
}
