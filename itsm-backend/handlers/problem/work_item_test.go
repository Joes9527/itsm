package problem

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/workitemrelation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProblemCreate_CreatesWorkItemInSameTransaction 锁定统一 WorkItem 领域模型宪章 §3.2
// 的事务边界约束：创建 Problem 必须在同一事务内建好对应的 tickets 行（record_class=
// "problem"），并把 problems.work_item_id 回填指向那条 tickets 行。
func TestProblemCreate_CreatesWorkItemInSameTransaction(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "wi-create")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "wi-create")

	p, err := service.Create(ctx, tenant.ID, &Problem{
		Title: "Disk latency spike", Description: "p99 disk latency", Priority: "high", CreatedBy: user.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, p.WorkItemID, "Problem.WorkItemID must be populated right after creation")

	workItem, err := client.Ticket.Get(ctx, *p.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "problem", workItem.RecordClass)
	assert.Equal(t, tenant.ID, workItem.TenantID)
	assert.Equal(t, user.ID, workItem.RequesterID)
	assert.Equal(t, p.Title, workItem.Title)
	assert.NotEmpty(t, workItem.TicketNumber)

	stored, err := client.Problem.Get(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, workItem.ID, stored.WorkItemID)
}

// TestProblemCreate_RollsBackWorkItemWhenCreatorInvalid 验证事务原子性：创建人不存在/不
// 属于该租户时，Problem 创建整体失败，且不留下孤儿 tickets 行（回滚必须清理已建的
// WorkItem，而不是留下一条没有 Problem 指向它的 tickets 记录）。
func TestProblemCreate_RollsBackWorkItemWhenCreatorInvalid(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "wi-rollback")

	ticketCountBefore, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)

	_, err = service.Create(ctx, tenant.ID, &Problem{
		Title: "Orphan attempt", Priority: "high", CreatedBy: 999999,
	})
	require.Error(t, err)

	ticketCountAfter, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, ticketCountBefore, ticketCountAfter, "failed problem creation must not leave an orphan tickets row")

	problemCount, err := client.Problem.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, problemCount)
}

// TestProblemAddAssociations_TicketWritesWorkItemRelation 锁定"relatedType=ticket 迁移到
// WorkItemRelation"这条要求：AddAssociations 对普通工单产生的是 WorkItemRelation 行
// （relation_type=related_to），而不是旧的 Problem<->Ticket ent 多对多 edge。
func TestProblemAddAssociations_TicketWritesWorkItemRelation(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "wi-assoc")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "wi-assoc")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)
	require.NotNil(t, p.WorkItemID)

	relatedTicket, err := client.Ticket.Create().
		SetTitle("Related ticket").SetTicketNumber("PRB-REL-1").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "ticket", []int{relatedTicket.ID}))

	relations, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenant.ID),
			workitemrelation.SourceWorkItemID(*p.WorkItemID),
			workitemrelation.TargetWorkItemID(relatedTicket.ID),
			workitemrelation.RelationType("related_to"),
			workitemrelation.DeletedAtIsNil(),
		).All(ctx)
	require.NoError(t, err)
	require.Len(t, relations, 1)
	assert.Equal(t, user.ID, relations[0].CreatedByID)

	// The legacy ent m2m edge must NOT be used by the new write path.
	problemEnt, err := client.Problem.Get(ctx, p.ID)
	require.NoError(t, err)
	legacyEdgeTickets, err := client.Problem.QueryTickets(problemEnt).All(ctx)
	require.NoError(t, err)
	assert.Empty(t, legacyEdgeTickets, "new ticket associations must not be written to the legacy Problem<->Ticket edge")

	// GetWithAssociations must resolve the ticket via WorkItemRelation.
	withAssoc, err := service.GetWithAssociations(ctx, p.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, withAssoc.Tickets, 1)
	assert.Equal(t, relatedTicket.ID, withAssoc.Tickets[0].ID)
	assert.Equal(t, relatedTicket.TicketNumber, withAssoc.Tickets[0].Number)

	// Idempotent re-add must not create a duplicate live relation row.
	require.NoError(t, service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "ticket", []int{relatedTicket.ID}))
	relationsAfterReAdd, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenant.ID),
			workitemrelation.SourceWorkItemID(*p.WorkItemID),
			workitemrelation.TargetWorkItemID(relatedTicket.ID),
			workitemrelation.RelationType("related_to"),
			workitemrelation.DeletedAtIsNil(),
		).All(ctx)
	require.NoError(t, err)
	require.Len(t, relationsAfterReAdd, 1, "re-adding the same ticket association must be a no-op, not a duplicate row")

	// RemoveAssociation soft-deletes the relation and relinking creates a fresh row.
	require.NoError(t, service.RemoveAssociation(ctx, tenant.ID, p.ID, "ticket", relatedTicket.ID))
	liveAfterRemove, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenant.ID),
			workitemrelation.SourceWorkItemID(*p.WorkItemID),
			workitemrelation.TargetWorkItemID(relatedTicket.ID),
			workitemrelation.DeletedAtIsNil(),
		).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, liveAfterRemove)

	withAssocAfterRemove, err := service.GetWithAssociations(ctx, p.ID, tenant.ID)
	require.NoError(t, err)
	assert.Empty(t, withAssocAfterRemove.Tickets)

	require.NoError(t, service.AddAssociations(ctx, tenant.ID, p.ID, user.ID, "ticket", []int{relatedTicket.ID}))
	liveAfterRelink, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenant.ID),
			workitemrelation.SourceWorkItemID(*p.WorkItemID),
			workitemrelation.TargetWorkItemID(relatedTicket.ID),
			workitemrelation.DeletedAtIsNil(),
		).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, liveAfterRelink, "relinking after soft-delete must succeed and produce exactly one live relation")
}

// TestProblemWorkItem_CrossTenantIsolation 锁定 AGENTS.md 租户强闭合约束：跨租户不能读取/
// 写入他租户的 Problem、其 WorkItem，或其 WorkItemRelation。
func TestProblemWorkItem_CrossTenantIsolation(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenantA := createProblemHandlerTenant(t, ctx, client, "wi-iso-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "wi-iso-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "wi-iso-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "wi-iso-b")

	problemA := createProblemHandlerProblem(t, ctx, service, tenantA.ID, userA.ID)
	require.NotNil(t, problemA.WorkItemID)

	ticketB, err := client.Ticket.Create().
		SetTitle("Tenant B ticket").SetTicketNumber("PRB-ISO-B").SetRequesterID(userB.ID).SetTenantID(tenantB.ID).Save(ctx)
	require.NoError(t, err)

	// Tenant B cannot link its own ticket to Tenant A's problem (problem lookup fails closed).
	err = service.AddAssociations(ctx, tenantB.ID, problemA.ID, userB.ID, "ticket", []int{ticketB.ID})
	require.Error(t, err)

	// Tenant A cannot link a foreign tenant's ticket to its own problem.
	err = service.AddAssociations(ctx, tenantA.ID, problemA.ID, userA.ID, "ticket", []int{ticketB.ID})
	require.ErrorContains(t, err, "current tenant")

	// Directly probing WorkItemRelation across tenants must not surface Tenant A's relation
	// under Tenant B's tenant_id filter, even if the caller knew the raw WorkItem IDs.
	ticketA, err := client.Ticket.Create().
		SetTitle("Tenant A ticket").SetTicketNumber("PRB-ISO-A").SetRequesterID(userA.ID).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, service.AddAssociations(ctx, tenantA.ID, problemA.ID, userA.ID, "ticket", []int{ticketA.ID}))

	foreignCount, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenantB.ID),
			workitemrelation.SourceWorkItemID(*problemA.WorkItemID),
		).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, foreignCount, "tenant B's tenant-scoped query must not see tenant A's relation")

	// GetWithAssociations under tenant B for problem A must fail closed (not found), never
	// silently return an empty/partial result impersonating success.
	_, err = service.GetWithAssociations(ctx, problemA.ID, tenantB.ID)
	require.True(t, ent.IsNotFound(err))
}

// TestGenerateWorkItemTicketNumber_CrossTenantSameMonthNoCollision 锁定一个在实现过程中
// 发现的真实缺陷类别：IncidentService.generateWorkItemTicketNumber 和
// repository/ticket.EntRepository.GenerateTicketNumber 都按租户维度计数生成
// TKT-YYYYMM-NNNNNN 编号，但 tickets.ticket_number 是全局唯一索引（不区分租户）——两个
// 不同租户在同一个月第一次创建单据都会生成 "TKT-YYYYMM-000001" 并撞上全局唯一约束。
// Problem 这次新写的生成器改成全局维度计数，这个测试锁定"两个租户各自创建 Problem 不
// 会互相撞号"这个正确行为。
func TestGenerateWorkItemTicketNumber_CrossTenantSameMonthNoCollision(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenantA := createProblemHandlerTenant(t, ctx, client, "seq-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "seq-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "seq-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "seq-b")

	pA, err := service.Create(ctx, tenantA.ID, &Problem{Title: "A first problem", Priority: "medium", CreatedBy: userA.ID})
	require.NoError(t, err)
	pB, err := service.Create(ctx, tenantB.ID, &Problem{Title: "B first problem", Priority: "medium", CreatedBy: userB.ID})
	require.NoError(t, err, "tenant B's first problem of the month must not collide with tenant A's ticket number")

	require.NotNil(t, pA.WorkItemID)
	require.NotNil(t, pB.WorkItemID)
	ticketA, err := client.Ticket.Get(ctx, *pA.WorkItemID)
	require.NoError(t, err)
	ticketB, err := client.Ticket.Get(ctx, *pB.WorkItemID)
	require.NoError(t, err)
	assert.NotEqual(t, ticketA.TicketNumber, ticketB.TicketNumber)
}

// fakeSequenceProvider is a minimal SequenceProvider stub used to exercise the Redis-backed
// branch of generateWorkItemTicketNumber without a real Redis instance.
type fakeSequenceProvider struct {
	seq int64
	err error
}

func (f *fakeSequenceProvider) GetNextSequenceWithExpiry(_ context.Context, _ string, _ time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.seq++
	return f.seq, nil
}

// TestEntRepository_SequenceProviderWiring 锁定 SetSequenceService 注入的 SequenceProvider
// 确实被 generateWorkItemTicketNumber 使用（优先于数据库兜底路径）；同时验证 Provider 返回
// 错误时能正确降级到数据库兜底，不影响 Problem 创建成功。这条路径目前没有被
// internal/bootstrap/app.go 注入（不在本次任务允许修改的文件范围内，见交付说明），
// 但作为新写的能力必须有单测覆盖，不能是形同虚设的死接口。
func TestEntRepository_SequenceProviderWiring(t *testing.T) {
	client, service, ctx := setupProblemHandlerTest(t)
	defer client.Close()
	tenant := createProblemHandlerTenant(t, ctx, client, "seq-provider")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "seq-provider")

	repo, ok := service.repo.(*EntRepository)
	require.True(t, ok)
	provider := &fakeSequenceProvider{seq: 40}
	repo.SetSequenceService(provider)

	p, err := service.Create(ctx, tenant.ID, &Problem{Title: "Sequenced problem", Priority: "low", CreatedBy: user.ID})
	require.NoError(t, err)
	require.NotNil(t, p.WorkItemID)
	workItem, err := client.Ticket.Get(ctx, *p.WorkItemID)
	require.NoError(t, err)
	now := time.Now()
	expected := fmt.Sprintf("TKT-%04d%02d-%06d", now.Year(), int(now.Month()), 41)
	assert.Equal(t, expected, workItem.TicketNumber)

	// Provider failure must gracefully fall back to the DB-based sequence, not fail creation.
	repo.SetSequenceService(&fakeSequenceProvider{err: fmt.Errorf("redis unavailable")})
	p2, err := service.Create(ctx, tenant.ID, &Problem{Title: "Fallback problem", Priority: "low", CreatedBy: user.ID})
	require.NoError(t, err)
	require.NotNil(t, p2.WorkItemID)
}

// TestProblemAddAssociationHTTP_MissingUserContext 锁定 AddAssociation 在缺少 user_id 上下文
// 时 fail closed（拒绝写入 WorkItemRelation.created_by_id 为 0/未知），而不是静默放行。
func TestProblemAddAssociationHTTP_MissingUserContext(t *testing.T) {
	r, _, service, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()

	tenant := createProblemHandlerTenant(t, ctx, client, "http-assoc-nouser")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "http-assoc-nouser")
	p := createProblemHandlerProblem(t, ctx, service, tenant.ID, user.ID)

	ticket1, err := client.Ticket.Create().
		SetTitle("T1").SetTicketNumber("PRB-NOUSER-1").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	assocReq := dto.ProblemAssociationRequest{RelatedType: "ticket", RelatedIDs: []int{ticket1.ID}}
	// performProblemRequest only sets X-User-ID when userID > 0; pass 0 to omit it.
	w := performProblemRequest(r, "POST", fmt.Sprintf("/api/v1/problems/%d/associations", p.ID), assocReq, tenant.ID, 0)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	var res common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, common.AuthErrorCode, res.Code)
}
