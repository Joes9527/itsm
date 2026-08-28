// backfill_incident_comments 是 WorkItem 详情页能力对齐设计（docs/superpowers/specs/
// 2026-08-28-work-item-detail-page-parity-design.md §4.2）用的一次性迁移工具。
//
// 背景：Incident 评论历史上存在 incident_events 表里（event_type="comment"），Problem/Change/
// Ticket 的评论统一存在 ticket_comments 表里。本工具把 Incident 的存量评论事件按
// incident.work_item_id 写入 ticket_comments，为前端切到统一的 ticketCommentAdapter 做数据
// 准备。跑完、前端切换完成后，controller/incident_controller.go 的 GetIncidentComments/
// CreateIncidentComment 及对应路由才能安全退役——见同一设计文档 §4.2 第 3 步。
//
// 不处理的事：
//   - user_id 缺失（<=0）或 content 为空的历史评论事件会被跳过并计入 skipped，不会报错——
//     ticket_comments.user_id 是 Positive() 必填、content 是 NotEmpty() 必填，旧数据里这两种
//     不合规的行本来就无法映射成一条合法的 ticket_comments。
//   - incident.work_item_id 仍为空（还没跑过 cmd/backfill_incident_work_item）的 Incident 下的
//     评论事件会被跳过并计入 skipped——本工具不负责创建 WorkItem，只负责搬评论。
//   - 所属 Incident 已软删除的评论事件会被跳过。
//   - is_internal/mentions 一律用默认值（false / 空），不从 incident_events.data 里解析——
//     旧数据这两个字段不可靠，见设计文档 §4.2 第 1 步。
//
// 用法：
//
//	go run ./cmd/backfill_incident_comments -dry-run=true               # 预览，不写入
//	go run ./cmd/backfill_incident_comments -dry-run=false              # 全部租户实际回填
//	go run ./cmd/backfill_incident_comments -dry-run=false -tenant-id=3 # 只处理指定租户
package main

import (
	"context"
	"time"

	"itsm-backend/ent"
)

// commentPlan 是 resolvePlan 对一条 incident_events 评论事件算出的、待写入 ticket_comments
// 的字段集合。
type commentPlan struct {
	ticketID  int
	userID    int
	content   string
	tenantID  int
	createdAt time.Time
}

// resolvePlan 决定一条 incident_events 评论事件应该生成一条 ticket_comments 写入计划
// （ok=true），还是因为不满足写入条件而跳过（ok=false，reason 给出原因）。纯决策逻辑，
// 除了查一次所属 Incident 之外不做任何写入——backfillOne 和 previewBackfill 共用它，
// 保证"预览会跳过的行"和"实际会跳过的行"是同一套判断，不会各写一份分叉的逻辑。
func resolvePlan(ctx context.Context, client *ent.Client, event *ent.IncidentEvent) (plan commentPlan, ok bool, reason string, err error) {
	inc, err := client.Incident.Get(ctx, event.IncidentID)
	if err != nil {
		return commentPlan{}, false, "", err
	}
	if inc.DeletedAt != nil {
		return commentPlan{}, false, "所属 incident 已被软删除", nil
	}
	if inc.WorkItemID == 0 {
		return commentPlan{}, false, "incident 尚未回填 work_item_id（先跑 cmd/backfill_incident_work_item）", nil
	}
	if event.UserID <= 0 {
		return commentPlan{}, false, "评论事件缺少可归属的 user_id", nil
	}
	if event.Description == "" {
		return commentPlan{}, false, "评论事件 description 为空", nil
	}
	return commentPlan{
		ticketID:  inc.WorkItemID,
		userID:    event.UserID,
		content:   event.Description,
		tenantID:  event.TenantID,
		createdAt: event.CreatedAt,
	}, true, "", nil
}
