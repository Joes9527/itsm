package service_request_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	service_catalog "itsm-backend/handlers/service_catalog"
	sr "itsm-backend/handlers/service_request"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"

	_ "github.com/mattn/go-sqlite3"
)

// Requested Item 的分类纠正契约：
// 目录已声明完整三级，因此申请项既不允许清空、也不接受部分分类；
// 原因必填、版本 CAS、证据与写入同事务。
type srClassificationFixture struct {
	client *ent.Client
	ctx    context.Context
	svc    *Service
	tenant *ent.Tenant
	admin  *ent.User
	user   *ent.User
	viewer *ent.User
	item   *ent.Ticket
	tree   [3]int
	other  [3]int
}

func newSRClassificationFixture(t *testing.T) *srClassificationFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode(t.Name()).SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("owner").SetEmail("owner@test.com").SetName("Owner").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	admin, err := client.User.Create().SetUsername("admin").SetEmail("admin@test.com").SetName("Admin").
		SetPasswordHash("hash").SetRole("super_admin").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// 边界按权限而不是角色名判定：viewer 没有 service_request:write。
	viewer, err := client.User.Create().SetUsername("viewer").SetEmail("viewer@test.com").SetName("Viewer").
		SetPasswordHash("hash").SetRole("viewer").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	svc := NewService(NewEntRepository(client, executionfixture.Standard()), client, zaptest.NewLogger(t).Sugar(), nil)

	scService := service_catalog.NewService(service_catalog.NewEntRepository(client), client, zaptest.NewLogger(t).Sugar(), nil)
	configureCatalogPublicationForTest(ctx, client, tenant.ID, scService)
	catalog, err := scService.Create(ctx, tenant.ID, catalogCreateInput("云主机申请-分类纠正", "云服务", "desc", 1, "enabled", 0, 0, nil, "", ""))
	require.NoError(t, err)

	created, err := svc.SubmitCreation(ctx, tenant.ID, requester.ID, catalog.ID, &sr.ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData:           map[string]interface{}{"title": "申请一台云主机-分类纠正", "reason": "分类纠正回归"},
	})
	require.NoError(t, err)

	categories := service.NewTicketCategoryService(client)
	tree := func(prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant.ID})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}
	primary := tree("sr")
	alternative := tree("sr-alt")
	// 申请项初始状态与目录声明一致：完整三级。
	client.Ticket.UpdateOneID(created.TicketID).SetCategoryID(primary[2]).ExecX(ctx)

	item, err := client.Ticket.Get(ctx, created.TicketID)
	require.NoError(t, err)
	return &srClassificationFixture{client: client, ctx: ctx, svc: svc, tenant: tenant, admin: admin, user: requester, viewer: viewer, item: item, tree: primary, other: alternative}
}

func (f *srClassificationFixture) workItem(t *testing.T) *ent.Ticket {
	t.Helper()
	return f.client.Ticket.GetX(f.ctx, f.item.ID)
}

func (f *srClassificationFixture) srID(t *testing.T) int {
	t.Helper()
	row := f.client.ServiceRequest.Query().
		Where(servicerequest.HasWorkItemWith(ticket.IDEQ(f.item.ID))).
		OnlyX(f.ctx)
	return row.ID
}

func (f *srClassificationFixture) correct(t *testing.T, target int, reason string, version int, role string) error {
	t.Helper()
	actor := f.admin
	switch role {
	case "viewer":
		actor = f.viewer
	case "end_user":
		actor = f.user
	}
	_, err := f.svc.CorrectClassification(f.ctx, sr.ClassificationCorrection{
		Meta:             workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: actor.ID, Source: "http", ExpectedVersion: version},
		ServiceRequestID: f.srID(t),
		TargetCategoryID: target,
		Reason:           reason,
		ActorRole:        role,
	})
	return err
}

func TestRequestedItemClassificationCorrectionRequiresCompleteTarget(t *testing.T) {
	f := newSRClassificationFixture(t)

	// 完整 → 完整：允许，版本 +1，并写入前后路径证据。
	require.NoError(t, f.correct(t, f.other[2], "申请入口选错了服务项", f.workItem(t).Version, "super_admin"))
	updated := f.workItem(t)
	require.Equal(t, f.other[2], updated.CategoryID)
	require.Equal(t, f.item.Version+1, updated.Version)

	receipt := f.client.AuditLog.Query().Where(auditlog.ResourceEQ("work_item_classification")).OnlyX(f.ctx)
	require.NotNil(t, receipt.RequestBody)
	require.Contains(t, *receipt.RequestBody, `"classificationReason":"申请入口选错了服务项"`)
	require.Contains(t, *receipt.RequestBody, fmt.Sprintf(`"id":%d`, f.tree[2]))
	require.Contains(t, *receipt.RequestBody, fmt.Sprintf(`"id":%d`, f.other[2]))

	// 完整 → 部分：拒绝（目录已声明完整三级）。
	err := f.correct(t, f.other[1], "试图降级到二级", f.workItem(t).Version, "super_admin")
	require.ErrorContains(t, err, "complete three-level path")
	// 完整 → 空：拒绝（申请项不允许无分类）。
	err = f.correct(t, 0, "试图清空分类", f.workItem(t).Version, "super_admin")
	require.Error(t, err)
	require.Equal(t, f.other[2], f.workItem(t).CategoryID)
	require.Equal(t, f.item.Version+1, f.workItem(t).Version)
}

func TestRequestedItemClassificationCorrectionGuards(t *testing.T) {
	f := newSRClassificationFixture(t)
	version := f.workItem(t).Version

	// 原因必填：缺原因时不改分类、不写审计、不动版本。
	err := f.correct(t, f.other[2], "   ", version, "super_admin")
	require.ErrorContains(t, err, "reason is required")
	require.Equal(t, f.tree[2], f.workItem(t).CategoryID)
	require.Equal(t, version, f.workItem(t).Version)
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.ResourceEQ("work_item_classification")).CountX(f.ctx))

	// 无 service_request:write 的角色不能改分类（权限判定，不按角色名）。
	err = f.correct(t, f.other[2], "无权修改分类", version, "viewer")
	require.Error(t, err)
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeForbidden, appErr.Code)

	// 缺少显式预期版本：按参数错误拒绝。
	err = f.correct(t, f.other[2], "缺少预期版本", 0, "super_admin")
	require.Error(t, err)
	appErr, ok = common.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, common.ErrCodeValidation, appErr.Code)

	// 过期版本（正数但非当前）：按版本冲突拒绝，且不写入。
	err = f.correct(t, f.other[2], "过期版本", version+5, "super_admin")
	require.Error(t, err)
	require.True(t, common.IsVersionConflictError(err), "stale version must be a version conflict, got %v", err)
	require.Equal(t, f.tree[2], f.workItem(t).CategoryID)
	require.Equal(t, version, f.workItem(t).Version)

	// 停用 / 未知 / 跨租户目标：一律拒绝且不落库。
	inactive := f.tree[0]
	f.client.TicketCategory.UpdateOneID(inactive).SetIsActive(false).ExecX(f.ctx)
	foreign := f.client.Tenant.Create().SetName("other").SetCode(t.Name() + "-other").SetDomain("o.test").SetStatus("active").SaveX(f.ctx)
	foreignCategory := f.client.TicketCategory.Create().SetName("foreign").SetCode("foreign").SetTenantID(foreign.ID).SetLevel(1).SetIsActive(true).SaveX(f.ctx)
	for _, scenario := range []struct {
		name   string
		target int
	}{
		{"inactive", inactive},
		{"unknown", 999999},
		{"foreign", foreignCategory.ID},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			err := f.correct(t, scenario.target, "非法目标探测", f.workItem(t).Version, "super_admin")
			require.Error(t, err)
			require.Equal(t, f.tree[2], f.workItem(t).CategoryID)
		})
	}
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.ResourceEQ("work_item_classification")).CountX(f.ctx))
}

func TestRequestedItemClassificationCorrectionRollsBackWhenAuditFails(t *testing.T) {
	f := newSRClassificationFixture(t)
	f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				return nil, errors.New("injected audit failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	err := f.correct(t, f.other[2], "审计失败必须整笔回滚", f.workItem(t).Version, "super_admin")
	require.Error(t, err)
	after := f.workItem(t)
	require.Equal(t, f.tree[2], after.CategoryID, "audit failure must roll back the classification write")
	require.Equal(t, f.item.Version, after.Version)
}

// 分类纠正只动分类：归属与其它共享字段不变。
func TestRequestedItemClassificationCorrectionKeepsDerivedState(t *testing.T) {
	f := newSRClassificationFixture(t)
	assignee := f.admin.ID
	f.client.Ticket.UpdateOneID(f.item.ID).SetAssigneeID(assignee).SetStatus("in_progress").ExecX(f.ctx)
	before := f.workItem(t)

	require.NoError(t, f.correct(t, f.other[2], "纠正分类但不改归属", before.Version, "super_admin"))
	after := f.client.Ticket.Query().Where(ticket.IDEQ(f.item.ID)).OnlyX(f.ctx)
	require.Equal(t, f.other[2], after.CategoryID)
	require.Equal(t, assignee, after.AssigneeID)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Priority, after.Priority)
	require.Equal(t, before.ResolvedAt, after.ResolvedAt)
	require.Nil(t, after.ClosedAt)
}
