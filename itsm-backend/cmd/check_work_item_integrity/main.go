// check_work_item_integrity 是 Wave 1（统一 Work Item 领域模型重构）新增的常驻可重复运行的
// 数据完整性检查工具，不是一次性迁移脚本——设计文档 §18.3-9。
//
// 检查内容：一条 tickets 行的 record_class 若不是 "generic"，就应该有且仅有一条对应专业
// 扩展表（incidents/problems/changes）的行通过 work_item_id 指回它；反之，一条专业扩展表
// 行的 work_item_id 若指向某个 tickets.id，那条 ticket 的 record_class 应该跟这张扩展表
// 匹配。任何一边对不上都报告为异常，不自动修复——自动修复需要业务判断（比如该建一条缺失的
// 专业记录，还是该纠正 record_class），这个工具只负责发现，不负责决定怎么修。
//
// 用法：
//
//	go run ./cmd/check_work_item_integrity -tenant-id=0   # 0 表示检查所有租户
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

type mismatch struct {
	kind        string
	ticketID    int
	tenantID    int
	recordClass string
	detail      string
}

func main() {
	tenantID := flag.Int("tenant-id", 0, "只检查指定租户（<=0 表示检查所有租户）")
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
		"ops:check_work_item_integrity",
		"WorkItem 重构：record_class 与专业扩展表 work_item_id 一致性检查",
	)

	mismatches, err := findMismatches(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("检查失败", "error", err)
	}

	if len(mismatches) == 0 {
		sugar.Infow("未发现不一致", "tenant_id", *tenantID)
		return
	}

	sugar.Warnw("发现不一致记录", "count", len(mismatches), "tenant_id", *tenantID)
	for _, m := range mismatches {
		sugar.Warnw("不一致", "kind", m.kind, "ticket_id", m.ticketID, "tenant_id", m.tenantID,
			"record_class", m.recordClass, "detail", m.detail)
	}
	os.Exit(1)
}

func findMismatches(ctx context.Context, client *ent.Client, tenantID int) ([]mismatch, error) {
	var out []mismatch

	// 1) record_class != generic 但找不到对应专业扩展记录。
	q := client.Ticket.Query().Where(ticket.RecordClassNEQ("generic"))
	if tenantID > 0 {
		q = q.Where(ticket.TenantID(tenantID))
	}
	tickets, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询非 generic 工单失败: %w", err)
	}
	for _, t := range tickets {
		var exists bool
		var checkErr error
		switch t.RecordClass {
		case "incident":
			exists, checkErr = client.Incident.Query().Where(incident.WorkItemID(t.ID)).Exist(ctx)
		case "problem":
			exists, checkErr = client.Problem.Query().Where(problem.WorkItemID(t.ID)).Exist(ctx)
		case "change_request":
			exists, checkErr = client.Change.Query().Where(change.WorkItemID(t.ID)).Exist(ctx)
		case "service_request_item", "catalog_task":
			// 这两类在 Wave 1 阶段还没有对应的 work_item_id 外键（ServiceRequest 沿用
			// 既有 ticket_id 列，CatalogTask 是 Wave 2 才新建的表），暂不检查，
			// 留给各自的 Wave 2 任务包。
			continue
		default:
			// 落到这里说明 record_class 是一个本工具不认识的值。以前这里跟上面两类
			// 一样直接 continue，等于把"写错的 record_class"和"还没实现的 record_class"
			// 混为一谈静默放行——record_class 是整个 WorkItem 模型的分派键，一个拼错的
			// 值会让所有按它分派的逻辑（含本工具第 1 段检查）悄悄跳过这条记录。
			// 因此单独报一种不一致，让它可见。
			out = append(out, mismatch{
				kind: "unknown_record_class", ticketID: t.ID, tenantID: t.TenantID,
				recordClass: t.RecordClass,
				detail: fmt.Sprintf("record_class=%q 不在已知取值内（generic/incident/problem/change_request/service_request_item/catalog_task）",
					t.RecordClass),
			})
			continue
		}
		if checkErr != nil {
			return nil, fmt.Errorf("查询 ticket %d 的专业扩展记录失败: %w", t.ID, checkErr)
		}
		if !exists {
			out = append(out, mismatch{
				kind: "missing_extension", ticketID: t.ID, tenantID: t.TenantID,
				recordClass: t.RecordClass,
				detail:      fmt.Sprintf("record_class=%s 但找不到 work_item_id=%d 的专业扩展记录", t.RecordClass, t.ID),
			})
		}
	}

	// 2) 专业扩展记录的 work_item_id 指向的 ticket 的 record_class 对不上。
	incidents, err := queryScoped(ctx, client.Incident.Query(), tenantID)
	if err != nil {
		return nil, err
	}
	for _, i := range incidents {
		if err := checkBackref(ctx, client, i.WorkItemID, i.TenantID, "incident", &out); err != nil {
			return nil, err
		}
	}
	problems, err := queryScopedProblem(ctx, client, tenantID)
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		if err := checkBackref(ctx, client, p.WorkItemID, p.TenantID, "problem", &out); err != nil {
			return nil, err
		}
	}
	changes, err := queryScopedChange(ctx, client, tenantID)
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		if err := checkBackref(ctx, client, c.WorkItemID, c.TenantID, "change_request", &out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func checkBackref(ctx context.Context, client *ent.Client, workItemID, tenantID int, expectedClass string, out *[]mismatch) error {
	t, err := client.Ticket.Get(ctx, workItemID)
	if err != nil {
		if ent.IsNotFound(err) {
			*out = append(*out, mismatch{
				kind: "dangling_work_item_id", ticketID: workItemID, tenantID: tenantID,
				recordClass: expectedClass,
				detail:      fmt.Sprintf("work_item_id=%d 指向的 ticket 不存在", workItemID),
			})
			return nil
		}
		return fmt.Errorf("查询 ticket %d 失败: %w", workItemID, err)
	}
	// work_item_id 没有 DB 层外键约束（纯 int 列 + 唯一索引，见 ent/schema/incident.go 等），
	// 理论上不能排除专业扩展记录因数据错误而指向别的租户的 ticket——即便应用层始终在同一事务内
	// 以相同 tenant_id 创建两边的记录。跨租户指向本身就是一种需要报告的不一致，且比
	// record_class 不匹配更严重，所以单独作为一种 mismatch 上报，而不是被 record_class 检查掩盖。
	if t.TenantID != tenantID {
		*out = append(*out, mismatch{
			kind: "tenant_mismatch", ticketID: workItemID, tenantID: tenantID,
			recordClass: expectedClass,
			detail: fmt.Sprintf("专业扩展记录属于租户 %d，但 work_item_id=%d 指向的 ticket 属于租户 %d",
				tenantID, workItemID, t.TenantID),
		})
	}
	if t.RecordClass != expectedClass {
		*out = append(*out, mismatch{
			kind: "record_class_mismatch", ticketID: workItemID, tenantID: tenantID,
			recordClass: t.RecordClass,
			detail:      fmt.Sprintf("专业扩展记录期望 record_class=%s，实际是 %s", expectedClass, t.RecordClass),
		})
	}
	return nil
}

func queryScoped(ctx context.Context, q *ent.IncidentQuery, tenantID int) ([]*ent.Incident, error) {
	if tenantID > 0 {
		q = q.Where(incident.TenantID(tenantID))
	}
	return q.All(ctx)
}

func queryScopedProblem(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Problem, error) {
	q := client.Problem.Query()
	if tenantID > 0 {
		q = q.Where(problem.TenantID(tenantID))
	}
	return q.All(ctx)
}

func queryScopedChange(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.Change, error) {
	q := client.Change.Query()
	if tenantID > 0 {
		q = q.Where(change.TenantID(tenantID))
	}
	return q.All(ctx)
}
