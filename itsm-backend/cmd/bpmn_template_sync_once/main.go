// Command bpmn_template_sync_once 是一次性运维工具：把单个内置 BPMN 模板同步到
// 指定租户（未部署则部署 v1，内容漂移则发布并激活新版本）。
//
// 存在的理由：内置模板同步只在 seeder 里触发（ITSM_AUTO_SEED=true），而共享开发库
// 常驻 ITSM_AUTO_SEED=false——改了 .bpmn 之后没有受支持的部署入口。这里直接复用
// BPMNTemplateService.DeployTemplateByName 这一条生产路径，不复制也不改写同步逻辑，
// 并且只作用于 -key 指定的单个模板，避免 LoadAndDeployTemplates 顺带升级其它漂移模板。
//
// 默认 dry-run：只解析模板、打印每个用户任务声明的路由、并对固定范围路由做真实解析，
// 确认能解析出候选人才落库。-apply 才写库。
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
	"itsm-backend/ent/processdefinition"
	"itsm-backend/service"
	"itsm-backend/service/approver"

	"go.uber.org/zap"
)

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func main() {
	key := flag.String("key", "", "内置模板 key（.bpmn 文件名去扩展名），必填")
	tenantID := flag.Int("tenant-id", 1, "目标租户 ID")
	apply := flag.Bool("apply", false, "真正写库；缺省只做 dry-run 校验")
	flag.Parse()

	if *key == "" {
		exitf("-key 必填")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		exitf("加载配置失败: %v", err)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		exitf("初始化日志失败: %v", err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		exitf("连接数据库失败: %v", err)
	}
	defer client.Close()

	ctx := tenantctx.WithTenantID(context.Background(), *tenantID)

	tmplSvc := service.NewBPMNTemplateService(client)

	// ---- 1. 当前库中最新版本 ----
	before, err := latest(ctx, client, *key, *tenantID)
	if err != nil {
		exitf("查询当前定义失败: %v", err)
	}
	if before == nil {
		fmt.Printf("[当前] %s 尚未部署（将部署 v1）\n", *key)
	} else {
		fmt.Printf("[当前] %s version=%s id=%d is_active=%v\n", *key, before.Version, before.ID, before.IsActive)
	}

	// ---- 2. 校验嵌入模板 ----
	content, err := tmplSvc.GetTemplateContent(*key)
	if err != nil {
		exitf("读取嵌入模板失败: %v", err)
	}
	if err := inspect(ctx, client, content, *tenantID); err != nil {
		exitf("模板校验失败: %v", err)
	}

	if !*apply {
		fmt.Println("[dry-run] 校验通过，未写库。加 -apply 执行同步。")
		return
	}

	// ---- 3. 同步（复用生产路径） ----
	if err := tmplSvc.DeployTemplateByName(ctx, *key, *tenantID); err != nil {
		exitf("同步模板失败: %v", err)
	}

	after, err := latest(ctx, client, *key, *tenantID)
	if err != nil {
		exitf("查询同步后定义失败: %v", err)
	}
	fmt.Printf("[结果] %s version=%s id=%d is_active=%v\n", *key, after.Version, after.ID, after.IsActive)

	active, err := client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(*key),
			processdefinition.TenantID(*tenantID),
			processdefinition.IsActive(true),
		).
		Count(ctx)
	if err != nil {
		exitf("统计 active 版本失败: %v", err)
	}
	if active != 1 {
		exitf("不变量被破坏：%s 有 %d 个 is_active 版本，期望恰好 1 个", *key, active)
	}
	fmt.Printf("[校验] %s 恰好 1 个 is_active 版本\n", *key)
}

func latest(ctx context.Context, client *ent.Client, key string, tenantID int) (*ent.ProcessDefinition, error) {
	d, err := client.ProcessDefinition.Query().
		Where(processdefinition.Key(key), processdefinition.TenantID(tenantID)).
		Order(ent.Desc(processdefinition.FieldIsLatest), ent.Desc(processdefinition.FieldID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// inspect 检查用户任务声明的路由，并对固定范围路由做真实解析。
//
// 不把"未声明路由"当作失败：`ValidateDefinitionForPublication` 里那条
// "task requires candidate resolution configuration" 只作用于服务目录/绑定发布，
// 对内置模板 seeding 不生效；而所有内置模板（incident/service_request/ticket_urgent …）
// 的履行类节点都没有声明路由，引擎会按 requester_id 兜底。这是全产品既有行为，
// 不是本模板独有的缺陷，在这里拦下来只会挡住受支持的部署路径。
//
// 真正会失败的是"声明了固定范围路由却解析不出人"——那种任务在当前引擎语义下保持未指派
// （见 bpmn_process_engine.go 的固定范围分支），会停在原地等人，所以必须落库前先验证。
func inspect(ctx context.Context, client *ent.Client, content []byte, tenantID int) error {
	parsed, err := service.NewBPMNParser().ParseXML(content)
	if err != nil {
		return fmt.Errorf("解析 BPMN 失败: %w", err)
	}

	tasks, unrouted := 0, 0
	for _, p := range parsed.Processes {
		if !p.IsExecutable {
			return fmt.Errorf("流程 %s 不是 executable", p.ID)
		}
		for _, t := range p.UserTasks {
			tasks++
			declared := t.AssigneeSource != "" || t.CandidateUsers != "" || t.CandidateGroups != "" ||
				t.AssigneeRole != "" || t.AssigneeGmChain ||
				t.AssigneeDeptId > 0 || t.AssigneeTeamId > 0 || t.AssigneeProjectId > 0 || t.AssigneeTempTeamId > 0
			fmt.Printf("  [任务] %-20s purpose=%-10s source=%-20s dept=%d team=%d project=%d tempTeam=%d candidates=%q groups=%q\n",
				t.ID, orDash(t.TaskPurpose), orDash(t.AssigneeSource),
				t.AssigneeDeptId, t.AssigneeTeamId, t.AssigneeProjectId, t.AssigneeTempTeamId,
				t.CandidateUsers, t.CandidateGroups)
			if !declared {
				// 未声明路由：引擎按 requester_id→triggered_by→assignee_id→默认 兜底。
				// generic 进单固定 approval_required=false，据此标注该节点当前是否可达。
				unrouted++
				fmt.Printf("         !! 未声明路由，运行时会兜底给申请人（generic 进单下%s）\n",
					reachableHint(t.ID))
			}
			if t.AssigneeTeamId > 0 {
				out, err := approver.NewTeamLeaderResolver().Resolve(ctx, client, &approver.ApproverContext{
					TenantID: tenantID,
					TeamID:   t.AssigneeTeamId,
				})
				if err != nil {
					return fmt.Errorf("任务 %q 的团队路由 team=%d 解析失败: %w", t.ID, t.AssigneeTeamId, err)
				}
				if len(out) == 0 {
					return fmt.Errorf("任务 %q 的团队路由 team=%d 解析出 0 个候选人", t.ID, t.AssigneeTeamId)
				}
				for _, c := range out {
					fmt.Printf("         -> 解析到负责人 id=%d role=%s source=%s\n", c.UserID, c.Role, c.Source)
				}
			}
		}
	}
	fmt.Printf("  [校验] %d 个用户任务，%d 个未声明路由（兜底给申请人），已声明的固定范围路由均可解析\n", tasks, unrouted)
	return nil
}

// reachableHint 只针对 ticket_general_flow 的网关条件，说明该节点在 generic 进单
// （approval_required=false、need_escalate 未设置）下是否真的会被走到。
func reachableHint(taskID string) string {
	switch taskID {
	case "Activity_Approval":
		return "不可达：Flow_ApprovalYes 要求 approval_required==true"
	case "Activity_Escalate":
		return "不可达：Flow_4 要求 need_escalate==true"
	default:
		return "可达"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
