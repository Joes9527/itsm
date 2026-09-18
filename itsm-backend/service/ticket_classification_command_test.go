package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/handlers/shared/workitemmutation"
	ticketrepo "itsm-backend/repository/ticket"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

// createClassificationTree 建立一棵完整三级分类树（分类 → 类型 → 项目）。
func createClassificationTree(t *testing.T, f *bpmnAuthorizationFixture, tenantID int, prefix string) [3]*ent.TicketCategory {
	t.Helper()
	var parent *ent.TicketCategory
	var nodes [3]*ent.TicketCategory
	for level := 1; level <= 3; level++ {
		builder := f.client.TicketCategory.Create().
			SetTenantID(tenantID).
			SetCode(fmt.Sprintf("%s-l%d", prefix, level)).
			SetName(fmt.Sprintf("%s 第%d级", prefix, level)).
			SetLevel(level).
			SetIsActive(true)
		if parent != nil {
			builder = builder.SetParentID(parent.ID)
		}
		node, err := builder.Save(f.userCtx)
		require.NoError(t, err)
		nodes[level-1] = node
		parent = node
	}
	return nodes
}

// 通用工单的分类写入必须与其余四域同一权威契约：
// 只接受最深节点 ID；分类确实变化（设置或清空）时必须带原因；前后完整路径写入既有操作回执。
func TestGenericTicketClassificationRequiresNodeIDAndReason(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	genericCloseGrantPermission(t, f)
	repo := ticketrepo.NewEntRepository(f.client, f.engine.logger)
	svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: repo, Logger: f.engine.logger, Execution: executionfixture.Standard()})
	svc.SetNotificationService(genericCloseNotificationService(f.client, f.engine.logger, executionfixture.Standard()))

	tree := createClassificationTree(t, f, f.tenant.ID, "cti-command")
	foreign := createClassificationTree(t, f, f.otherTenant.ID, "cti-command-foreign")
	item := f.client.Ticket.Create().
		SetTenantID(f.tenant.ID).
		SetRequesterID(f.actor.ID).
		SetTitle("分类契约").
		SetTicketNumber("CTI-CMD-1").
		SetRecordClass("generic").
		SetStatus("new").
		SetPriority("medium").
		SaveX(f.userCtx)

	edit := func(operation string, fields dto.TicketEditFields) (workitemmutation.Result, error) {
		return svc.UpdateTicket(f.userCtx, dto.TicketEditCommand{
			WorkItemID: item.ID,
			Fields:     fields,
			Meta: workitemmutation.Meta{
				TenantID:        f.tenant.ID,
				ActorID:         f.actor.ID,
				ExpectedVersion: f.client.Ticket.GetX(f.userCtx, item.ID).Version,
				Source:          "http",
				OperationID:     operation,
			},
		})
	}

	// 1) 改变分类但没有原因：必须拒绝（不得静默写入）。
	_, err := edit("cti-no-reason", dto.TicketEditFields{CategoryID: &tree[2].ID})
	require.ErrorContains(t, err, "reason")

	// 2) 带原因设置完整三级节点：成功，且只保存最深节点 ID。
	_, err = edit("cti-set", dto.TicketEditFields{CategoryID: &tree[2].ID, ClassificationReason: "按现场信息归类"})
	require.NoError(t, err)
	stored := f.client.Ticket.GetX(f.userCtx, item.ID)
	require.Equal(t, tree[2].ID, stored.CategoryID)

	// 3) 同一目标重复提交属于"无变化"：允许无原因（幂等编辑不被打断）。
	_, err = edit("cti-same", dto.TicketEditFields{CategoryID: &tree[2].ID})
	require.NoError(t, err)

	// 4) 跨租户节点必须拒绝，且现状不变。
	_, err = edit("cti-foreign", dto.TicketEditFields{CategoryID: &foreign[2].ID, ClassificationReason: "跨租户"})
	require.Error(t, err)
	require.Equal(t, tree[2].ID, f.client.Ticket.GetX(f.userCtx, item.ID).CategoryID)

	// 5) 清空同样需要原因。
	zero := 0
	_, err = edit("cti-clear-no-reason", dto.TicketEditFields{CategoryID: &zero})
	require.ErrorContains(t, err, "reason")
	require.Equal(t, tree[2].ID, f.client.Ticket.GetX(f.userCtx, item.ID).CategoryID)

	_, err = edit("cti-clear", dto.TicketEditFields{CategoryID: &zero, ClassificationReason: "初始归类有误，先撤回"})
	require.NoError(t, err)
	cleared := f.client.Ticket.GetX(f.userCtx, item.ID)
	require.Zero(t, cleared.CategoryID)

	// 6) 纠正证据落在既有操作回执：原因 + 前后完整路径快照。
	receipt := f.client.AuditLog.Query().
		Where(auditlog.ResourceEQ("work_item"), auditlog.OperationIDEQ("cti-clear")).
		OnlyX(f.userCtx)
	require.NotNil(t, receipt.RequestBody)
	require.Contains(t, *receipt.RequestBody, `"classificationReason":"初始归类有误，先撤回"`)
	require.Contains(t, *receipt.RequestBody, `"classificationBefore"`)
	require.Contains(t, *receipt.RequestBody, fmt.Sprintf(`"id":%d`, tree[2].ID))
	require.NotContains(t, *receipt.RequestBody, `"category"`+`:`) // 不再存在按名称解析的载荷字段
}

// 拒绝纠正时不得留下任何分类或回执残留。
func TestGenericTicketClassificationRejectionLeavesNoTrace(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	genericCloseGrantPermission(t, f)
	repo := ticketrepo.NewEntRepository(f.client, f.engine.logger)
	svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: repo, Logger: f.engine.logger, Execution: executionfixture.Standard()})
	svc.SetNotificationService(genericCloseNotificationService(f.client, f.engine.logger, executionfixture.Standard()))

	tree := createClassificationTree(t, f, f.tenant.ID, "cti-reject")
	item := f.client.Ticket.Create().
		SetTenantID(f.tenant.ID).
		SetRequesterID(f.actor.ID).
		SetTitle("拒绝不留痕").
		SetTicketNumber("CTI-CMD-2").
		SetRecordClass("generic").
		SetStatus("new").
		SetPriority("medium").
		SaveX(f.userCtx)

	_, err := svc.UpdateTicket(f.userCtx, dto.TicketEditCommand{
		WorkItemID: item.ID,
		Fields:     dto.TicketEditFields{CategoryID: &tree[2].ID},
		Meta:       workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: f.client.Ticket.GetX(f.userCtx, item.ID).Version, Source: "http", OperationID: "cti-reject-no-reason"},
	})
	require.Error(t, err)
	require.Zero(t, f.client.Ticket.GetX(f.userCtx, item.ID).CategoryID)
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.OperationIDEQ("cti-reject-no-reason")).CountX(f.userCtx))

	// 目标不存在（引用已删除/id 编造）同样失败关闭。
	bogus := 987654
	_, err = svc.UpdateTicket(f.userCtx, dto.TicketEditCommand{
		WorkItemID: item.ID,
		Fields:     dto.TicketEditFields{CategoryID: &bogus, ClassificationReason: "编造目标"},
		Meta:       workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: f.client.Ticket.GetX(f.userCtx, item.ID).Version, Source: "http", OperationID: "cti-bogus-target"},
	})
	require.Error(t, err)
	require.Zero(t, f.client.Ticket.GetX(f.userCtx, item.ID).CategoryID)
}

// 只有原因、没有分类目标属于无效命令：必须显式报错，不能被静默丢弃。
func TestGenericTicketClassificationRejectsReasonWithoutTarget(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	genericCloseGrantPermission(t, f)
	repo := ticketrepo.NewEntRepository(f.client, f.engine.logger)
	svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: repo, Logger: f.engine.logger, Execution: executionfixture.Standard()})
	svc.SetNotificationService(genericCloseNotificationService(f.client, f.engine.logger, executionfixture.Standard()))

	item := f.client.Ticket.Create().
		SetTenantID(f.tenant.ID).
		SetRequesterID(f.actor.ID).
		SetTitle("原因无目标").
		SetTicketNumber("CTI-CMD-3").
		SetRecordClass("generic").
		SetStatus("new").
		SetPriority("medium").
		SaveX(f.userCtx)

	_, err := svc.UpdateTicket(f.userCtx, dto.TicketEditCommand{
		WorkItemID: item.ID,
		Fields:     dto.TicketEditFields{ClassificationReason: "只有原因没有分类"},
		Meta:       workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "cti-reason-without-target"},
	})
	require.ErrorContains(t, err, "requires a classification target")
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.OperationIDEQ("cti-reason-without-target")).CountX(f.userCtx))
}
