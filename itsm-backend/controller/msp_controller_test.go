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
	executionfixture "itsm-backend/tests/fixtures/execution"
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
			controller.ticketService = service.NewTicketService(&service.TicketServiceConfig{Client: client, Repository: repository.NewEntRepository(client, logger), Logger: logger, Execution: executionfixture.Standard(), Directory: sameTransactionDirectory{}, SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{})})
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
				payload = fmt.Sprintf(`{"title":"After","assigneeId":%d,"version":%d,"operationId":"serialization-update"}`, next.ID, item.Version)
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

type serializationFailureDirectory struct{}

func (serializationFailureDirectory) Open(context.Context, *ent.Tx, int) (*ent.Client, func() error, error) {
	return nil, nil, fmt.Errorf("directory snapshot: %w", &pq.Error{Code: "40001", Message: "private-database-detail"})
}

func TestConvergedAssignmentBoundariesMapSerializationConflict(t *testing.T) {
	for _, operation := range []string{"msp", "auto", "accept", "escalate", "subtask"} {
		t.Run(operation, func(t *testing.T) {
			_, client, tc := setupTestTicketController(t)
			tenant, actor := createTestTenantAndUserForTicket(t, client)
			ctx := tenantctx.WithTenantID(context.Background(), tenant.ID)
			actor = client.User.UpdateOne(actor).SetRole("super_admin").SaveX(ctx)
			parent := client.Ticket.Create().SetTitle("Parent").SetTicketNumber("SERIAL-PARENT").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
			item := client.Ticket.Create().SetTitle("Before").SetTicketNumber("SERIAL-CHILD").SetRequesterID(actor.ID).SetAssigneeID(actor.ID).SetParentTicketID(parent.ID).SetTenantID(tenant.ID).SaveX(ctx)
			logger := zap.NewNop().Sugar()
			sessions := authorization.NewSessionReader(client, serializationFailureDirectory{})
			tc.ticketService = service.NewTicketService(&service.TicketServiceConfig{Client: client, Repository: repository.NewEntRepository(client, logger), Logger: logger, Execution: executionfixture.Standard(), Directory: serializationFailureDirectory{}, SessionReader: sessions})
			record := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(record)
			c.Set("user_id", actor.ID)
			c.Set("tenant_id", tenant.ID)
			c.Set("role", "super_admin")
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(item.ID)}}
			payload := `{}`
			var handler gin.HandlerFunc
			switch operation {
			case "msp":
				handler = (&MSPController{ticketService: tc.ticketService, logger: logger}).AssignMSPTechnician
				payload = fmt.Sprintf(`{"customerTenantId":%d}`, tenant.ID)
			case "auto":
				handler = serializationConflictAutoAssignHandler(client, logger, sessions)
			case "accept":
				handler = serializationConflictAcceptTicketHandler(client, logger, sessions)
				payload = fmt.Sprintf(`{"ticketId":%d}`, item.ID)
			case "escalate":
				handler = tc.EscalateTicket
				payload = fmt.Sprintf(`{"reason":"Review conflict","version":%d,"operationId":"serialization-escalate"}`, item.Version)
			case "subtask":
				handler = tc.UpdateSubtask
				c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(parent.ID)}, {Key: "subtask_id", Value: strconv.Itoa(item.ID)}}
				payload = fmt.Sprintf(`{"title":"After","assigneeId":%d,"version":%d,"operationId":"serialization-update"}`, actor.ID, item.Version)
			}
			c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(payload)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			handler(c)
			require.Equal(t, 409, record.Code, record.Body.String())
			require.Contains(t, record.Body.String(), `"retryable":true`)
			require.NotContains(t, record.Body.String(), "private-database-detail")
			require.NotContains(t, record.Body.String(), "currentVersion")
			require.Equal(t, "Before", client.Ticket.GetX(ctx, item.ID).Title)
		})
	}
}
