package approver

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTeamWorkloadTest(t *testing.T) (*ent.Client, context.Context, *ent.Tenant) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:team_workload_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("TWR Tenant").SetCode("twr").SetDomain("twr.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	return client, ctx, tenant
}

func createTeam(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, name, code string) *ent.Team {
	t.Helper()
	tm, err := client.Team.Create().SetName(name).SetCode(code).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	return tm
}

func createTeamMember(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, username string, teamID int) *ent.User {
	t.Helper()
	// User 的 ent schema 没有声明反向的 team 边（team_users 外键只在 Team 侧用
	// edge.To("users", User.Type) 声明），所以没有 UserCreate.SetTeamXxx 这种方法——
	// 必须先建好 User，再从 Team 一侧用 AddUserIDs 把这个外键关系接上。
	u, err := client.User.Create().
		SetUsername(username).SetEmail(username + "@twr.example.com").SetName(username).
		SetPasswordHash("hash").SetActive(true).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Team.UpdateOneID(teamID).AddUserIDs(u.ID).Save(ctx)
	require.NoError(t, err)
	return u
}

var twrTicketSeq int

// createOpenTicket 每次调用生成一个新的唯一 ticket_number（同一个 assignee/requester 组合
// 可能被调用多次，比如给同一个人堆多张负载工单），用一个包级自增计数器而不是拼 ID，避免
// 撞 ticket_number 的唯一约束。
func createOpenTicket(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, requesterID, assigneeID int) {
	t.Helper()
	twrTicketSeq++
	_, err := client.Ticket.Create().
		SetTitle("load").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("TWR-" + strconvItoa(twrTicketSeq)).
		SetRequesterID(requesterID).SetAssigneeID(assigneeID).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestTeamWorkloadResolver_PicksLeastBusyMember(t *testing.T) {
	client, ctx, tenant := setupTeamWorkloadTest(t)
	team := createTeam(t, client, ctx, tenant.ID, "服务台-L1", "服务台-l1")

	busy := createTeamMember(t, client, ctx, tenant.ID, "busy_agent", team.ID)
	idle := createTeamMember(t, client, ctx, tenant.ID, "idle_agent", team.ID)
	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("requester@twr.example.com").SetName("requester").
		SetPasswordHash("hash").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	createOpenTicket(t, client, ctx, tenant.ID, requester.ID, busy.ID)
	createOpenTicket(t, client, ctx, tenant.ID, requester.ID, busy.ID)

	resolver := NewTeamWorkloadResolver()
	result, err := resolver.Resolve(ctx, client, &ApproverContext{TenantID: tenant.ID, TeamCode: "服务台-l1"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, idle.ID, result[0].UserID, "零负载的 idle_agent 应该胜出")
}

func TestTeamWorkloadResolver_EmptyTeam_ReturnsError(t *testing.T) {
	client, ctx, tenant := setupTeamWorkloadTest(t)
	createTeam(t, client, ctx, tenant.ID, "服务台-L1", "服务台-l1")

	resolver := NewTeamWorkloadResolver()
	_, err := resolver.Resolve(ctx, client, &ApproverContext{TenantID: tenant.ID, TeamCode: "服务台-l1"})
	assert.Error(t, err)
}

func TestTeamWorkloadResolver_UnknownTeamCode_ReturnsError(t *testing.T) {
	client, ctx, tenant := setupTeamWorkloadTest(t)

	resolver := NewTeamWorkloadResolver()
	_, err := resolver.Resolve(ctx, client, &ApproverContext{TenantID: tenant.ID, TeamCode: "不存在的团队"})
	assert.Error(t, err)
}
