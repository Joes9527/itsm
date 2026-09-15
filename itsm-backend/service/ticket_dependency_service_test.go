package service

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupTicketDependencyTest(t *testing.T) (*ent.Client, *TicketDependencyService, context.Context) {
	client := enttest.Open(t, "sqlite3", testDSN())
	logger := zaptest.NewLogger(t).Sugar()
	service := NewTicketDependencyService(client, logger)
	ctx := context.Background()
	return client, service, ctx
}

func createDependencyTestTenant(ctx context.Context, client *ent.Client, suffix string) (*ent.Tenant, error) {
	return client.Tenant.Create().
		SetName("Dependency Tenant " + suffix).
		SetCode("dep" + suffix).
		SetDomain("dep" + suffix + ".example.com").
		SetStatus("active").
		Save(ctx)
}

func createDependencyTestUser(ctx context.Context, client *ent.Client, tenantID int, suffix string) (*ent.User, error) {
	return client.User.Create().
		SetUsername("depuser" + suffix).
		SetEmail("dep" + suffix + "@example.com").
		SetName("Dependency Test User").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenantID).
		Save(ctx)
}

func createDependencyTestTicket(ctx context.Context, client *ent.Client, tenantID, requesterID int, number string) (*ent.Ticket, error) {
	return client.Ticket.Create().
		SetTitle("Dependency Test Ticket " + number).
		SetDescription("desc").
		SetPriority("medium").
		SetStatus("open").
		SetTicketNumber(number).
		SetTenantID(tenantID).
		SetRequesterID(requesterID).
		Save(ctx)
}

// TestGetRelationStats_TicketNotFound 锁定 fail-closed 行为：不存在的工单 ID 必须
// 报错，不能被静默当成"没有关联"返回一份全零的成功统计——那样跟"自己的空工单"
// 在响应形状上完全无法区分。
func TestGetRelationStats_TicketNotFound(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	tenant, err := createDependencyTestTenant(ctx, client, "notfound")
	require.NoError(t, err)

	stats, err := service.GetRelationStats(ctx, 999999, tenant.ID)
	require.Error(t, err, "不存在的工单 ID 必须返回错误，不能静默返回全零统计")
	assert.Nil(t, stats)
	assert.Contains(t, err.Error(), "ticket not found",
		"错误信息必须匹配 controller 层 404 映射所依赖的 \"ticket not found\" 约定")
}

// TestGetRelationStats_CrossTenantTicketFailsClosed 锁定跨租户访问必须 fail closed：
// 用另一个租户的 tenantID 查询一条真实存在的工单，必须报错，不能返回该工单的真实
// 或伪造的关联统计。
func TestGetRelationStats_CrossTenantTicketFailsClosed(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	ownerTenant, err := createDependencyTestTenant(ctx, client, "owner")
	require.NoError(t, err)
	otherTenant, err := createDependencyTestTenant(ctx, client, "other")
	require.NoError(t, err)

	requester, err := createDependencyTestUser(ctx, client, ownerTenant.ID, "owner")
	require.NoError(t, err)

	tkt, err := createDependencyTestTicket(ctx, client, ownerTenant.ID, requester.ID, "TKT-DEP-CROSS-1")
	require.NoError(t, err)

	stats, err := service.GetRelationStats(ctx, tkt.ID, otherTenant.ID)
	require.Error(t, err, "跨租户访问必须报错，不能静默返回统计")
	assert.Nil(t, stats)
	assert.Contains(t, err.Error(), "ticket not found")
}

// TestGetRelationStats_OwnTenantSucceeds 确认修复没有连带破坏本租户内正常查询：
// 一个有子工单和父工单的工单必须返回正确的统计。
func TestGetRelationStats_OwnTenantSucceeds(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	tenant, err := createDependencyTestTenant(ctx, client, "own")
	require.NoError(t, err)
	requester, err := createDependencyTestUser(ctx, client, tenant.ID, "own")
	require.NoError(t, err)

	parent, err := createDependencyTestTicket(ctx, client, tenant.ID, requester.ID, "TKT-DEP-PARENT-1")
	require.NoError(t, err)

	child, err := createDependencyTestTicket(ctx, client, tenant.ID, requester.ID, "TKT-DEP-CHILD-1")
	require.NoError(t, err)
	_, err = child.Update().SetParentTicketID(parent.ID).Save(ctx)
	require.NoError(t, err)

	parentStats, err := service.GetRelationStats(ctx, parent.ID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, parentStats)
	assert.Equal(t, 1, parentStats.ChildrenCount)
	assert.Equal(t, 0, parentStats.ParentCount)
	assert.Equal(t, 1, parentStats.TotalRelations)

	childStats, err := service.GetRelationStats(ctx, child.ID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, childStats)
	assert.Equal(t, 0, childStats.ChildrenCount)
	assert.Equal(t, 1, childStats.ParentCount)
	assert.Equal(t, 1, childStats.TotalRelations)
}

// TestGetTicketRelations_TicketNotFound 锁定 fail-closed 行为：跟 GetRelationStats
// 一样，不存在的工单 ID 必须报错，不能返回一个空列表——空列表和"这个 ID 不存在"
// 在响应形状上必须能区分开。
func TestGetTicketRelations_TicketNotFound(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	tenant, err := createDependencyTestTenant(ctx, client, "relnotfound")
	require.NoError(t, err)

	relations, err := service.GetTicketRelations(ctx, 999999, tenant.ID)
	require.Error(t, err)
	assert.Nil(t, relations)
	assert.Contains(t, err.Error(), "ticket not found")
}

// TestGetTicketRelations_CrossTenantFailsClosed 锁定跨租户访问必须 fail closed。
func TestGetTicketRelations_CrossTenantFailsClosed(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	ownerTenant, err := createDependencyTestTenant(ctx, client, "relowner")
	require.NoError(t, err)
	otherTenant, err := createDependencyTestTenant(ctx, client, "relother")
	require.NoError(t, err)
	requester, err := createDependencyTestUser(ctx, client, ownerTenant.ID, "relowner")
	require.NoError(t, err)

	tkt, err := createDependencyTestTicket(ctx, client, ownerTenant.ID, requester.ID, "TKT-DEP-REL-CROSS-1")
	require.NoError(t, err)

	relations, err := service.GetTicketRelations(ctx, tkt.ID, otherTenant.ID)
	require.Error(t, err)
	assert.Nil(t, relations)
	assert.Contains(t, err.Error(), "ticket not found")
}

// TestGetTicketRelations_NoRelations 确认没有父工单也没有子工单时返回空切片
// （不是 nil），前端按空数组渲染"暂无关联工单"。
func TestGetTicketRelations_NoRelations(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	tenant, err := createDependencyTestTenant(ctx, client, "relnone")
	require.NoError(t, err)
	requester, err := createDependencyTestUser(ctx, client, tenant.ID, "relnone")
	require.NoError(t, err)
	tkt, err := createDependencyTestTicket(ctx, client, tenant.ID, requester.ID, "TKT-DEP-REL-NONE-1")
	require.NoError(t, err)

	relations, err := service.GetTicketRelations(ctx, tkt.ID, tenant.ID)
	require.NoError(t, err)
	assert.NotNil(t, relations)
	assert.Len(t, relations, 0)
}

// TestGetTicketRelations_ParentAndChildren 锁定父子关系的完整形状：本工单既有父
// 工单又有子工单时，两条关联记录都要出现，且 source/target 方向正确——这是
// TicketRelationCards.tsx 判断"本单指向对方"还是"对方指向本单"的唯一依据。
func TestGetTicketRelations_ParentAndChildren(t *testing.T) {
	client, service, ctx := setupTicketDependencyTest(t)
	defer client.Close()

	tenant, err := createDependencyTestTenant(ctx, client, "relfull")
	require.NoError(t, err)
	requester, err := createDependencyTestUser(ctx, client, tenant.ID, "relfull")
	require.NoError(t, err)

	grandparent, err := createDependencyTestTicket(ctx, client, tenant.ID, requester.ID, "TKT-DEP-REL-GP-1")
	require.NoError(t, err)
	middle, err := createDependencyTestTicket(ctx, client, tenant.ID, requester.ID, "TKT-DEP-REL-MID-1")
	require.NoError(t, err)
	_, err = middle.Update().SetParentTicketID(grandparent.ID).Save(ctx)
	require.NoError(t, err)
	middle, err = client.Ticket.Get(ctx, middle.ID)
	require.NoError(t, err)

	child, err := createDependencyTestTicket(ctx, client, tenant.ID, requester.ID, "TKT-DEP-REL-CHILD-1")
	require.NoError(t, err)
	_, err = child.Update().SetParentTicketID(middle.ID).Save(ctx)
	require.NoError(t, err)

	relations, err := service.GetTicketRelations(ctx, middle.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, relations, 2, "既有父工单又有子工单，必须各出现一条")

	var parentRel, childRel *dto.TicketRelation
	for _, r := range relations {
		if r.TargetTicketID == middle.ID {
			parentRel = r
		}
		if r.SourceTicketID == middle.ID {
			childRel = r
		}
	}

	require.NotNil(t, parentRel, "本工单作为 target 的那条（来自祖父工单）必须存在")
	assert.Equal(t, grandparent.ID, parentRel.SourceTicketID)
	assert.Equal(t, grandparent.TicketNumber, parentRel.SourceTicketNumber)
	assert.Equal(t, "parent_child", parentRel.RelationType)
	require.NotNil(t, parentRel.SourceTicket)
	assert.Equal(t, grandparent.Title, parentRel.SourceTicket.Title)

	require.NotNil(t, childRel, "本工单作为 source 的那条（指向子工单）必须存在")
	assert.Equal(t, child.ID, childRel.TargetTicketID)
	assert.Equal(t, child.TicketNumber, childRel.TargetTicketNumber)
	require.NotNil(t, childRel.TargetTicket)
	assert.Equal(t, child.Title, childRel.TargetTicket.Title)
}
