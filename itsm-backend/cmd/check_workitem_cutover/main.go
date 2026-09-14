// check_workitem_cutover 是 Wave 2 C1 的只读切换预检 CLI。
//
// 契约：
//   - 只读：在只读事务内调用 service/workitemcutover.Inspect，退出前一律 Rollback；
//     绝不取消、更新、删除或重新触发任何实例。
//   - exit 0 表示可以切换；exit 2 表示存在依赖阻塞；exit 1 表示工具自身失败。
//   - 输出只包含计数、非敏感 ID（实例/回调/binding ID）与阻塞原因。
//
// 用法：
//
//	go run ./cmd/check_workitem_cutover                 # 检查所有租户
//	go run ./cmd/check_workitem_cutover -tenant-id=7    # 只检查租户 7
//	go run ./cmd/check_workitem_cutover -json           # 机器可读输出
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/service/workitemcutover"

	"go.uber.org/zap"
)

const (
	exitSwitchable = 0
	exitError      = 1
	exitBlocked    = 2
)

func main() {
	tenantID := flag.Int("tenant-id", 0, "只检查指定租户（<=0 表示检查所有租户）")
	jsonOut := flag.Bool("json", false, "以 JSON 输出")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(exitError)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(exitError)
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
		"ops:check_workitem_cutover",
		"WorkItem 收敛：BPMN 业务身份切换（旧词表 -> 规范 recordClass）只读预检",
	)

	// Read-only repeatable-read snapshot: the preflight must observe one consistent
	// state and must not be able to write.
	tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin read-only transaction: %v\n", err)
		os.Exit(exitError)
	}
	defer func() { _ = tx.Rollback() }()

	rep, err := workitemcutover.Inspect(ctx, tx, *tenantID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
		os.Exit(exitError)
	}
	_ = tx.Rollback()

	if *jsonOut {
		encoded, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
			os.Exit(exitError)
		}
		fmt.Println(string(encoded))
	} else {
		printReport(rep)
	}

	if rep.Switchable {
		os.Exit(exitSwitchable)
	}
	os.Exit(exitBlocked)
}

func printReport(rep workitemcutover.Report) {
	mode := "可切换"
	if !rep.Switchable {
		mode = "不可切换"
	}
	fmt.Printf("WorkItem 流程身份切换预检（tenant=%d）：%s\n", rep.TenantID, mode)
	fmt.Printf("已检查：process_instances=%d, process_callback_outbox=%d, process_bindings=%d\n",
		rep.Checked.ProcessInstances, rep.Checked.CallbackOutbox, rep.Checked.Bindings)
	for _, item := range rep.Blockers {
		fmt.Printf("[阻塞] %s 数量=%d：%s\n", item.Kind, item.Count, item.Reason)
		if len(item.SampleIDs) > 0 {
			fmt.Printf("        样例 ID：%v\n", item.SampleIDs)
		}
		if item.Truncated {
			fmt.Printf("        （已按 %d 行上限截断，实际可能更多）\n", workitemcutover.ScanLimit)
		}
	}
	for _, item := range rep.Informational {
		fmt.Printf("[信息] %s 数量=%d：%s\n", item.Kind, item.Count, item.Reason)
	}
	if rep.Switchable {
		fmt.Println("结论：无活跃旧依赖，可以切换（exit 0）")
		return
	}
	fmt.Println("结论：存在阻塞，切换必须失败（exit 2）")
}
