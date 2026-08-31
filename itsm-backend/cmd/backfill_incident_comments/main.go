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
//   - incident.work_item_id 异常为空的评论事件会被跳过并计入 skipped。migration 021 会在
//     收敛权威模型前拒绝这类数据，因此该分支只是防御性检查。
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
	"flag"
	"fmt"
	"os"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/ticketcomment"

	"go.uber.org/zap"
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
		return commentPlan{}, false, "incident 缺少权威 work_item_id", nil
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

// outcome 是 backfillOne 处理一条评论事件的结果。
type outcome int

const (
	outcomeCreated outcome = iota
	outcomeSkipped
)

// findCandidates 返回所有 event_type="comment" 的 IncidentEvent（可选按租户收窄）。是否真的
// 写入由 resolvePlan/backfillOne 逐条判断——这里只做粗筛，不查所属 Incident。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.IncidentEvent, error) {
	q := client.IncidentEvent.Query().Where(incidentevent.EventType("comment"))
	if tenantID > 0 {
		q = q.Where(incidentevent.TenantID(tenantID))
	}
	return q.All(ctx)
}

// alreadyMigrated 用 (ticket_id, user_id, content, created_at) 四元组查重——同一条评论事件
// 重复回填时命中同一行，不产生第二条 ticket_comments。
func alreadyMigrated(ctx context.Context, client *ent.Client, plan commentPlan) (bool, error) {
	return client.TicketComment.Query().
		Where(
			ticketcomment.TicketID(plan.ticketID),
			ticketcomment.UserID(plan.userID),
			ticketcomment.Content(plan.content),
			ticketcomment.CreatedAt(plan.createdAt),
		).
		Exist(ctx)
}

// backfillOne 处理一条评论事件：resolvePlan 判断是否该写入，alreadyMigrated 查重，
// 不存在则写入一条 ticket_comments。返回的 outcome 供调用方统计 created/skipped 数量，
// reason 在 outcomeSkipped 时说明原因（已迁移过 / 不满足写入条件），仅用于日志展示。
func backfillOne(ctx context.Context, client *ent.Client, event *ent.IncidentEvent) (outcome, string, error) {
	plan, ok, reason, err := resolvePlan(ctx, client, event)
	if err != nil {
		return outcomeSkipped, "", err
	}
	if !ok {
		return outcomeSkipped, reason, nil
	}

	exists, err := alreadyMigrated(ctx, client, plan)
	if err != nil {
		return outcomeSkipped, "", fmt.Errorf("查重失败: %w", err)
	}
	if exists {
		return outcomeSkipped, "已经回填过（命中查重）", nil
	}

	_, err = client.TicketComment.Create().
		SetTicketID(plan.ticketID).
		SetUserID(plan.userID).
		SetContent(plan.content).
		SetIsInternal(false).
		SetTenantID(plan.tenantID).
		SetCreatedAt(plan.createdAt).
		SetUpdatedAt(plan.createdAt).
		Save(ctx)
	if err != nil {
		return outcomeSkipped, "", fmt.Errorf("写入 ticket_comments 失败: %w", err)
	}
	return outcomeCreated, "", nil
}

// previewBackfill 是 -dry-run 用的只读版本：跑跟 backfillOne 完全相同的判断链路
// （resolvePlan + alreadyMigrated），但不调用 Create，只统计数量。skipReasons 按
// resolvePlan/alreadyMigrated 给出的原因字符串分组计数，而不是只给一个笼统的总数——
// 不同跳过原因的运维含义差别很大，折叠成一个数字会让操作员看不出该先处理哪类数据问题。
//
// failed 统计 resolvePlan/alreadyMigrated 查询本身出错（不是主动判定该跳过）的行数，
// 处理方式跟下面 main() 里真实回填循环的 failed 计数完全对应：单独一行查询出错不应该
// 像以前那样让整个预览直接 Fatal 掉、看不到其余行的预览结果——预览不该比真实回填更脆弱。
func previewBackfill(ctx context.Context, client *ent.Client, events []*ent.IncidentEvent) (wouldCreate int, skipReasons map[string]int, failed int, err error) {
	skipReasons = make(map[string]int)
	for _, event := range events {
		plan, ok, reason, resolveErr := resolvePlan(ctx, client, event)
		if resolveErr != nil {
			failed++
			continue
		}
		if !ok {
			skipReasons[reason]++
			continue
		}
		exists, existsErr := alreadyMigrated(ctx, client, plan)
		if existsErr != nil {
			failed++
			continue
		}
		if exists {
			// 与 backfillOne 命中查重时使用的原因字符串保持一致，保证预览和实际运行
			// 对"已经回填过"这一种情况报告出同样的文案。
			skipReasons["已经回填过（命中查重）"]++
			continue
		}
		wouldCreate++
	}
	return wouldCreate, skipReasons, failed, nil
}

func main() {
	tenantID := flag.Int("tenant-id", 0, "只处理指定租户（<=0 表示处理所有租户）")
	dryRun := flag.Bool("dry-run", true, "true 只打印候选统计，不实际写入；确认无误后用 -dry-run=false 真正回填")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		sugar.Fatalw("connect database", "error", err)
	}
	defer client.Close()

	ctx := tenantctx.SystemContext(
		context.Background(),
		"ops:backfill_incident_comments",
		"WorkItem 详情页能力对齐：把 incident_events 里的存量评论事件搬到 ticket_comments",
	)

	candidates, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填评论事件失败", "error", err)
	}
	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的评论事件", "tenant_id", *tenantID)
		return
	}
	sugar.Infow("找到待回填评论事件", "count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun)

	if *dryRun {
		wouldCreate, skipReasons, failed, err := previewBackfill(ctx, client, candidates)
		if err != nil {
			sugar.Fatalw("预览回填失败", "error", err)
		}
		sugar.Infow("dry-run 预览完成", "would_create", wouldCreate, "skip_reasons", skipReasons, "failed", failed)
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	created, skipped, failed := 0, 0, 0
	for _, event := range candidates {
		result, reason, err := backfillOne(ctx, client, event)
		switch {
		case err != nil:
			sugar.Errorw("回填评论失败", "incident_event_id", event.ID, "error", err)
			failed++
		case result == outcomeSkipped:
			sugar.Infow("跳过评论事件", "incident_event_id", event.ID, "reason", reason)
			skipped++
		default:
			created++
		}
	}

	sugar.Infow("回填完成", "created", created, "skipped", skipped, "failed", failed, "total", len(candidates))
	if failed > 0 {
		os.Exit(1)
	}
}
