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
	"itsm-backend/ent/systemconfig"

	"go.uber.org/zap"
)

const legacyApprovalWriteLockedKey = "legacyApprovalWriteLocked"

func main() {
	tenantID := flag.Int("tenant-id", 0, "要锁定/解锁的租户ID（必填）")
	unlock := flag.Bool("unlock", false, "解锁而不是锁定（默认锁定）")
	flag.Parse()

	if *tenantID <= 0 {
		fmt.Fprintln(os.Stderr, "必须传 -tenant-id，且必须大于 0")
		os.Exit(1)
	}

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
		"ops:lock_legacy_approvals",
		"toggle the legacy ApprovalWorkflow config write lock for one tenant",
	)

	value := "true"
	action := "锁定"
	if *unlock {
		value = "false"
		action = "解锁"
	}

	existing, err := client.SystemConfig.Query().
		Where(
			systemconfig.KeyEQ(legacyApprovalWriteLockedKey),
			systemconfig.TenantIDEQ(*tenantID),
			systemconfig.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		sugar.Fatalw("query existing lock state", "tenant_id", *tenantID, "error", err)
	}

	if existing != nil {
		_, err = existing.Update().
			SetValue(value).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		if err != nil {
			sugar.Fatalw("update lock state", "tenant_id", *tenantID, "error", err)
		}
	} else {
		_, err = client.SystemConfig.Create().
			SetKey(legacyApprovalWriteLockedKey).
			SetValue(value).
			SetValueType("boolean").
			SetCategory("approval").
			SetDescription("旧审批工作流配置（ApprovalWorkflow CRUD）是否已下线只读——true 表示 Create/Update/Patch/Delete 一律拒绝").
			SetTenantID(*tenantID).
			SetCreatedAt(time.Now()).
			SetUpdatedAt(time.Now()).
			Save(ctx)
		if err != nil {
			sugar.Fatalw("create lock state", "tenant_id", *tenantID, "error", err)
		}
	}

	sugar.Infow("完成", "tenant_id", *tenantID, "action", action, "locked", !*unlock)
}
