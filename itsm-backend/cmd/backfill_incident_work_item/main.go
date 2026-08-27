// backfill_incident_work_item 是 Wave 2（统一 Work Item 领域模型重构 · Incident 迁移）
// 用的一次性迁移工具，不是常规业务命令。
//
// 背景：Wave 2 之后 IncidentService.CreateIncident 在同一个数据库事务内同时创建
// tickets 行（record_class="incident"）和 incidents 行，并把 incidents.work_item_id
// 回填指向那条 tickets 行。但这次改动之前创建的存量 Incident 没有对应的 tickets 行，
// work_item_id 是 NULL。这个工具把这批存量记录补齐：为每条缺失 work_item_id 的 Incident
// 新建一条 tickets 行（record_class="incident"，公共字段取自 Incident 自身），并把
// incidents.work_item_id 回填指向新建的这条 tickets 行——同一条 Incident 记录只在
// work_item_id 为空时处理，重复运行是幂等的（已经回填过的行不会再进候选列表，也不会
// 被覆盖）。
//
// 不处理的事：
//   - 不迁移存量运行中的 ProcessInstance.BusinessID（那是
//     cmd/backfill_process_instance_business_identity 类工具的职责边界，且这批 Incident
//     在 Wave 2 之前创建时触发的流程用的是 Incident 自身主键做 business_id，混着 WorkItem
//     ID 语义热切换存在运行中流程状态不一致的风险，不在本工具范围内，需要业务判断，
//     参见 docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md §15.2.6）。
//   - 不处理软删除（deleted_at 非空）的 Incident：这些记录在产品里已经不可见，没有
//     必要为它们新建 WorkItem。
//   - 跑完之后建议用 cmd/check_work_item_integrity 复核一次，确认不再有
//     record_class=incident 但缺 work_item_id 的不一致。
//
// 用法：
//
//	go run ./cmd/backfill_incident_work_item -dry-run=true               # 预览，不写入
//	go run ./cmd/backfill_incident_work_item -dry-run=false              # 全部租户实际回填
//	go run ./cmd/backfill_incident_work_item -dry-run=false -tenant-id=3 # 只处理指定租户
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	entticket "itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

func main() {
	tenantID := flag.Int("tenant-id", 0, "只处理指定租户（<=0 表示处理所有租户）")
	dryRun := flag.Bool("dry-run", true, "true 只打印候选列表，不实际写入；确认无误后用 -dry-run=false 真正回填")
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
		"ops:backfill_incident_work_item",
		"WorkItem Wave 2：给切换前创建的存量 Incident 补建 tickets 行并回填 work_item_id",
	)

	candidates, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填事件失败", "error", err)
	}

	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的事件", "tenant_id", *tenantID)
		return
	}

	sugar.Infow("找到待回填事件", "count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun)
	for _, c := range candidates {
		sugar.Infow("候选事件",
			"incident_id", c.ID, "tenant_id", c.TenantID,
			"incident_number", c.IncidentNumber, "title", c.Title)
	}

	if *dryRun {
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	succeeded, failed := 0, 0
	for _, c := range candidates {
		if err := backfillOne(ctx, client, c); err != nil {
			sugar.Errorw("回填失败", "incident_id", c.ID, "tenant_id", c.TenantID, "error", err)
			failed++
			continue
		}
		sugar.Infow("回填成功", "incident_id", c.ID, "tenant_id", c.TenantID)
		succeeded++
	}

	sugar.Infow("回填完成", "succeeded", succeeded, "failed", failed, "total", len(candidates))
	if failed > 0 {
		os.Exit(1)
	}
}

// findCandidates 返回缺失 work_item_id、未被软删除的存量 Incident。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Incident, error) {
	q := client.Incident.Query().
		Where(incident.WorkItemIDIsNil(), incident.DeletedAtIsNil())
	if tenantID > 0 {
		q = q.Where(incident.TenantID(tenantID))
	}
	return q.All(ctx)
}

// backfillOne 在一个数据库事务内为一条 Incident 新建 tickets 行（record_class="incident"，
// 创建后不可变）并回填 incidents.work_item_id——同 IncidentService.CreateIncident 新创建
// 路径遵守的同一条不变量（AGENTS.md §3.2：WorkItem 创建与专业扩展记录创建必须在同一事务中
// 完成）。用 Where(incident.WorkItemIDIsNil()) 作为更新条件，保证并发运行/重复运行时不会
// 对同一条 Incident 回填两次。
func backfillOne(ctx context.Context, client *ent.Client, inc *ent.Incident) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	rollback := func(cause error) error {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w（回滚也失败: %v）", cause, rbErr)
		}
		return cause
	}

	ticketNumber, err := generateBackfillTicketNumber(ctx, tx.Client(), inc.TenantID, inc.CreatedAt)
	if err != nil {
		return rollback(fmt.Errorf("生成工单编号失败: %w", err))
	}

	now := time.Now()
	workItem, err := tx.Ticket.Create().
		SetTitle(inc.Title).
		SetDescription(inc.Description).
		SetType("incident").
		SetRecordClass("incident").
		SetPriority(inc.Priority).
		SetTicketNumber(ticketNumber).
		SetRequesterID(inc.ReporterID).
		SetTenantID(inc.TenantID).
		SetCreatedAt(inc.CreatedAt).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("创建 WorkItem 失败: %w", err))
	}

	affected, err := tx.Incident.Update().
		Where(incident.ID(inc.ID), incident.TenantID(inc.TenantID), incident.WorkItemIDIsNil()).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("回填 work_item_id 失败: %w", err))
	}
	if affected == 0 {
		// 条件更新 0 行说明这条 Incident 在本工具查出候选之后、写入之前已经被并发处理
		// 补上了 work_item_id（例如两次运行本工具重叠，或业务侧正好走了新建路径）——
		// 不是错误，回滚这次多余的 WorkItem 创建，避免留下一条没有 Incident 指向它的
		// 孤儿 tickets 行。
		return rollback(fmt.Errorf("事件 %d 的 work_item_id 已被并发回填，跳过并回滚本次新建的 WorkItem", inc.ID))
	}

	if err := tx.Commit(); err != nil {
		return rollback(fmt.Errorf("提交事务失败: %w", err))
	}
	return nil
}

// generateBackfillTicketNumber 复用与 IncidentService.generateWorkItemTicketNumber /
// repository/ticket.EntRepository.GenerateTicketNumber 相同的编号格式（TKT-YYYYMM-NNNNNN），
// 但按 Incident 自己的创建时间取年月而不是当前时间——回填的 WorkItem 编号应该落在事件
// 实际发生的月份，而不是全部堆到运行这个工具的当月。这里用同一事务内的 DB 查询算下一个
// 序号：一次性离线工具逐行串行处理，不需要 Redis 原子计数器，同一事务内查最大值 + 1
// 足够避免同一次运行内的重复（不同事务之间仍受 ticket_number 的唯一索引兜底保护，
// 撞号会在 Save 时报错，交由上层 backfillOne 整体回滚重试）。
func generateBackfillTicketNumber(ctx context.Context, client *ent.Client, tenantID int, createdAt time.Time) (string, error) {
	year, month := createdAt.Year(), int(createdAt.Month())
	prefix := fmt.Sprintf("TKT-%04d%02d-", year, month)

	existing, err := client.Ticket.Query().
		Where(entticket.TenantIDEQ(tenantID), entticket.TicketNumberHasPrefix(prefix)).
		All(ctx)
	if err != nil {
		return "", err
	}
	maxSeq := 0
	for _, t := range existing {
		if idx := strings.LastIndex(t.TicketNumber, "-"); idx >= 0 {
			var seq int
			if _, scanErr := fmt.Sscanf(t.TicketNumber[idx+1:], "%d", &seq); scanErr == nil && seq > maxSeq {
				maxSeq = seq
			}
		}
	}
	return fmt.Sprintf("TKT-%04d%02d-%06d", year, month, maxSeq+1), nil
}
