// backfill_change_work_item 是 Wave 2（统一 Work Item 领域模型重构 · Change 迁移）用的
// 一次性迁移工具，不是常规业务命令。它还处理两件 Change 域特有的风险点：
//
//  1. Change 有一套已经在生产运行的 CAB 审批 BPMN 桥接机制（Track4），businessKey 采用
//     "change:{id}" 约定，之前用 Change 自己的主键（changes.id）构造。这次迁移把
//     handlers/change/service.go 里 businessKey 的身份收敛为 WorkItem ID（tickets.id），
//     意味着任何一条正在运行（status="running"）的 ProcessInstance，如果它的 business_key
//     还是旧格式 "change:{旧ChangeID}"，代码切换之后就再也查不到了——CAB 审批会静默失效
//     （审批人点"批准"直接报错）。所以这个工具必须在给 Change 补建 WorkItem 的同一个事务
//     里，原地把这条 Change 名下匹配旧格式且仍在运行的 ProcessInstance 的
//     business_key/business_id 改写成新格式，不能只回填 work_item_id。
//  2. Change.related_tickets 是自由文本 JSON 数组（存的是 ticket_number 字符串，不是
//     数据库主键 ID——见 dto.CreateChangeRequest.RelatedTickets 的字段注释），迁移到
//     WorkItemRelation（relation_type="related_to"）时需要按 ticket_number 反查真实工单。
//     查不到的字符串（拼写错误、已删除工单、非法格式）跳过并计数，不阻塞整个回填——这是
//     业务判断，不是 fail closed 的安全边界，跟 handlers/change/repository_impl.go 的
//     resolveTicketNumbers 用同一套处理原则。
//
// WorkItem 创建 + ProcessInstance 迁移 + related_tickets 迁移这三步在同一个数据库事务里
// 完成，任一步失败整体回滚，不会留下"WorkItem 建好了但运行中流程还指向旧 businessKey"或
// "关联工单迁移了一半"的中间态。
//
// 不处理的事：
//   - 不迁移已经 completed/terminated 的历史 ProcessInstance 的 business_key/business_id。
//     它们不会再产生新的审批决策查询；EntRepository.GetApprovalHistory 对历史
//     ProcessApprovalDecision.BusinessID 做了新旧两种格式的兼容查询（旧格式=changeID 的
//     十进制形式，新格式=workItemID 的十进制形式），不迁移历史实例不会让历史审批记录从
//     审批历史里消失。只处理 status="running" 的实例，这是任务书明确的范围。
//   - 不把 business_type 从 "change" 改成 recordClass 词表里的 "change_request"——那是
//     独立、更大范围的收敛项（见 ent/schema/process_instance.go 的字段注释），本次任务
//     只处理 businessKey/businessId 的身份收敛（Change 自己的主键 -> WorkItem ID），不在
//     这次任务范围内触碰 business_type 的取值本身。
//   - 不处理 Change 没有 ent 软删除概念（ent/schema/change.go 没有 deleted_at 字段），
//     这里不需要过滤已删除记录。
//
// 用法：
//
//	go run ./cmd/backfill_change_work_item -dry-run=true               # 预览，不写入
//	go run ./cmd/backfill_change_work_item -dry-run=false              # 全部租户实际回填
//	go run ./cmd/backfill_change_work_item -dry-run=false -tenant-id=3 # 只处理指定租户
//
// ⚠️ 运维顺序：如果存量数据里还有 cmd/backfill_legacy_pending_changes 的候选（Track4 上线
// 切换时刻遗留、旧审批链下提交到 pending 但从未有 BPMN 实例的变更），必须先跑这个工具，
// 再跑 cmd/backfill_legacy_pending_changes——后者调用的
// handlers/change.Service.BackfillLegacyPendingChange 现在要求 Change 已经有关联的
// WorkItem（resolveWorkItemID fail closed），顺序反了会导致那批变更的回填全部失败（报错
// 会明确提示"请先运行 cmd/backfill_change_work_item"，不是静默失败或产生错误数据）。
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
	changeent "itsm-backend/ent/change"
	"itsm-backend/ent/processinstance"
	entticket "itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemrelation"

	"go.uber.org/zap"
)

// changeTicketRelationType 必须与 handlers/change/repository_impl.go 里的同名常量取值
// 一致（各自定义，避免让这个一次性迁移工具依赖业务包内部符号，同
// cmd/backfill_problem_work_item 的做法）。
const changeTicketRelationType = "related_to"

// backfillResult 记录单条 Change 回填的结果，用于汇总统计和 dry-run 之外的成功日志。
type backfillResult struct {
	workItemID           int
	migratedInstance     bool
	migratedRelations    int
	skippedTicketNumbers []string
}

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
		"ops:backfill_change_work_item",
		"WorkItem Wave 2：给切换前创建的存量 Change 补建 tickets 行、回填 work_item_id，把运行中流程实例的 businessKey 从旧格式迁移到新格式，并把 related_tickets 迁移到 WorkItemRelation",
	)

	candidates, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填变更失败", "error", err)
	}

	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的变更", "tenant_id", *tenantID)
		return
	}

	sugar.Infow("找到待回填变更", "count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun)
	for _, c := range candidates {
		sugar.Infow("候选变更", "change_id", c.ID, "tenant_id", c.TenantID, "title", c.Title, "status", c.Status)
	}

	if *dryRun {
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	succeeded, failed := 0, 0
	migratedInstances, migratedRelationsTotal, skippedRelationsTotal := 0, 0, 0
	for _, c := range candidates {
		result, err := backfillOne(ctx, client, c)
		if err != nil {
			sugar.Errorw("回填失败", "change_id", c.ID, "tenant_id", c.TenantID, "error", err)
			failed++
			continue
		}
		sugar.Infow("回填成功",
			"change_id", c.ID, "tenant_id", c.TenantID,
			"work_item_id", result.workItemID,
			"migrated_running_instance", result.migratedInstance,
			"migrated_related_ticket_relations", result.migratedRelations,
			"skipped_related_ticket_numbers", result.skippedTicketNumbers)
		succeeded++
		if result.migratedInstance {
			migratedInstances++
		}
		migratedRelationsTotal += result.migratedRelations
		skippedRelationsTotal += len(result.skippedTicketNumbers)
	}

	sugar.Infow("回填完成",
		"succeeded", succeeded, "failed", failed, "total", len(candidates),
		"migrated_running_instances_total", migratedInstances,
		"migrated_related_ticket_relations_total", migratedRelationsTotal,
		"skipped_related_ticket_numbers_total", skippedRelationsTotal)
	if failed > 0 {
		os.Exit(1)
	}
}

// findCandidates 返回缺失 work_item_id 的存量 Change。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Change, error) {
	q := client.Change.Query().Where(changeent.WorkItemIDIsNil())
	if tenantID > 0 {
		q = q.Where(changeent.TenantID(tenantID))
	}
	return q.All(ctx)
}

// backfillOne 在一个数据库事务内为一条 Change 新建 tickets 行（record_class="change_request"，
// 创建后不可变）并回填 changes.work_item_id，然后：
//   - 把这条 Change 名下匹配旧 businessKey 格式且仍在运行的 ProcessInstance 原地迁移到新格式；
//   - 把 related_tickets 里能解析出真实工单的编号迁移成 WorkItemRelation。
//
// 用 Where(changeent.WorkItemIDIsNil()) 作为更新条件，保证并发运行/重复运行时不会对同一条
// Change 回填两次。
func backfillOne(ctx context.Context, client *ent.Client, c *ent.Change) (*backfillResult, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	rollback := func(cause error) (*backfillResult, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("%w（回滚也失败: %v）", cause, rbErr)
		}
		return nil, cause
	}

	ticketNumber, err := generateBackfillTicketNumber(ctx, tx.Client(), c.CreatedAt)
	if err != nil {
		return rollback(fmt.Errorf("生成工单编号失败: %w", err))
	}

	now := time.Now()
	workItem, err := tx.Ticket.Create().
		SetTitle(c.Title).
		SetDescription(c.Description).
		SetType("change").
		SetRecordClass("change_request").
		SetPriority(c.Priority).
		SetTicketNumber(ticketNumber).
		SetRequesterID(c.CreatedBy).
		SetTenantID(c.TenantID).
		SetCreatedAt(c.CreatedAt).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("创建 WorkItem 失败: %w", err))
	}

	affected, err := tx.Change.Update().
		Where(changeent.ID(c.ID), changeent.TenantID(c.TenantID), changeent.WorkItemIDIsNil()).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	if err != nil {
		return rollback(fmt.Errorf("回填 work_item_id 失败: %w", err))
	}
	if affected == 0 {
		// 条件更新 0 行说明这条 Change 在本工具查出候选之后、写入之前已经被并发处理
		// 补上了 work_item_id——不是错误，回滚这次多余的 WorkItem 创建，避免留下一条
		// 没有 Change 指向它的孤儿 tickets 行。
		return rollback(fmt.Errorf("变更 %d 的 work_item_id 已被并发回填，跳过并回滚本次新建的 WorkItem", c.ID))
	}

	migratedInstance, err := migrateRunningProcessInstance(ctx, tx.Client(), c.ID, c.TenantID, workItem.ID)
	if err != nil {
		return rollback(fmt.Errorf("迁移运行中流程实例失败: %w", err))
	}

	migratedRelations, skipped, err := migrateRelatedTickets(ctx, tx.Client(), c, workItem.ID)
	if err != nil {
		return rollback(fmt.Errorf("迁移相关工单关联失败: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return rollback(fmt.Errorf("提交事务失败: %w", err))
	}
	return &backfillResult{
		workItemID:           workItem.ID,
		migratedInstance:     migratedInstance,
		migratedRelations:    migratedRelations,
		skippedTicketNumbers: skipped,
	}, nil
}

// migrateRunningProcessInstance 把这条 Change 名下、business_key 仍是旧格式
// "change:{旧ChangeID}" 且 status="running" 的 ProcessInstance 原地迁移到新格式
// "change:{workItemID}"，同步把结构化的 business_id 列也改成 workItemID（business_type
// 保持不变，仍是 "change"——见文件顶部"不处理的事"）。找不到匹配的运行中实例不是错误
// （大多数 Change 在回填时刻已经不在审批流程中），返回 false。
func migrateRunningProcessInstance(ctx context.Context, client *ent.Client, changeID, tenantID, workItemID int) (bool, error) {
	oldBusinessKey := fmt.Sprintf("change:%d", changeID)
	newBusinessKey := fmt.Sprintf("change:%d", workItemID)

	instance, err := client.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(oldBusinessKey),
			processinstance.TenantID(tenantID),
			processinstance.Status("running"),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("查询运行中流程实例失败: %w", err)
	}

	if _, err := client.ProcessInstance.UpdateOneID(instance.ID).
		Where(processinstance.TenantID(tenantID)).
		SetBusinessKey(newBusinessKey).
		SetBusinessID(workItemID).
		Save(ctx); err != nil {
		return false, fmt.Errorf("更新流程实例 businessKey 失败: %w", err)
	}
	return true, nil
}

// migrateRelatedTickets 把 c.RelatedTickets（自由文本工单编号）解析成当前租户下真实存在的
// 工单，写入 WorkItemRelation（relation_type="related_to"，source=新建的 workItemID）。
// 查不到的编号跳过并返回，不阻塞整个回填。
func migrateRelatedTickets(ctx context.Context, client *ent.Client, c *ent.Change, workItemID int) (migrated int, skipped []string, err error) {
	if len(c.RelatedTickets) == 0 {
		return 0, nil, nil
	}
	seen := make(map[string]struct{}, len(c.RelatedTickets))
	ordered := make([]string, 0, len(c.RelatedTickets))
	for _, n := range c.RelatedTickets {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		ordered = append(ordered, n)
	}
	if len(ordered) == 0 {
		return 0, nil, nil
	}

	found, err := client.Ticket.Query().
		Where(entticket.TicketNumberIn(ordered...), entticket.TenantID(c.TenantID)).
		All(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("查询相关工单失败: %w", err)
	}
	byNumber := make(map[string]int, len(found))
	for _, t := range found {
		byNumber[t.TicketNumber] = t.ID
	}

	for _, n := range ordered {
		targetID, ok := byNumber[n]
		if !ok {
			skipped = append(skipped, n)
			continue
		}
		exists, existErr := client.WorkItemRelation.Query().
			Where(
				workitemrelation.TenantID(c.TenantID),
				workitemrelation.SourceWorkItemID(workItemID),
				workitemrelation.TargetWorkItemID(targetID),
				workitemrelation.RelationType(changeTicketRelationType),
				workitemrelation.DeletedAtIsNil(),
			).
			Exist(ctx)
		if existErr != nil {
			return migrated, skipped, fmt.Errorf("检查已存在的 WorkItemRelation 失败: %w", existErr)
		}
		if exists {
			continue
		}
		if _, createErr := client.WorkItemRelation.Create().
			SetTenantID(c.TenantID).
			SetSourceWorkItemID(workItemID).
			SetTargetWorkItemID(targetID).
			SetRelationType(changeTicketRelationType).
			SetCreatedByID(c.CreatedBy).
			SetCreatedAt(c.CreatedAt).
			Save(ctx); createErr != nil {
			return migrated, skipped, fmt.Errorf("迁移工单关联到 WorkItemRelation 失败 (ticket_id=%d): %w", targetID, createErr)
		}
		migrated++
	}
	return migrated, skipped, nil
}

// generateBackfillTicketNumber 复用与 handlers/change.EntRepository.generateWorkItemTicketNumber
// 相同的编号格式
// （TKT-YYYYMM-NNNNNN）与按租户维度计数的已知限制（tickets.ticket_number 是全局唯一索引，
// 详见 handlers/change/repository_impl.go 同名函数的注释——这是一个跨 Ticket/Incident/
// Problem/Change 共享的既有缺陷，不在本次任务范围内修）。取 Change 自己的创建时间算年月，
// 而不是运行工具时的当前时间。
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
