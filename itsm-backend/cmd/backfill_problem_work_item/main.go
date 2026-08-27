// backfill_problem_work_item 是 Wave 2（统一 Work Item 领域模型重构 · Problem 迁移）
// 用的一次性迁移工具，不是常规业务命令。镜像 cmd/backfill_incident_work_item 的形状。
//
// 背景：Wave 2 之后 handlers/problem.EntRepository.Create 在同一个数据库事务内同时创建
// tickets 行（record_class="problem"）和 problems 行，并把 problems.work_item_id 回填指向
// 那条 tickets 行。但这次改动之前创建的存量 Problem 没有对应的 tickets 行，work_item_id
// 是 NULL。这个工具把这批存量记录补齐，且顺带完成第二件事：
//
//  1. 为每条缺失 work_item_id 的 Problem 新建一条 tickets 行（record_class="problem"，
//     公共字段取自 Problem 自身），并把 problems.work_item_id 回填指向新建的这条 tickets 行。
//  2. 把这条 Problem 通过旧的 ent Problem<->Ticket 多对多 edge（AddAssociations 迁移前的
//     写路径）已经关联的普通工单，逐条迁移成 WorkItemRelation（relation_type="related_to"，
//     source=新建的 WorkItem，target=被关联的工单 ID）。迁移后旧 edge 数据本身不删除（删除
//     需要改 ent/schema/problem.go 并触发一次全量 ent codegen，超出本次任务允许修改的文件
//     范围），但应用层从这次改动起只读写 WorkItemRelation，不再读旧 edge。
//
// 这两步在同一个数据库事务里完成，任一步失败整体回滚，不会留下"tickets 行建好了但
// work_item_id 没回填"或"回填了但旧关联没迁移"的中间态。
//
// 同一条 Problem 记录只在 work_item_id 为空时处理，重复运行是幂等的（已经回填过的行不会
// 再进候选列表，也不会被覆盖）。
//
// 不处理的事：
//   - 不迁移存量运行中的 ProcessInstance.BusinessID——Problem 目前没有接入任何 BPMN 触发
//     路径（handlers/problem.Service.Create 不调用 processTriggerService；唯一引用
//     "problem_management_flow" 触发逻辑的 service.ProblemService.triggerWorkflowForProblem
//     是确认死代码，从未被真实路由触达），所以不存在需要迁移的运行中 Problem 流程实例。
//   - 不处理 relatedType="incident"/"change" 方向的关联：这次 Problem 迁移任务范围明确
//     只覆盖 relatedType="ticket" 这一支路径，incident/change 方向继续使用旧 edge。
//   - 不处理软删除（deleted_at 非空）的 Problem：这些记录在产品里已经不可见，没有必要为
//     它们新建 WorkItem。
//   - 迁移出的 WorkItemRelation.created_by_id 取 Problem 自己的 created_by（原始 Problem
//     创建人）作为最佳近似值——旧的 ent m2m edge 不记录"是谁在什么时候建立了这条关联"，
//     没有更准确的来源可用。
//
// 用法：
//
//	go run ./cmd/backfill_problem_work_item -dry-run=true               # 预览，不写入
//	go run ./cmd/backfill_problem_work_item -dry-run=false              # 全部租户实际回填
//	go run ./cmd/backfill_problem_work_item -dry-run=false -tenant-id=3 # 只处理指定租户
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
	"itsm-backend/ent/problem"
	entticket "itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemrelation"

	"go.uber.org/zap"
)

// problemTicketRelationType 必须与 handlers/problem/repository_impl.go 里的同名常量取值
// 一致——两处各自定义（避免让这个一次性迁移工具依赖业务包内部符号），修改其中一处时要
// 同步检查另一处。
const problemTicketRelationType = "related_to"

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
		"ops:backfill_problem_work_item",
		"WorkItem Wave 2：给切换前创建的存量 Problem 补建 tickets 行、回填 work_item_id，并把旧的 Problem<->Ticket 关联迁移到 WorkItemRelation",
	)

	candidates, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填问题失败", "error", err)
	}

	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的问题", "tenant_id", *tenantID)
		return
	}

	sugar.Infow("找到待回填问题", "count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun)
	for _, c := range candidates {
		sugar.Infow("候选问题", "problem_id", c.ID, "tenant_id", c.TenantID, "title", c.Title)
	}

	if *dryRun {
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	succeeded, failed, relationsMigrated := 0, 0, 0
	for _, c := range candidates {
		migrated, err := backfillOne(ctx, client, c)
		if err != nil {
			sugar.Errorw("回填失败", "problem_id", c.ID, "tenant_id", c.TenantID, "error", err)
			failed++
			continue
		}
		sugar.Infow("回填成功", "problem_id", c.ID, "tenant_id", c.TenantID, "migrated_ticket_relations", migrated)
		succeeded++
		relationsMigrated += migrated
	}

	sugar.Infow("回填完成", "succeeded", succeeded, "failed", failed, "total", len(candidates), "migrated_ticket_relations_total", relationsMigrated)
	if failed > 0 {
		os.Exit(1)
	}
}

// findCandidates 返回缺失 work_item_id、未被软删除的存量 Problem。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Problem, error) {
	q := client.Problem.Query().
		Where(problem.WorkItemIDIsNil(), problem.DeletedAtIsNil())
	if tenantID > 0 {
		q = q.Where(problem.TenantID(tenantID))
	}
	return q.All(ctx)
}

// backfillOne 在一个数据库事务内为一条 Problem 新建 tickets 行（record_class="problem"，
// 创建后不可变）并回填 problems.work_item_id，然后把这条 Problem 通过旧 ent 多对多 edge
// 关联的普通工单迁移成等价的 WorkItemRelation 行。返回成功迁移的关联条数。用
// Where(problem.WorkItemIDIsNil()) 作为更新条件，保证并发运行/重复运行时不会对同一条
// Problem 回填两次。
func backfillOne(ctx context.Context, client *ent.Client, prob *ent.Problem) (int, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("开启事务失败: %w", err)
	}
	rollback := func(cause error) (int, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return 0, fmt.Errorf("%w（回滚也失败: %v）", cause, rbErr)
		}
		return 0, cause
	}

	ticketNumber, err := generateBackfillTicketNumber(ctx, tx.Client(), prob.CreatedAt)
	if err != nil {
		return rollback(fmt.Errorf("生成工单编号失败: %w", err))
	}

	now := time.Now()
	workItem, err := tx.Ticket.Create().
		SetTitle(prob.Title).
		SetDescription(prob.Description).
		SetType("problem").
		SetRecordClass("problem").
		SetPriority(prob.Priority).
		SetTicketNumber(ticketNumber).
		SetRequesterID(prob.CreatedBy).
		SetTenantID(prob.TenantID).
		SetCreatedAt(prob.CreatedAt).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("创建 WorkItem 失败: %w", err))
	}

	affected, err := tx.Problem.Update().
		Where(problem.ID(prob.ID), problem.TenantID(prob.TenantID), problem.WorkItemIDIsNil()).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("回填 work_item_id 失败: %w", err))
	}
	if affected == 0 {
		// 条件更新 0 行说明这条 Problem 在本工具查出候选之后、写入之前已经被并发处理
		// 补上了 work_item_id——不是错误，回滚这次多余的 WorkItem 创建，避免留下一条
		// 没有 Problem 指向它的孤儿 tickets 行。
		return rollback(fmt.Errorf("问题 %d 的 work_item_id 已被并发回填，跳过并回滚本次新建的 WorkItem", prob.ID))
	}

	// 迁移旧的 Problem<->Ticket 多对多 edge 到 WorkItemRelation。
	linkedTickets, err := tx.Problem.QueryTickets(prob).All(ctx)
	if err != nil {
		return rollback(fmt.Errorf("查询旧关联工单失败: %w", err))
	}
	migrated := 0
	for _, t := range linkedTickets {
		exists, err := tx.WorkItemRelation.Query().
			Where(
				workitemrelation.TenantID(prob.TenantID),
				workitemrelation.SourceWorkItemID(workItem.ID),
				workitemrelation.TargetWorkItemID(t.ID),
				workitemrelation.RelationType(problemTicketRelationType),
				workitemrelation.DeletedAtIsNil(),
			).
			Exist(ctx)
		if err != nil {
			return rollback(fmt.Errorf("检查已存在的 WorkItemRelation 失败: %w", err))
		}
		if exists {
			continue
		}
		_, err = tx.WorkItemRelation.Create().
			SetTenantID(prob.TenantID).
			SetSourceWorkItemID(workItem.ID).
			SetTargetWorkItemID(t.ID).
			SetRelationType(problemTicketRelationType).
			SetCreatedByID(prob.CreatedBy).
			SetCreatedAt(prob.CreatedAt).
			Save(ctx)
		if err != nil {
			return rollback(fmt.Errorf("迁移工单关联到 WorkItemRelation 失败 (ticket_id=%d): %w", t.ID, err))
		}
		migrated++
	}

	if err := tx.Commit(); err != nil {
		return rollback(fmt.Errorf("提交事务失败: %w", err))
	}
	return migrated, nil
}

// generateBackfillTicketNumber 复用与 handlers/problem.EntRepository.
// generateWorkItemTicketNumber 相同的编号格式（TKT-YYYYMM-NNNNNN）与全局（不区分租户）
// 计数维度——tickets.ticket_number 是全局唯一索引，按租户维度计数会在不同租户同月第一次
// 建单时互相撞号（这是 cmd/backfill_incident_work_item 里
// generateBackfillTicketNumber 和 IncidentService.generateWorkItemTicketNumber 都存在、
// 但不在本次任务允许修改范围内的同类缺陷，已在交付说明里列出）。取 Problem 自己的创建
// 时间算年月，而不是运行工具时的当前时间——回填的 WorkItem 编号应该落在 Problem 实际
// 创建的月份。同一事务内串行处理，不需要 Redis 原子计数器；不同事务之间仍受
// ticket_number 的唯一索引兜底保护，撞号会在 Save 时报错，交由上层 backfillOne 整体回滚。
func generateBackfillTicketNumber(ctx context.Context, client *ent.Client, createdAt time.Time) (string, error) {
	year, month := createdAt.Year(), int(createdAt.Month())
	prefix := fmt.Sprintf("TKT-%04d%02d-", year, month)

	existing, err := client.Ticket.Query().
		Where(entticket.TicketNumberHasPrefix(prefix)).
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
