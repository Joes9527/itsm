package dto

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestEnrichTicketResponseUsers(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket-user-enrich?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("租户一").SetCode("tenant-1").Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("enrich-requester").
		SetEmail("enrich-requester@example.com").
		SetName("张建国").
		SetPasswordHash("hash").
		SetTenantID(tenant.ID).
		SetDepartment("核心业务二组").
		Save(ctx)
	require.NoError(t, err)

	assignee, err := client.User.Create().
		SetUsername("enrich-assignee").
		SetEmail("enrich-assignee@example.com").
		SetName("李维").
		SetPasswordHash("hash").
		SetTenantID(tenant.ID).
		SetDepartment("运维一线").
		Save(ctx)
	require.NoError(t, err)

	resp := &TicketResponse{ID: 1, RequesterID: requester.ID, AssigneeID: assignee.ID}
	EnrichTicketResponseUsers(ctx, client, resp, tenant.ID)

	require.NotNil(t, resp.Requester)
	require.Equal(t, "张建国", resp.Requester.Name)
	require.Equal(t, "核心业务二组", resp.Requester.Department)

	require.NotNil(t, resp.Assignee)
	require.Equal(t, "李维", resp.Assignee.Name)
	require.Equal(t, "运维一线", resp.Assignee.Department)
}

func TestEnrichTicketResponseUsersCrossTenantFailClosed(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket-user-enrich-ct?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	queryTenant, err := client.Tenant.Create().SetName("租户甲").SetCode("tenant-a").Save(ctx)
	require.NoError(t, err)
	otherTenant, err := client.Tenant.Create().SetName("租户乙").SetCode("tenant-b").Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("ct-requester").
		SetEmail("ct-requester@example.com").
		SetName("跨租户用户").
		SetPasswordHash("hash").
		SetTenantID(otherTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 查询租户甲时不应看到租户乙的用户
	resp := &TicketResponse{ID: 2, RequesterID: requester.ID}
	EnrichTicketResponseUsers(ctx, client, resp, queryTenant.ID)

	// 跨租户用户不应出现在响应中（fail closed）
	require.Nil(t, resp.Requester)
}
