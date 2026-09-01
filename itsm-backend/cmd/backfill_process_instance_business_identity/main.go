// backfill_process_instance_business_identity 是 Wave 1（统一 Work Item 领域模型重构）
// 上线切换时刻用的一次性迁移工具，不是常规业务命令。
//
// 背景：Wave 1 之后 process_instances 有了结构化的 business_type/business_id 两列，
// StartProcess 会在创建实例时原子写入它们；bpmn_process_engine.go 的
// recordApprovalDecision 也改成直接读这两列，不再从 variables JSON 里现取。
// 但本次部署之前创建的实例行这两列是空的——它们只有 variables["business_type"]
// 和 business_key。于是这批仍在运行的老实例一旦走到审批节点，写出的
// process_approval_decisions 行就会带着空的 business_type/business_id，而下面三个
// 消费方依赖它们是对的：
//
//   - service/provisioning_service.go（据此判断是否放行开通动作）
//   - service/ticket_workflow_service.go（工单审批决策回查）
//   - handlers/change/repository_impl.go（变更审批决策回查）
//
// 这个工具把仍在 running/suspended 的存量实例的这两列补齐：优先用 variables 里的
// business_type/business_id，取不到再回退解析 business_key（约定格式 "{type}:{id}"，
// 见 BPMNApprovalBridge.findPendingBusinessTask 与 ProcessTriggerService）。
// suspended 实例会被 resume 回 running 继续产生审批决策，因此和 running 一起处理；
// 已经结束（completed/terminated）的实例不再产生新的审批决策，不需要回填。
//
// 用法（直接 go run，不需要 build tag）：
//
//	go run ./cmd/backfill_process_instance_business_identity -dry-run=true              # 预览，不写入
//	go run ./cmd/backfill_process_instance_business_identity -dry-run=false             # 全部租户实际回填
//	go run ./cmd/backfill_process_instance_business_identity -dry-run=false -tenant-id=3 # 只处理指定租户
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"

	"go.uber.org/zap"
)

// candidate 是一条待回填的流程实例：status=running 且 business_type/business_id
// 至少缺一个，同时能从 variables 或 business_key 推导出缺失的那部分。
type candidate struct {
	id           int
	tenantID     int
	instanceKey  string
	businessKey  string
	businessType string
	businessID   int
	// source 记录推导来源，dry-run 输出里能一眼看出这条是靠 variables 还是靠
	// business_key 解析出来的，便于人工抽查。
	source string
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
		"ops:backfill_process_instance_business_identity",
		"WorkItem Wave 1：给切换前创建的运行中流程实例补齐 business_type/business_id",
	)

	candidates, skipped, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填流程实例失败", "error", err)
	}

	for _, s := range skipped {
		// 推导不出业务身份的行必须显式报出来：它们同样会写出空 business_type 的审批
		// 决策，但这个工具修不了，需要人工判断（多半是历史脏数据或自定义触发路径）。
		sugar.Warnw("无法推导业务身份，需人工处理",
			"process_instance_id", s.id, "tenant_id", s.tenantID,
			"process_instance_key", s.instanceKey, "business_key", s.businessKey)
	}

	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的流程实例", "tenant_id", *tenantID, "unresolvable", len(skipped))
		if len(skipped) > 0 {
			os.Exit(1)
		}
		return
	}

	sugar.Infow("找到待回填流程实例",
		"count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun, "unresolvable", len(skipped))
	for _, c := range candidates {
		sugar.Infow("候选流程实例",
			"process_instance_id", c.id, "tenant_id", c.tenantID,
			"process_instance_key", c.instanceKey, "business_key", c.businessKey,
			"business_type", c.businessType, "business_id", c.businessID, "source", c.source)
	}

	if *dryRun {
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	succeeded, failed := 0, 0
	for _, c := range candidates {
		// 带 tenant_id 条件更新：这个工具以系统身份运行，写侧仍然显式收敛到候选行
		// 自己的租户，避免任何越租户写入。
		affected, err := client.ProcessInstance.Update().
			Where(
				processinstance.ID(c.id),
				processinstance.TenantID(c.tenantID),
			).
			SetBusinessType(c.businessType).
			SetBusinessID(c.businessID).
			Save(ctx)
		if err != nil {
			sugar.Errorw("回填失败", "process_instance_id", c.id, "tenant_id", c.tenantID, "error", err)
			failed++
			continue
		}
		if affected == 0 {
			sugar.Warnw("回填未命中任何行（可能已被并发修改）", "process_instance_id", c.id, "tenant_id", c.tenantID)
			failed++
			continue
		}
		sugar.Infow("回填成功",
			"process_instance_id", c.id, "tenant_id", c.tenantID,
			"business_type", c.businessType, "business_id", c.businessID)
		succeeded++
	}

	sugar.Infow("回填完成", "succeeded", succeeded, "failed", failed, "total", len(candidates))
	if failed > 0 || len(skipped) > 0 {
		os.Exit(1)
	}
}

// findCandidates 返回可回填的实例，以及查得到但推导不出业务身份的实例（skipped）。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) (resolved []candidate, skipped []candidate, err error) {
	query := client.ProcessInstance.Query().
		Where(
			// running 与 suspended 都还会继续产生后续的 ProcessApprovalDecision（suspended
			// 实例会被 resume 回 running），completed/terminated 不会再有新的审批决策，
			// 缺失业务身份也不会再被读到，因此排除在候选之外。
			processinstance.StatusIn("running", "suspended"),
			processinstance.Or(
				processinstance.BusinessTypeIsNil(),
				processinstance.BusinessTypeEQ(""),
				processinstance.BusinessIDIsNil(),
				processinstance.BusinessIDEQ(0),
			),
		)
	if tenantID > 0 {
		query = query.Where(processinstance.TenantID(tenantID))
	}
	instances, err := query.All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("查询运行中流程实例失败: %w", err)
	}

	for _, inst := range instances {
		c := candidate{
			id:          inst.ID,
			tenantID:    inst.TenantID,
			instanceKey: inst.ProcessInstanceID,
			businessKey: inst.BusinessKey,
		}
		bType, bID, source := deriveBusinessIdentity(inst)
		if bType == "" || bID <= 0 {
			skipped = append(skipped, c)
			continue
		}
		// 已经正确的那一半保持原值，只补缺失的部分——避免用推导值覆盖 StartProcess
		// 写入的权威值（例如 business_type 已写好、只有 business_id 是 0 的行）。
		c.businessType = inst.BusinessType
		if c.businessType == "" {
			c.businessType = bType
		}
		c.businessID = inst.BusinessID
		if c.businessID <= 0 {
			c.businessID = bID
		}
		c.source = source
		resolved = append(resolved, c)
	}
	return resolved, skipped, nil
}

// deriveBusinessIdentity 推导实例的业务身份：优先 variables 里的
// business_type/business_id，取不到再回退解析 business_key（"{type}:{id}"）。
// 两个来源可以互补（例如 variables 只有 business_type、business_key 有完整两段）。
func deriveBusinessIdentity(inst *ent.ProcessInstance) (businessType string, businessID int, source string) {
	var fromVars, fromKey bool

	if inst.Variables != nil {
		if v, ok := inst.Variables["business_type"].(string); ok {
			businessType = strings.ToLower(strings.TrimSpace(v))
			fromVars = businessType != ""
		}
		if id := toInt(inst.Variables["business_id"]); id > 0 {
			businessID = id
			fromVars = true
		}
	}

	if businessType == "" || businessID <= 0 {
		// business_key 的约定格式是 "{business_type}:{business_id}"（见
		// ProcessTriggerService 与 BPMNApprovalBridge.findPendingBusinessTask）。
		// 用 LastIndex 切分：业务类型里不含冒号，而 ID 段必须是纯数字，这样即便
		// 历史数据里出现了带冒号的前缀也能落到最后一段。
		key := strings.TrimSpace(inst.BusinessKey)
		if idx := strings.LastIndex(key, ":"); idx > 0 && idx < len(key)-1 {
			keyType := strings.ToLower(strings.TrimSpace(key[:idx]))
			keyID, convErr := strconv.Atoi(strings.TrimSpace(key[idx+1:]))
			if convErr == nil && keyID > 0 && keyType != "" {
				if businessType == "" {
					businessType = keyType
					fromKey = true
				}
				if businessID <= 0 {
					businessID = keyID
					fromKey = true
				}
			}
		}
	}

	switch {
	case fromVars && fromKey:
		source = "variables+business_key"
	case fromVars:
		source = "variables"
	case fromKey:
		source = "business_key"
	}
	return businessType, businessID, source
}

// toInt 把 variables JSON 里可能出现的数值形态统一成 int：JSON 反序列化后数字是
// float64，但同进程内直接塞进去的是 int，两种都要认。
func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i
		}
	}
	return 0
}
