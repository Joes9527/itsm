// backfill_servicecatalog_target_class 是 Wave 2（统一 Work Item 领域模型重构 ·
// ServiceRequest 层级规范化）用的一次性数据回填工具，不是常规业务命令。
//
// 背景：ServiceCatalog.target_class 这一列在 Wave 1 就已经加进 ent schema，但在这次
// ServiceRequest 迁移之前一直是"加了列没人写"——存量行全部是空值，
// handlers/service_request 的路由判断（isIncidentCatalog/mapTargetClassToTicketType）
// 读的是 itsm_type。这次迁移把路由判断改成读 target_class（itsm_type/target_class 不
// 允许两个字段并存做路由依据），所以存量行必须先跑这个工具补上 target_class，再上线新
// 路由代码——跟 Incident/Problem/Change 三次迁移要求先跑各自 backfill 命令再上线新路由
// 代码是同一个部署顺序模式，不是本工具独有的额外要求。
//
// 映射规则（与 handlers/service_catalog.ComputeTargetClass 保持同一份逻辑，这里直接调用，
// 不重新实现一遍，避免两处映射表漂移）：
//
//	itsm_type="Incident" → target_class="incident"
//	itsm_type="Change"   → target_class="change_request"
//	其余（"Request"、空值、未知取值）→ target_class="service_request_item"（fail-safe 默认，
//	对齐 ent schema itsm_type 字段的 Default("Request")）
//
// 这只是一次性数据回填，不涉及运行中 BPMN 流程实例迁移（跟 Incident/Problem/Change 那三个
// backfill 工具不同，ServiceCatalog 本身不是 WorkItem，不需要新建 tickets 行/事务化改造），
// 所以实现明显更简单：逐行按条件 UPDATE 即可，不需要事务化创建配对记录。
//
// 用法：
//
//	go run ./cmd/backfill_servicecatalog_target_class -dry-run=true               # 预览，不写入
//	go run ./cmd/backfill_servicecatalog_target_class -dry-run=false              # 全部租户实际回填
//	go run ./cmd/backfill_servicecatalog_target_class -dry-run=false -tenant-id=3 # 只处理指定租户
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
	"itsm-backend/ent/servicecatalog"
	"itsm-backend/handlers/service_catalog"

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
		"ops:backfill_servicecatalog_target_class",
		"WorkItem Wave 2：给存量 ServiceCatalog 按 itsm_type 回填 target_class",
	)

	candidates, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填服务目录项失败", "error", err)
	}

	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的服务目录项", "tenant_id", *tenantID)
		return
	}

	sugar.Infow("找到待回填服务目录项", "count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun)
	for _, c := range candidates {
		sugar.Infow("候选目录项",
			"catalog_id", c.ID, "tenant_id", c.TenantID, "name", c.Name,
			"itsm_type", c.ItsmType, "computed_target_class", service_catalog.ComputeTargetClass(c.ItsmType))
	}

	if *dryRun {
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	succeeded, failed := 0, 0
	for _, c := range candidates {
		if err := backfillOne(ctx, client, c); err != nil {
			sugar.Errorw("回填失败", "catalog_id", c.ID, "tenant_id", c.TenantID, "error", err)
			failed++
			continue
		}
		sugar.Infow("回填成功", "catalog_id", c.ID, "tenant_id", c.TenantID)
		succeeded++
	}

	sugar.Infow("回填完成", "succeeded", succeeded, "failed", failed, "total", len(candidates))
	if failed > 0 {
		os.Exit(1)
	}
}

// findCandidates 返回 target_class 为空（NULL 或空字符串——ent Optional() 不带 Nillable()
// 的字符串列在 Go 侧统一解码为 ""，但底层 DB 列允许 NULL，两种取值都要覆盖）的存量
// ServiceCatalog。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.ServiceCatalog, error) {
	q := client.ServiceCatalog.Query().
		Where(servicecatalog.Or(servicecatalog.TargetClassIsNil(), servicecatalog.TargetClassEQ("")))
	if tenantID > 0 {
		q = q.Where(servicecatalog.TenantID(tenantID))
	}
	return q.All(ctx)
}

// backfillOne 按条件 UPDATE 单条记录的 target_class。条件里带上
// Or(TargetClassIsNil(), TargetClassEQ(""))，保证并发运行/重复运行时不会覆盖已经被
// 其他路径（例如 handlers/service_catalog 的 Update 自愈同步）写过的值——幂等。
func backfillOne(ctx context.Context, client *ent.Client, cat *ent.ServiceCatalog) error {
	affected, err := client.ServiceCatalog.Update().
		Where(
			servicecatalog.ID(cat.ID),
			servicecatalog.TenantID(cat.TenantID),
			servicecatalog.Or(servicecatalog.TargetClassIsNil(), servicecatalog.TargetClassEQ("")),
		).
		SetTargetClass(service_catalog.ComputeTargetClass(cat.ItsmType)).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("更新 target_class 失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("服务目录项 %d 的 target_class 已被并发回填，跳过", cat.ID)
	}
	return nil
}
