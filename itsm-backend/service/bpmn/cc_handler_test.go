package bpmn

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type ccHandlerRecipientFixture struct {
	client      *ent.Client
	ctx         context.Context
	tenant      *ent.Tenant
	requester   *ent.User
	recipient   *ent.User
	ticket      *ent.Ticket
	handler     *CCTaskHandler
	callbackCtx context.Context
}

func newCCHandlerRecipientFixture(t *testing.T, suffix string) *ccHandlerRecipientFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+suffix+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode(suffix).SetDomain(suffix + ".test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername(suffix + "-requester").SetEmail(suffix + "-requester@test.invalid").SetPasswordHash("x").SetName("Requester").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername(suffix + "-recipient").SetEmail(suffix + "-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC recipient test").SetTicketNumber("CC-" + suffix).SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, suffix+"-delivery-key")
	return &ccHandlerRecipientFixture{
		client: client, ctx: ctx, tenant: tenant, requester: requester, recipient: recipient,
		ticket: ticket, handler: NewCCTaskHandler(client, zap.NewNop().Sugar()), callbackCtx: callbackCtx,
	}
}

func (f *ccHandlerRecipientFixture) variables(ccType string) map[string]interface{} {
	return map[string]interface{}{
		"ticket_id": f.ticket.ID,
		"ccType":    ccType,
		"ccNotify":  true,
	}
}

func (f *ccHandlerRecipientFixture) assertNoEffects(t *testing.T) {
	t.Helper()
	assert.Zero(t, f.client.TicketCC.Query().CountX(f.ctx))
	assert.Zero(t, f.client.TicketNotification.Query().CountX(f.ctx))
	assert.Zero(t, f.client.Notification.Query().CountX(f.ctx))
}

func TestCCTaskHandler_UsesContextTenantAndRejectsCrossTenantRecipients(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cc_handler_tenant?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenantA := client.Tenant.Create().SetName("A").SetCode("cc-a").SetDomain("cc-a.test").SetStatus("active").SaveX(ctx)
	tenantB := client.Tenant.Create().SetName("B").SetCode("cc-b").SetDomain("cc-b.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-requester").SetEmail("cc-requester@test.invalid").SetPasswordHash("x").SetName("Requester").SetTenantID(tenantA.ID).SetActive(true).SaveX(ctx)
	crossTenantUser := client.User.Create().SetUsername("cc-cross").SetEmail("cc-cross@test.invalid").SetPasswordHash("x").SetName("Cross").SetTenantID(tenantB.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC tenant test").SetTicketNumber("CC-1").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenantA.ID).SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenantA.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, "cc-cross-tenant-key")

	_, err := handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id": ticket.ID,
		"tenant_id": tenantB.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(crossTenantUser.ID),
		"ccNotify":  true,
	})

	require.Error(t, err)
	assert.Zero(t, client.TicketCC.Query().CountX(ctx))
	assert.Zero(t, client.TicketNotification.Query().CountX(ctx))
	assert.Zero(t, client.Notification.Query().CountX(ctx))
}

func TestCCTaskHandler_RetryIsAtomicAndIdempotent(t *testing.T) {
	const dsn = "file:cc_handler_retry?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("cc-retry").SetDomain("cc-retry.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-owner").SetEmail("cc-owner@test.invalid").SetPasswordHash("x").SetName("Owner").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername("cc-recipient").SetEmail("cc-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC retry test").SetTicketNumber("CC-2").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, "cc-retry-key")
	variables := map[string]interface{}{
		"ticket_id": ticket.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(recipient.ID),
		"ccNotify":  true,
	}

	first, err := handler.Execute(callbackCtx, nil, variables)
	require.NoError(t, err)
	second, err := handler.Execute(callbackCtx, nil, variables)
	require.NoError(t, err)

	assert.Equal(t, 1, client.TicketCC.Query().CountX(ctx))
	assert.Equal(t, 1, client.TicketNotification.Query().CountX(ctx))
	assert.Equal(t, 1, client.Notification.Query().CountX(ctx))
	assert.Equal(t, []int{recipient.ID}, first.OutputVars["added_cc_users"])
	assert.Empty(t, second.OutputVars["added_cc_users"])

	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var storedKey sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, "SELECT delivery_key FROM ticket_ccs").Scan(&storedKey))
	require.True(t, storedKey.Valid)
	assert.Equal(t, "cc-retry-key", storedKey.String)

	row := client.TicketCC.Query().OnlyX(ctx)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "deliveryKey")
	assert.NotContains(t, string(encoded), "delivery_key")
}

func TestCCTaskHandler_RequiresCallbackExecutionKey(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cc_handler_requires_key?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("cc-key").SetDomain("cc-key.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-key-owner").SetEmail("cc-key-owner@test.invalid").SetPasswordHash("x").SetName("Owner").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername("cc-key-recipient").SetEmail("cc-key-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC key test").SetTicketNumber("CC-KEY").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)

	_, err := handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id": ticket.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(recipient.ID),
		"ccNotify":  true,
	})

	require.EqualError(t, err, "抄送回调执行键不能为空")
	assert.Zero(t, client.TicketCC.Query().CountX(ctx))
	assert.Zero(t, client.TicketNotification.Query().CountX(ctx))
	assert.Zero(t, client.Notification.Query().CountX(ctx))
}

func TestCCTaskHandler_DifferentDeliveryReusesActiveOrdinaryRelation(t *testing.T) {
	const dsn = "file:cc_handler_active_ordinary?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("cc-ordinary").SetDomain("cc-ordinary.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-ordinary-owner").SetEmail("cc-ordinary-owner@test.invalid").SetPasswordHash("x").SetName("Owner").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername("cc-ordinary-recipient").SetEmail("cc-ordinary-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC ordinary test").SetTicketNumber("CC-ORDINARY").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	ordinary := client.TicketCC.Create().
		SetTicketID(ticket.ID).
		SetUserID(recipient.ID).
		SetAddedBy(requester.ID).
		SetTenantID(tenant.ID).
		SetAddedAt(time.Now()).
		SetIsActive(true).
		SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, "cc-different-delivery-key")

	result, err := handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id": ticket.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(recipient.ID),
		"ccNotify":  true,
	})

	require.NoError(t, err)
	assert.Empty(t, result.OutputVars["added_cc_users"])
	assert.Equal(t, 1, client.TicketCC.Query().CountX(ctx))
	assert.Zero(t, client.TicketNotification.Query().CountX(ctx))
	assert.Zero(t, client.Notification.Query().CountX(ctx))

	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var storedKey sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, "SELECT delivery_key FROM ticket_ccs WHERE id = ?", ordinary.ID).Scan(&storedKey))
	assert.False(t, storedKey.Valid)
}

func TestCCTaskHandlerRejectsUnknownNotifyChannelsBeforeEffects(t *testing.T) {
	for _, tt := range []struct {
		name      string
		channels  interface{}
		wantError bool
		want      []string
	}{
		{name: "omitted defaults to in app", want: []string{"in_app"}},
		{name: "empty defaults to in app", channels: "  ", want: []string{"in_app"}},
		{name: "known channels deduplicate", channels: "email, in_app, email", want: []string{"email", "in_app"}},
		{name: "unknown channel", channels: "unknown", wantError: true},
		{name: "mixed known and unknown channels", channels: "email,unknown", wantError: true},
		{name: "non string channels", channels: []interface{}{"email"}, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCCHandlerRecipientFixture(t, "cc-notify-"+strconv.Itoa(len(tt.name)))
			variables := fixture.variables("user")
			variables["ccUserIds"] = strconv.Itoa(fixture.recipient.ID)
			if tt.channels != nil {
				variables["notifyChannels"] = tt.channels
			}

			_, err := fixture.handler.Execute(fixture.callbackCtx, nil, variables)
			if tt.wantError {
				require.ErrorContains(t, err, "通知渠道")
				fixture.assertNoEffects(t)
				return
			}
			require.NoError(t, err)
			rows := fixture.client.TicketNotification.Query().AllX(fixture.ctx)
			channels := make([]string, 0, len(rows))
			for _, row := range rows {
				channels = append(channels, row.Channel)
			}
			assert.ElementsMatch(t, tt.want, channels)
		})
	}
}

func TestCCTaskHandlerNormalizeCallbackPayloadValidatesChannelsForEveryCCType(t *testing.T) {
	handler := NewCCTaskHandler(nil, zap.NewNop().Sugar())
	for _, tt := range []struct {
		name      string
		variables map[string]interface{}
	}{
		{name: "omitted", variables: map[string]interface{}{"ccType": "user"}},
		{name: "empty", variables: map[string]interface{}{"ccType": "user", "notifyChannels": "  "}},
	} {
		t.Run(tt.name+" channels default to in app", func(t *testing.T) {
			payload, err := handler.NormalizeCallbackPayload("", tt.variables)

			require.NoError(t, err)
			assert.Equal(t, "in_app", payload["notifyChannels"])
		})
	}

	for _, ccType := range []string{"user", "group", "role", "variable"} {
		t.Run(ccType+" canonicalizes valid channels", func(t *testing.T) {
			variables := map[string]interface{}{
				"ccType":         ccType,
				"notifyChannels": " email, in_app,email ",
			}
			if ccType == "variable" {
				variables["ccVariable"] = "watchers"
				variables["watchers"] = []interface{}{float64(7)}
			}

			payload, err := handler.NormalizeCallbackPayload("", variables)

			require.NoError(t, err)
			assert.Equal(t, "email,in_app", payload["notifyChannels"])
		})

		t.Run(ccType+" rejects mixed invalid channels", func(t *testing.T) {
			variables := map[string]interface{}{
				"ccType":         ccType,
				"notifyChannels": "in_app,emial",
			}
			if ccType == "variable" {
				variables["ccVariable"] = "watchers"
				variables["watchers"] = []interface{}{float64(7)}
			}

			payload, err := handler.NormalizeCallbackPayload("", variables)

			require.ErrorContains(t, err, "通知渠道")
			assert.Nil(t, payload)
		})

		t.Run(ccType+" rejects non string channels", func(t *testing.T) {
			variables := map[string]interface{}{
				"ccType":         ccType,
				"notifyChannels": []interface{}{"email"},
			}
			if ccType == "variable" {
				variables["ccVariable"] = "watchers"
				variables["watchers"] = []interface{}{float64(7)}
			}

			payload, err := handler.NormalizeCallbackPayload("", variables)

			require.ErrorContains(t, err, "通知渠道")
			assert.Nil(t, payload)
		})
	}
}

func TestCCTaskHandlerValidatesTenantOwnedGroupAndRoleSelectors(t *testing.T) {
	for _, selectorType := range []string{"group", "role"} {
		for _, tt := range []struct {
			name      string
			selector  func(t *testing.T, f *ccHandlerRecipientFixture) int
			wantError bool
		}{
			{
				name: "foreign tenant selector",
				selector: func(t *testing.T, f *ccHandlerRecipientFixture) int {
					t.Helper()
					otherTenant := f.client.Tenant.Create().SetName("Other").SetCode("other-" + selectorType).SetDomain("other-" + selectorType + ".test").SetStatus("active").SaveX(f.ctx)
					otherUser := f.client.User.Create().SetUsername("other-" + selectorType).SetEmail("other-" + selectorType + "@test.invalid").SetPasswordHash("x").SetName("Other").SetTenantID(otherTenant.ID).SetActive(true).SaveX(f.ctx)
					if selectorType == "group" {
						group := f.client.Group.Create().SetName("foreign-group").SetTenantID(otherTenant.ID).SaveX(f.ctx)
						f.client.User.UpdateOneID(otherUser.ID).AddGroupIDs(group.ID).ExecX(f.ctx)
						return group.ID
					}
					role := f.client.Role.Create().SetName("foreign role").SetCode("foreign-" + selectorType).SetTenantID(otherTenant.ID).SaveX(f.ctx)
					f.client.User.UpdateOneID(otherUser.ID).AddRoleIDs(role.ID).ExecX(f.ctx)
					return role.ID
				},
				wantError: true,
			},
			{
				name: "missing selector",
				selector: func(t *testing.T, _ *ccHandlerRecipientFixture) int {
					t.Helper()
					return 999999
				},
				wantError: true,
			},
			{
				name: "empty same tenant selector",
				selector: func(t *testing.T, f *ccHandlerRecipientFixture) int {
					t.Helper()
					if selectorType == "group" {
						return f.client.Group.Create().SetName("empty-group").SetTenantID(f.tenant.ID).SaveX(f.ctx).ID
					}
					return f.client.Role.Create().SetName("empty role").SetCode("empty-" + selectorType).SetTenantID(f.tenant.ID).SaveX(f.ctx).ID
				},
				wantError: true,
			},
			{
				name: "active same tenant selector",
				selector: func(t *testing.T, f *ccHandlerRecipientFixture) int {
					t.Helper()
					if selectorType == "group" {
						group := f.client.Group.Create().SetName("active-group").SetTenantID(f.tenant.ID).SaveX(f.ctx)
						f.client.User.UpdateOneID(f.recipient.ID).AddGroupIDs(group.ID).ExecX(f.ctx)
						return group.ID
					}
					role := f.client.Role.Create().SetName("active role").SetCode("active-" + selectorType).SetTenantID(f.tenant.ID).SaveX(f.ctx)
					f.client.User.UpdateOneID(f.recipient.ID).AddRoleIDs(role.ID).ExecX(f.ctx)
					return role.ID
				},
			},
		} {
			t.Run(selectorType+"/"+tt.name, func(t *testing.T) {
				fixture := newCCHandlerRecipientFixture(t, "cc-selector-"+selectorType+"-"+strconv.Itoa(len(tt.name)))
				selectorID := tt.selector(t, fixture)
				variables := fixture.variables(selectorType)
				if selectorType == "group" {
					variables["ccGroupIds"] = strconv.Itoa(selectorID)
				} else {
					variables["ccRoleIds"] = strconv.Itoa(selectorID)
				}

				result, err := fixture.handler.Execute(fixture.callbackCtx, nil, variables)
				if tt.wantError {
					require.Error(t, err)
					fixture.assertNoEffects(t)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, []int{fixture.recipient.ID}, result.OutputVars["added_cc_users"])
				assert.Equal(t, 1, fixture.client.TicketCC.Query().CountX(fixture.ctx))
				assert.Equal(t, 1, fixture.client.TicketNotification.Query().CountX(fixture.ctx))
				assert.Equal(t, 1, fixture.client.Notification.Query().CountX(fixture.ctx))
			})
		}
	}
}
