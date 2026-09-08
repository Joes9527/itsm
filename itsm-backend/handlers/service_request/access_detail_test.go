package service_request_test

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	sr "itsm-backend/handlers/service_request"
	"testing"
)

func TestManagedAccessDetailUsesAuthoritativeFulfillment(t *testing.T) {
	fx, task, itemID, req := verifiedAccessFixture(t)
	owner := sr.NewService(sr.NewEntRepository(fx.client), fx.client, zap.NewNop().Sugar(), nil)
	r := gin.New()
	r.Use(srAuth(fx.tenant.ID, fx.requester.ID))
	r.GET("/by-ticket/:ticketId", sr.NewHandler(owner).GetByTicket)
	for _, state := range []string{"fulfilling", "completed"} {
		if state == "completed" {
			_, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
			require.NoError(t, err)
		}
		response := srDoReq(t, r, "GET", fmt.Sprintf("/by-ticket/%d", itemID), nil)
		require.Zero(t, response.Code)
		data := response.Data.(map[string]interface{})
		require.Equal(t, state, data["fulfillmentState"])
		require.Equal(t, false, data["actions"].(map[string]interface{})["provision"].(map[string]interface{})["allowed"])
	}
}
