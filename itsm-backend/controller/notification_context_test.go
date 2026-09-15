package controller

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"itsm-backend/authentication"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// SQLite provides real notification persistence; these guards enforce the typed
// request-context requirement of the PostgreSQL RLS boundary without shared DBs.
func TestNotificationControllerPreservesTenantContextForPersistence(t *testing.T) {
	_, client, tenantID, userID := setupNotificationController(t)
	scoped := tenantctx.WithTenantID(context.Background(), tenantID)
	check := func(ctx context.Context) error {
		got, ok := tenantctx.TenantID(ctx)
		if !ok || got != tenantID {
			return fmt.Errorf("notification persistence lost authenticated tenant")
		}
		return nil
	}
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			if err := check(ctx); err != nil {
				return nil, err
			}
			return next.Query(ctx, q)
		})
	}))
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if err := check(ctx); err != nil {
				return nil, err
			}
			return next.Mutate(ctx, m)
		})
	})
	ctrl := NewNotificationController(service.NewNotificationService(client))
	for _, tc := range []struct {
		name, method string
		handler      gin.HandlerFunc
		body         interface{}
	}{
		{name: "list", method: "GET", handler: ctrl.GetNotifications},
		{name: "unread", method: "GET", handler: ctrl.GetUnreadCount},
		{name: "mark read", method: "PUT", handler: ctrl.MarkNotificationRead},
		{name: "mark all read", method: "PUT", handler: ctrl.MarkAllNotificationsRead},
		{name: "delete", method: "DELETE", handler: ctrl.DeleteNotification},
		{name: "create", method: "POST", handler: ctrl.CreateNotification, body: dto.CreateNotificationRequest{Title: "Context check", Message: "Test", Type: "info", UserID: userID, TenantID: tenantID}},
		{name: "batch read", method: "POST", handler: ctrl.MarkNotificationsRead},
		{name: "batch delete", method: "POST", handler: ctrl.DeleteNotifications},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := client.Notification.Create().SetTitle("existing").SetMessage("test").SetType("info").SetUserID(userID).SetTenantID(tenantID).Save(scoped)
			require.NoError(t, err)
			r := gin.New() // ContextWithFallback remains false, matching production.
			r.Use(func(c *gin.Context) {
				no := false
				c.Request = authentication.WithCookieTransportPolicy(c.Request, &no, true)
				c.Set("tenant_id", tenantID)
				c.Set("user_id", userID)
				c.Request = c.Request.WithContext(tenantctx.WithTenantID(c.Request.Context(), tenantID))
				c.Next()
			})
			r.Handle(tc.method, "/notifications/:id", tc.handler)
			body := tc.body
			if tc.name == "batch read" || tc.name == "batch delete" {
				body = dto.BatchNotificationRequest{NotificationIDs: []int{row.ID}}
			}
			resp := doReq(t, r, tc.method, "/notifications/"+strconv.Itoa(row.ID), body, false)
			require.Equal(t, common.SuccessCode, resp.Code, "body=%s", mustString(resp))
		})
	}
}
