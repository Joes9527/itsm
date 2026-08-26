// backfill_legacy_pending_changes 是 Track4（变更审批状态机迁移到 BPMN）上线切换时刻
// 用的一次性迁移工具，不是常规业务命令。
//
// 背景：迁移前用旧审批链流程提交、还处于 pending 状态的变更，没有对应的 BPMN 流程实例；
// 迁移后 approve/reject 都要求存在运行中的流程实例，这批变更会永久卡住，唯一的"恢复"
// 路径是直接取消变更——不是走正常审批流程。这个工具找出这批变更，用系统身份重放一次
// "提交审批"（等价于 SubmitChange，但跳过 draft 状态门槛），让它们接入 BPMN，正常推进到
// CAB 审批节点。
//
// 用法（跟 cmd/provision_tenant 等其它一次性运维工具一样，直接 go run，不需要 build tag）：
//
//	go run ./cmd/backfill_legacy_pending_changes -dry-run=true                 # 预览，不写入
//	go run ./cmd/backfill_legacy_pending_changes -dry-run=false                 # 全部租户实际执行
//	go run ./cmd/backfill_legacy_pending_changes -dry-run=false -tenant-id=3    # 只处理指定租户
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
	"itsm-backend/ent/processinstance"
	changedomain "itsm-backend/handlers/change"
	"itsm-backend/service"

	"go.uber.org/zap"
)

// candidate 是一个待回填的变更：status=pending 且没有任何（不限状态）对应的
// ProcessInstance——如果有实例（哪怕是 terminated），说明它已经走过 Track4 的新流程，
// 不需要也不应该重复回填。
type candidate struct {
	id       int
	tenantID int
	title    string
	typ      string
}

func main() {
	tenantID := flag.Int("tenant-id", 0, "只处理指定租户（<=0 表示处理所有租户）")
	dryRun := flag.Bool("dry-run", true, "true 只打印候选列表，不实际执行；确认无误后用 -dry-run=false 真正回填")
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
		"ops:backfill_legacy_pending_changes",
		"给 Track4 上线切换时刻的存量 pending 变更补上缺失的 BPMN 流程实例",
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
		sugar.Infow("候选变更", "change_id", c.id, "tenant_id", c.tenantID, "title", c.title, "type", c.typ)
	}

	if *dryRun {
		sugar.Infow("dry-run 模式，未实际执行——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	processEngine := service.NewCustomProcessEngine(client, sugar)
	processTriggerService := service.NewProcessTriggerService(client, processEngine)
	repo := changedomain.NewEntRepository(client, database.GetRawDB())
	svc := changedomain.NewService(repo, client, sugar)
	svc.SetProcessTriggerService(processTriggerService)
	svc.SetProcessEngine(processEngine)

	succeeded, failed := 0, 0
	for _, c := range candidates {
		if err := svc.BackfillLegacyPendingChange(ctx, c.id, c.tenantID); err != nil {
			sugar.Errorw("回填失败", "change_id", c.id, "tenant_id", c.tenantID, "error", err)
			failed++
			continue
		}
		sugar.Infow("回填成功", "change_id", c.id, "tenant_id", c.tenantID)
		succeeded++
	}

	sugar.Infow("回填完成", "succeeded", succeeded, "failed", failed, "total", len(candidates))
	if failed > 0 {
		os.Exit(1)
	}
}

func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]candidate, error) {
	query := client.Change.Query().Where(change.Status("pending"))
	if tenantID > 0 {
		query = query.Where(change.TenantID(tenantID))
	}
	changes, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询 pending 变更失败: %w", err)
	}

	var candidates []candidate
	for _, c := range changes {
		businessKey := fmt.Sprintf("change:%d", c.ID)
		// 排除 terminated 实例：跟 handlers/change/service.go 的
		// BackfillLegacyPendingChange 保持一致的判断口径，否则一次失败的回填
		// 尝试（补偿 CancelProcess 留下 terminated 记录）会让这个变更从候选列表
		// 里永久消失，dry-run 也看不到它。
		exists, err := client.ProcessInstance.Query().
			Where(
				processinstance.BusinessKey(businessKey),
				processinstance.TenantID(c.TenantID),
				processinstance.StatusNEQ("terminated"),
			).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查变更 %d 是否已有流程实例失败: %w", c.ID, err)
		}
		if exists {
			continue
		}
		candidates = append(candidates, candidate{id: c.ID, tenantID: c.TenantID, title: c.Title, typ: c.Type})
	}
	return candidates, nil
}
