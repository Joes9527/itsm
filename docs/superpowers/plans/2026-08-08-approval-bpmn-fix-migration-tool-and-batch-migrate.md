# 审批收敛·组件③ — 修复迁移工具 + 批量迁移存量自定义工作流 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 `legacy_approval_migration_service.go`（`buildLegacyApprovalBPMN` 读错字段名，导致生成的 BPMN `assignee` 属性字面是 `"<nil>"`，不分类型全部失效），然后新增一个批量迁移工具，把每个租户真正自定义过的 `ApprovalWorkflow` 转成 BPMN 流程定义 + `ProcessBinding`。

**Architecture:** 两个任务，顺序依赖——Task 1 先修 `buildLegacyApprovalBPMN` 本身（这是 Task 2 批量跑的时候依赖的核心转换逻辑，必须先保证单个工作流转换是对的），Task 2 在此基础上加一个新的、独立的运维 CLI 工具（`cmd/migrate_legacy_approvals/main.go`，跟 `cmd/provision_tenant/main.go` 同样的形状），遍历所有租户调用已经修好的 `Migrate()`。

**Tech Stack:** Go 1.x / Ent ORM / `stretchr/testify` / sqlite3（enttest）——跟这次会话之前三个组件用同一套。

## Global Constraints

- 设计文档：`docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md`（组件③部分，写这份计划前用真实探针重新核实过一次，原文档已经订正——`buildLegacyApprovalBPMN` 的 bug 比原来诊断的严重得多：不是"某几种类型的值映射错了"，是**字段名本身读错了，所有类型的节点现在都会生成 `assignee="<nil>"`**，探针实测确认（见"现状核实"章节）。
- **字段名修复**：`buildLegacyApprovalBPMN` 现在用 `node["assignee_type"]`/`node["assignee_value"]`/`node["step_order"]`（snake_case，`map[string]interface{}` 裸访问）读节点，但真正通过 `/admin/approvals` 界面创建/编辑出来的工作流，`Nodes` 字段是 `nodesToMaps`（`service/approval_type_converters.go:12`）产出的、`dto.ApprovalNodeConfig`（`dto/approval_types.go:68-83`）的**驼峰**形状（`assigneeType`/`assigneeValue`/`approverType`/`level`/`timeoutHours`）。修复必须复用 `mapsToNodes(nodes) ([]dto.ApprovalNodeConfig, error)` 这个已经存在、已经在 `parseWorkflowNodes`/`CreateWorkflow`/`UpdateWorkflow` 里验证过的强类型转换路径，不是自己重新发明一套字段解析。
- **`ApproverType` 兜底到 `AssigneeType` 的逻辑必须复用，不能漏掉**：`ApprovalService.parseWorkflowNodes`（`service/approval_service.go`）在 `AssigneeType` 为空时，会检查 `ApproverType` 是不是 `dept_manager`/`team_leader`/`project_manager`/`temp_team_leader`/`amount_based` 这 5 个"动态类型"之一，是的话把 `AssigneeType` 设成 `ApproverType` 的值——这是当前唯一"活的"、被 `TriggerApproval` 依赖的字段兜底约定，不是这次新发明的规则。`buildLegacyApprovalBPMN` 修复后必须复制同样的兜底（6 行左右的逻辑，直接照抄 `parseWorkflowNodes` 里的那段 `switch`），否则用"动态类型"下拉框配置出来的节点会漏判。`role` 类型不在这 5 个动态类型里——`assigneeType: "role"` 是前端直接设置的，不经过这层兜底。
- **不需要新增 ent 字段**：原设计文档提到"给 `ApprovalWorkflow` 加 `migrated_to_bpmn_at` 时间戳字段标记已迁移"——这次计划写的时候重新核实过，`LegacyApprovalMigrationService.Migrate` 已经有幂等检查（按 `process_definition.key == "legacy_approval_<workflowID>"` + `tenant_id` 查是否已存在，存在就跳过并标记 `Skipped: true`），不需要再加一个新的 ent 字段/DB migration 来达到同样的幂等效果——用已有机制，不重新发明。
- `amount_based` 类型（按金额阈值选审批人）明确不支持这轮迁移——遇到就让整个工作流的迁移失败，返回明确错误，不生成部分迁移的 BPMN（跟组件①的设计保持一致：这个类型需要接一个目前流程实例变量里没有的金额概念，这次不做）。
- 批量迁移工具是一个新的独立 CLI（`cmd/migrate_legacy_approvals/main.go`），照着 `cmd/provision_tenant/main.go` 的形状写（`config.LoadConfig()` + `database.InitDatabaseWithRLS` + `tenantctx.SystemContext`），不是 `-tags migrate` 那套 SQL schema 迁移机制——那套是给数据库表结构迁移用的，语义不匹配这次"批量转换业务数据"的需求。
- 所有新增/修改的 Go 代码保持在 `package service`（`legacy_approval_migration_service.go` 所在包）或新的 `package main`（`cmd/migrate_legacy_approvals/`），不新建其它子包。
- 测试用 `enttest.Open(t, "sqlite3", "file:<unique-name>?mode=memory&cache=shared&_fk=1")`，DSN 每个测试文件用不同前缀。

## File Structure

- Modify: `itsm-backend/service/legacy_approval_migration_service.go` — `buildLegacyApprovalBPMN` 重写字段解析 + 值映射；`Migrate` 加 `amount_based` 中止逻辑；新增 `MigrateAllForTenant`/`MigrateAll` 方法供批量工具调用。
- Modify: `itsm-backend/service/legacy_approval_migration_service_unit_test.go` — 现有测试用的是错误的 snake_case 节点形状，需要改成真实的驼峰形状；追加新测试覆盖组织范围类型、`role` 类型、`amount_based` 中止。
- Create: `itsm-backend/cmd/migrate_legacy_approvals/main.go` — 新的独立运维 CLI。

---

### Task 1: 修复 `buildLegacyApprovalBPMN` 的字段解析 + 值映射

**Files:**
- Modify: `itsm-backend/service/legacy_approval_migration_service.go`
- Modify: `itsm-backend/service/legacy_approval_migration_service_unit_test.go`

**Interfaces:**
- Consumes: `mapsToNodes(maps []map[string]interface{}) ([]dto.ApprovalNodeConfig, error)`（已存在，`service/approval_type_converters.go:12`）；`dto.ApprovalNodeConfig{Level, Name, ApproverType, AssigneeType, AssigneeValue, ApprovalMode, ...}`（已存在，`dto/approval_types.go:68-83`）；`dto.ApprovalNodeType`（已存在常量：`ApprovalNodeTypeUser="user"`、`ApprovalNodeTypeRole="role"`、`ApprovalNodeTypeDeptManager="dept_manager"`、`ApprovalNodeTypeTeamLeader="team_leader"`、`ApprovalNodeTypeProjectManager="project_manager"`、`ApprovalNodeTypeTempTeamLeader="temp_team_leader"`、`ApprovalNodeTypeAmountBased="amount_based"`，`dto/approval_types.go:11-19`）；组件①新增的 BPMN 声明式属性（`assigneeRole`/`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`，已存在于 `service/bpmn_types.go`/`bpmn_process_engine.go`）。
- Produces：`buildLegacyApprovalBPMN` 的函数签名不变（`func buildLegacyApprovalBPMN(key, name string, nodes []map[string]interface{}) (string, error)`），内部实现整体重写。

- [ ] **Step 1: 改写现有测试，先确认按预期失败**

`itsm-backend/service/legacy_approval_migration_service_unit_test.go` 现有的 `TestBuildLegacyApprovalBPMN` 用的是错误的（不代表真实数据的）snake_case 节点形状。把整个文件内容替换成：

```go
package service

import (
	"strings"
	"testing"
)

// 真实的节点形状——nodesToMaps（service/approval_type_converters.go:12）产出的驼峰 JSON，
// 匹配 dto.ApprovalNodeConfig 的 struct tag，不是这个函数以前错误假设的 snake_case。

func TestBuildLegacyApprovalBPMN_UserType(t *testing.T) {
	nodes := []map[string]interface{}{
		{"level": 2, "name": "IT总监审批", "assigneeType": "user", "assigneeValue": "7", "approvalMode": "any"},
		{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "3", "approvalMode": "any"},
	}
	xml, err := buildLegacyApprovalBPMN("legacy_1", "变更审批", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(xml, "经理审批") > strings.Index(xml, "IT总监审批") {
		t.Fatal("nodes were not ordered by level")
	}
	for _, want := range []string{`itsm:taskPurpose="approval"`, `itsm:assignee="3"`, `itsm:assignee="7"`} {
		if !strings.Contains(xml, want) {
			t.Fatalf("missing %s in:\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "<nil>") {
		t.Fatalf("generated XML contains literal <nil> -- field parsing regressed:\n%s", xml)
	}
	parsed, err := NewBPMNParser().ParseXML([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Processes[0].UserTasks[0]; got.TaskPurpose != "approval" || got.Assignee != "3" {
		t.Fatalf("migration attributes were not parsed: %#v", got)
	}
}

func TestBuildLegacyApprovalBPMN_RoleType(t *testing.T) {
	nodes := []map[string]interface{}{
		{"level": 1, "name": "运维经理审批", "assigneeType": "role", "assigneeValue": "manager", "approvalMode": "any"},
	}
	xml, err := buildLegacyApprovalBPMN("legacy_2", "角色审批", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `itsm:assigneeRole="manager"`) {
		t.Fatalf("role type should map to assigneeRole, not candidateGroups:\n%s", xml)
	}
	if strings.Contains(xml, "candidateGroups") {
		t.Fatalf("role type should not be conflated with candidateGroups:\n%s", xml)
	}
}

func TestBuildLegacyApprovalBPMN_GroupType(t *testing.T) {
	nodes := []map[string]interface{}{
		{"level": 1, "name": "安全组审批", "assigneeType": "group", "assigneeValue": "security-team", "approvalMode": "any"},
	}
	xml, err := buildLegacyApprovalBPMN("legacy_3", "组审批", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `itsm:candidateGroups="security-team"`) {
		t.Fatalf("group type should map to candidateGroups:\n%s", xml)
	}
}

func TestBuildLegacyApprovalBPMN_FixedScopeTypes(t *testing.T) {
	tests := []struct {
		assigneeType string
		wantAttr     string
	}{
		{"dept_manager", `itsm:assigneeDeptId="12"`},
		{"team_leader", `itsm:assigneeTeamId="12"`},
		{"project_manager", `itsm:assigneeProjectId="12"`},
		{"temp_team_leader", `itsm:assigneeTempTeamId="12"`},
	}
	for _, tt := range tests {
		t.Run(tt.assigneeType, func(t *testing.T) {
			nodes := []map[string]interface{}{
				{"level": 1, "name": "固定范围审批", "assigneeType": tt.assigneeType, "assigneeValue": "12", "approvalMode": "any"},
			}
			xml, err := buildLegacyApprovalBPMN("legacy_4", "固定范围审批", nodes)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(xml, tt.wantAttr) {
				t.Fatalf("assigneeType=%s should map to %s, got:\n%s", tt.assigneeType, tt.wantAttr, xml)
			}
			if strings.Contains(xml, `itsm:assignee="12"`) {
				t.Fatalf("org-scope ID %q should never be written into the assignee attribute directly:\n%s", tt.assigneeType, xml)
			}
		})
	}
}

func TestBuildLegacyApprovalBPMN_ApproverTypeFallback(t *testing.T) {
	// 没有直接设置 assigneeType，但 approverType 是 5 个"动态类型"之一——
	// 复用 parseWorkflowNodes 同样的兜底：ApproverType 兜底到 AssigneeType。
	nodes := []map[string]interface{}{
		{"level": 1, "name": "部门负责人审批", "approverType": "dept_manager", "assigneeValue": "9", "approvalMode": "any"},
	}
	xml, err := buildLegacyApprovalBPMN("legacy_5", "兜底测试", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `itsm:assigneeDeptId="9"`) {
		t.Fatalf("approverType=dept_manager without explicit assigneeType should still fall back correctly:\n%s", xml)
	}
}

func TestBuildLegacyApprovalBPMN_AmountBased_AbortsWithClearError(t *testing.T) {
	nodes := []map[string]interface{}{
		{"level": 1, "name": "金额审批", "assigneeType": "amount_based", "assigneeValue": "10000", "approvalMode": "any"},
	}
	_, err := buildLegacyApprovalBPMN("legacy_6", "金额审批", nodes)
	if err == nil {
		t.Fatal("expected an error for amount_based node, got nil")
	}
	if !strings.Contains(err.Error(), "amount_based") || !strings.Contains(err.Error(), "金额审批") {
		t.Fatalf("error should name the unsupported type and the offending node, got: %v", err)
	}
}

func TestBuildLegacyApprovalBPMN_AmountBased_AbortsEntireWorkflow(t *testing.T) {
	// 一个工作流里既有能迁移的节点又有 amount_based 节点——整体失败，不做部分迁移。
	nodes := []map[string]interface{}{
		{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "3", "approvalMode": "any"},
		{"level": 2, "name": "金额审批", "assigneeType": "amount_based", "assigneeValue": "10000", "approvalMode": "any"},
	}
	_, err := buildLegacyApprovalBPMN("legacy_7", "混合审批", nodes)
	if err == nil {
		t.Fatal("expected an error -- amount_based node should abort the whole workflow, not just skip itself")
	}
}
```

- [ ] **Step 2: 运行测试，确认按预期失败**

Run: `cd itsm-backend && go test ./service/... -run TestBuildLegacyApprovalBPMN -v`
Expected：大部分 FAIL——现有实现读 snake_case 字段，这些测试传的是驼峰字段，`assigneeType`/`assigneeValue` 全部读不到，生成的 XML 里 `assignee` 属性会是字面的 `"<nil>"`，断言失败。

- [ ] **Step 3: 重写 `buildLegacyApprovalBPMN`**

把 `itsm-backend/service/legacy_approval_migration_service.go` 里的 `buildLegacyApprovalBPMN` 函数（以及它调用的 `intValue` 辅助函数，不再需要，可以删除）整体替换成：

```go
// buildLegacyApprovalBPMN 把一个 ApprovalWorkflow.Nodes（dto.ApprovalNodeConfig 的驼峰 JSON
// 形状，nodesToMaps 产出）转成简单的线性审批链 BPMN XML。节点按 Level 排序，每个节点生成一个
// taskPurpose="approval" 的 userTask，按 AssigneeType 映射到对应的 BPMN 声明式属性——这些属性
// 是组件①加的，createUserTask 已经支持解析它们。
func buildLegacyApprovalBPMN(key, name string, nodes []map[string]interface{}) (string, error) {
	if strings.TrimSpace(key) == "" || len(nodes) == 0 {
		return "", fmt.Errorf("legacy workflow must have a key and at least one node")
	}

	configs, err := mapsToNodes(nodes)
	if err != nil {
		return "", fmt.Errorf("failed to parse legacy workflow nodes: %w", err)
	}

	sort.SliceStable(configs, func(i, j int) bool { return configs[i].Level < configs[j].Level })

	escape := func(v string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(v)); return b.String() }
	var tasks, flows strings.Builder
	previous := "StartEvent_1"
	for i, cfg := range configs {
		// ApproverType 兜底到 AssigneeType——复用 ApprovalService.parseWorkflowNodes
		// （service/approval_service.go）同样的约定，不是这里新发明的规则。
		assigneeType := cfg.AssigneeType
		if assigneeType == "" {
			switch cfg.ApproverType {
			case dto.ApprovalNodeTypeDeptManager, dto.ApprovalNodeTypeTeamLeader,
				dto.ApprovalNodeTypeProjectManager, dto.ApprovalNodeTypeTempTeamLeader,
				dto.ApprovalNodeTypeAmountBased:
				assigneeType = string(cfg.ApproverType)
			}
		}

		id := fmt.Sprintf("Approval_%d", i+1)
		var attr, value string
		switch assigneeType {
		case "user":
			attr, value = "assignee", cfg.AssigneeValue
		case "group":
			attr, value = "candidateGroups", cfg.AssigneeValue
		case string(dto.ApprovalNodeTypeRole):
			attr, value = "assigneeRole", cfg.AssigneeValue
		case string(dto.ApprovalNodeTypeDeptManager):
			attr, value = "assigneeDeptId", cfg.AssigneeValue
		case string(dto.ApprovalNodeTypeTeamLeader):
			attr, value = "assigneeTeamId", cfg.AssigneeValue
		case string(dto.ApprovalNodeTypeTempTeamLeader):
			attr, value = "assigneeTempTeamId", cfg.AssigneeValue
		case string(dto.ApprovalNodeTypeProjectManager):
			attr, value = "assigneeProjectId", cfg.AssigneeValue
		case string(dto.ApprovalNodeTypeAmountBased):
			return "", fmt.Errorf("workflow %q node %q uses unsupported assignee type amount_based -- migration aborted for the whole workflow, not just this node", name, cfg.Name)
		default:
			return "", fmt.Errorf("workflow %q node %q has unrecognized assignee type %q -- migration aborted", name, cfg.Name, assigneeType)
		}

		fmt.Fprintf(&tasks, `<bpmn:userTask id="%s" name="%s" itsm:taskPurpose="approval" itsm:approvalMode="single" itsm:%s="%s" itsm:commentRequiredOnReject="true"/>`, id, escape(cfg.Name), attr, escape(value))
		fmt.Fprintf(&flows, `<bpmn:sequenceFlow id="Flow_%d" sourceRef="%s" targetRef="%s"/>`, i+1, previous, id)
		previous = id
	}
	fmt.Fprintf(&flows, `<bpmn:sequenceFlow id="Flow_%d" sourceRef="%s" targetRef="EndEvent_1"/>`, len(configs)+1, previous)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:itsm="https://github.com/heidsoft/itsm/schema/bpmn" id="Definitions_%s" targetNamespace="https://github.com/heidsoft/itsm"><bpmn:process id="%s" name="%s" isExecutable="true"><bpmn:startEvent id="StartEvent_1"/>%s<bpmn:endEvent id="EndEvent_1"/>%s</bpmn:process></bpmn:definitions>`, escape(key), escape(key), escape(name), tasks.String(), flows.String()), nil
}
```

检查文件顶部的 `import` 块——`"sort"`、`"strings"`、`"encoding/xml"`、`"fmt"` 应该已经导入（现有代码就用到了），新增需要 `"itsm-backend/dto"`（检查是否已导入，`Migrate` 方法里已经用到 `dto.BusinessType`，应该已经有）。删除不再使用的 `intValue` 函数（原来专门给 `step_order` 排序用的，现在直接用 `dto.ApprovalNodeConfig.Level`，不再需要）。

- [ ] **Step 4: 运行测试，确认全部通过**

Run: `cd itsm-backend && go test ./service/... -run TestBuildLegacyApprovalBPMN -v`
Expected：全部 PASS。

- [ ] **Step 5: 跑整个 service 包和全量测试，确认没有回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... -v 2>&1 | tail -100`
Expected：编译通过；重点确认 `legacy_approval_migration_service_unit_test.go` 全部通过，`approval_service_test.go`/`approval_service_table_test.go`（这次没改 `approval_service.go` 本身，只是确认没有意外影响）继续 PASS。

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok"`
Expected：没有 FAIL 输出。

- [ ] **Step 6: Commit**

```bash
cd itsm-backend
git add service/legacy_approval_migration_service.go service/legacy_approval_migration_service_unit_test.go
git commit -m "fix(bpmn): buildLegacyApprovalBPMN read the wrong field names entirely

It read node fields via snake_case keys (assignee_type/assignee_value/
step_order), but real tenant-created ApprovalWorkflow.Nodes (via
CreateWorkflow/UpdateWorkflow -> nodesToMaps) store the camelCase
shape matching dto.ApprovalNodeConfig's JSON tags -- every node,
regardless of configured assignee type, generated a literal
assignee=\"<nil>\" (confirmed by probe before writing this fix). Now
reuses mapsToNodes (the same strongly-typed parser TriggerApproval
already relies on) and replicates parseWorkflowNodes' ApproverType->
AssigneeType fallback convention, then maps to the declarative BPMN
attributes component (1) of this convergence effort added: assignee
(user), candidateGroups (group), assigneeRole (role), and the four
assigneeDeptId/TeamId/ProjectId/TempTeamId fixed-scope attributes.
amount_based aborts the whole workflow's migration with a named error
instead of silently mis-mapping."
```

---

### Task 2: 批量迁移 CLI

**Files:**
- Modify: `itsm-backend/service/legacy_approval_migration_service.go` — 新增 `MigrateAllForTenant`/`MigrateAllTenants` 方法。
- Create: `itsm-backend/cmd/migrate_legacy_approvals/main.go`
- Test: `itsm-backend/service/legacy_approval_migration_service_unit_test.go`（追加，覆盖新增的批量方法；CLI `main.go` 本身不写单元测试——`main` 函数不可单测，核心逻辑都在 Task 1/本任务新增的 service 方法里，已经被测试覆盖）

**Interfaces:**
- Consumes: `LegacyApprovalMigrationService.Migrate(ctx, workflow, dryRun) (*LegacyApprovalMigrationResult, error)`（已存在，Task 1 修好的版本）；`config.LoadConfig()`、`database.InitDatabaseWithRLS`（已存在，`cmd/provision_tenant/main.go` 同款用法）；`tenantctx.SystemContext(ctx, component, reason) context.Context`（已存在，`common/tenantctx/system.go:39`）。
- Produces：
  - `func (s *LegacyApprovalMigrationService) MigrateAllForTenant(ctx context.Context, tenantID int, dryRun bool) ([]*LegacyApprovalMigrationResult, error)` — 遍历一个租户下所有 `is_active=true` 的 `ApprovalWorkflow`，逐个调用 `Migrate`，收集结果；单个工作流迁移失败不中止整个批次，把错误信息收进对应结果里继续下一个（一个租户的一条自定义工作流写得有问题，不该拖累同租户其它工作流的迁移）。
  - `func (s *LegacyApprovalMigrationService) MigrateAllTenants(ctx context.Context, dryRun bool) (map[int][]*LegacyApprovalMigrationResult, error)` — 遍历所有租户，对每个租户调用 `MigrateAllForTenant`，按 `tenantID` 汇总结果。

- [ ] **Step 1: 写测试（先写，此时会失败——这两个方法还不存在）**

在 `itsm-backend/service/legacy_approval_migration_service_unit_test.go` 文件末尾追加（这个文件现在需要能访问真实的 ent client，所以在文件顶部 `import` 块里补上 `"context"`、`"itsm-backend/ent"`、`"itsm-backend/ent/enttest"`、`_ "github.com/mattn/go-sqlite3"`、`"github.com/stretchr/testify/assert"`、`"github.com/stretchr/testify/require"`——如果还没有的话）：

```go

func newMigrationTestClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:legacy_approval_migration_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	return client
}

func TestLegacyApprovalMigrationService_MigrateAllForTenant_MigratesActiveWorkflows(t *testing.T) {
	client := newMigrationTestClient(t)
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Migration Test Tenant").
		SetCode("migration-test-tenant").
		SetDomain("migration-test.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ApprovalWorkflow.Create().
		SetName("自定义审批 A").
		SetTicketType("service_request").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{
			{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"},
		}).
		Save(ctx)
	require.NoError(t, err)

	// 未激活的工作流不应该被迁移
	_, err = client.ApprovalWorkflow.Create().
		SetName("已停用的审批").
		SetTicketType("change").
		SetIsActive(false).
		SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{
			{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"},
		}).
		Save(ctx)
	require.NoError(t, err)

	svc := NewLegacyApprovalMigrationService(client)
	results, err := svc.MigrateAllForTenant(ctx, tenant.ID, false)
	require.NoError(t, err)
	require.Len(t, results, 1, "只有 is_active=true 的工作流应该被迁移")
	assert.False(t, results[0].Skipped)
	assert.NotEmpty(t, results[0].ProcessDefinitionKey)
}

func TestLegacyApprovalMigrationService_MigrateAllForTenant_OneFailureDoesNotBlockOthers(t *testing.T) {
	client := newMigrationTestClient(t)
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Migration Failure Test Tenant").
		SetCode("migration-failure-test-tenant").
		SetDomain("migration-failure.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ApprovalWorkflow.Create().
		SetName("坏的审批（amount_based）").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{
			{"level": 1, "name": "金额审批", "assigneeType": "amount_based", "assigneeValue": "10000", "approvalMode": "any"},
		}).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ApprovalWorkflow.Create().
		SetName("好的审批").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{
			{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"},
		}).
		Save(ctx)
	require.NoError(t, err)

	svc := NewLegacyApprovalMigrationService(client)
	results, err := svc.MigrateAllForTenant(ctx, tenant.ID, false)
	require.NoError(t, err, "单个工作流迁移失败不应该让整个批次报错")
	require.Len(t, results, 2)

	var sawFailure, sawSuccess bool
	for _, r := range results {
		if r.Error != "" {
			sawFailure = true
			assert.Contains(t, r.Error, "amount_based")
		} else {
			sawSuccess = true
		}
	}
	assert.True(t, sawFailure, "amount_based 那条应该记录失败原因")
	assert.True(t, sawSuccess, "另一条正常的工作流不应该被拖累")
}

func TestLegacyApprovalMigrationService_MigrateAllTenants_GroupsByTenant(t *testing.T) {
	client := newMigrationTestClient(t)
	ctx := context.Background()

	tenant1, err := client.Tenant.Create().
		SetName("Tenant One").SetCode("tenant-one").SetDomain("tenant-one.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	tenant2, err := client.Tenant.Create().
		SetName("Tenant Two").SetCode("tenant-two").SetDomain("tenant-two.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.ApprovalWorkflow.Create().
		SetName("T1 审批").SetIsActive(true).SetTenantID(tenant1.ID).
		SetNodes([]map[string]interface{}{{"level": 1, "name": "审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"}}).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ApprovalWorkflow.Create().
		SetName("T2 审批").SetIsActive(true).SetTenantID(tenant2.ID).
		SetNodes([]map[string]interface{}{{"level": 1, "name": "审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"}}).
		Save(ctx)
	require.NoError(t, err)

	svc := NewLegacyApprovalMigrationService(client)
	byTenant, err := svc.MigrateAllTenants(ctx, false)
	require.NoError(t, err)
	require.Len(t, byTenant[tenant1.ID], 1)
	require.Len(t, byTenant[tenant2.ID], 1)
}

func TestLegacyApprovalMigrationService_MigrateAllForTenant_DryRunDoesNotPersist(t *testing.T) {
	client := newMigrationTestClient(t)
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Dry Run Tenant").SetCode("dry-run-tenant").SetDomain("dry-run.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ApprovalWorkflow.Create().
		SetName("试运行审批").SetIsActive(true).SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{{"level": 1, "name": "审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"}}).
		Save(ctx)
	require.NoError(t, err)

	svc := NewLegacyApprovalMigrationService(client)
	results, err := svc.MigrateAllForTenant(ctx, tenant.ID, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEmpty(t, results[0].BPMNXML, "dry run 应该返回生成的 XML 供人工检查")

	count, err := client.ProcessDefinition.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "dry run 不应该真的部署流程定义")
}
```

- [ ] **Step 2: 运行测试，确认按预期失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestLegacyApprovalMigrationService_MigrateAll' -v`
Expected：编译失败——`MigrateAllForTenant`/`MigrateAllTenants` 方法还不存在。`LegacyApprovalMigrationResult` 也还没有 `Error` 字段（当前只有 `WorkflowID`/`ProcessDefinitionKey`/`BPMNXML`/`Skipped`），下一步一起加。

- [ ] **Step 3: 给 `LegacyApprovalMigrationResult` 加 `Error` 字段，新增两个批量方法**

在 `itsm-backend/service/legacy_approval_migration_service.go` 里，把 `LegacyApprovalMigrationResult` 的定义（现有）：

```go
type LegacyApprovalMigrationResult struct {
	WorkflowID           int    `json:"workflowId"`
	ProcessDefinitionKey string `json:"processDefinitionKey"`
	BPMNXML              string `json:"bpmnXml,omitempty"`
	Skipped              bool   `json:"skipped"`
}
```

改成（加一个 `Error` 字段）：

```go
type LegacyApprovalMigrationResult struct {
	WorkflowID           int    `json:"workflowId"`
	ProcessDefinitionKey string `json:"processDefinitionKey"`
	BPMNXML              string `json:"bpmnXml,omitempty"`
	Skipped              bool   `json:"skipped"`
	Error                string `json:"error,omitempty"`
}
```

在 `Migrate` 方法（现有，不改它本身的逻辑）后面新增两个方法：

```go
// MigrateAllForTenant 迁移一个租户下所有启用的 ApprovalWorkflow。单个工作流迁移失败不中止
// 整个批次——把错误信息记进对应的 LegacyApprovalMigrationResult.Error，继续处理下一个，
// 避免一条写得有问题的自定义工作流拖累同租户其它工作流的迁移。
func (s *LegacyApprovalMigrationService) MigrateAllForTenant(ctx context.Context, tenantID int, dryRun bool) ([]*LegacyApprovalMigrationResult, error) {
	workflows, err := s.client.ApprovalWorkflow.Query().
		Where(approvalworkflow.TenantIDEQ(tenantID), approvalworkflow.IsActiveEQ(true)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query approval workflows for tenant %d: %w", tenantID, err)
	}

	results := make([]*LegacyApprovalMigrationResult, 0, len(workflows))
	for _, workflow := range workflows {
		result, migrateErr := s.Migrate(ctx, workflow, dryRun)
		if migrateErr != nil {
			results = append(results, &LegacyApprovalMigrationResult{
				WorkflowID: workflow.ID,
				Error:      migrateErr.Error(),
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// MigrateAllTenants 遍历所有租户，对每个租户调用 MigrateAllForTenant，按 tenantID 汇总结果。
func (s *LegacyApprovalMigrationService) MigrateAllTenants(ctx context.Context, dryRun bool) (map[int][]*LegacyApprovalMigrationResult, error) {
	tenants, err := s.client.Tenant.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query tenants: %w", err)
	}

	byTenant := make(map[int][]*LegacyApprovalMigrationResult, len(tenants))
	for _, tenant := range tenants {
		results, err := s.MigrateAllForTenant(ctx, tenant.ID, dryRun)
		if err != nil {
			return nil, fmt.Errorf("failed to migrate tenant %d: %w", tenant.ID, err)
		}
		byTenant[tenant.ID] = results
	}
	return byTenant, nil
}
```

在文件顶部 `import` 块里补上 `"itsm-backend/ent/approvalworkflow"`（如果还没有的话）。

- [ ] **Step 4: 运行测试，确认全部通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestLegacyApprovalMigrationService_MigrateAll' -v`
Expected：全部 PASS。

- [ ] **Step 5: 新建批量迁移 CLI**

创建 `itsm-backend/cmd/migrate_legacy_approvals/main.go`：

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/service"

	"go.uber.org/zap"
)

func main() {
	tenantID := flag.Int("tenant-id", 0, "只迁移指定租户（0 表示迁移所有租户）")
	dryRun := flag.Bool("dry-run", true, "只生成 BPMN XML 并打印，不真的部署/建绑定（默认开启，显式传 -dry-run=false 才会真的写库）")
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
		"ops:migrate_legacy_approvals",
		"batch-migrate tenant-customized ApprovalWorkflow records to BPMN",
	)

	migrationSvc := service.NewLegacyApprovalMigrationService(client)

	if *tenantID > 0 {
		results, err := migrationSvc.MigrateAllForTenant(ctx, *tenantID, *dryRun)
		if err != nil {
			sugar.Fatalw("migration failed", "tenant_id", *tenantID, "error", err)
		}
		printResults(sugar, *tenantID, results, *dryRun)
		return
	}

	byTenant, err := migrationSvc.MigrateAllTenants(ctx, *dryRun)
	if err != nil {
		sugar.Fatalw("migration failed", "error", err)
	}
	for tid, results := range byTenant {
		printResults(sugar, tid, results, *dryRun)
	}
}

func printResults(sugar *zap.SugaredLogger, tenantID int, results []*service.LegacyApprovalMigrationResult, dryRun bool) {
	migrated, skipped, failed := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Error != "":
			failed++
			sugar.Warnw("workflow migration failed", "tenant_id", tenantID, "workflow_id", r.WorkflowID, "error", r.Error)
		case r.Skipped:
			skipped++
		default:
			migrated++
		}
	}
	sugar.Infow("tenant migration summary",
		"tenant_id", tenantID, "dry_run", dryRun,
		"migrated", migrated, "skipped_already_migrated", skipped, "failed", failed,
	)
}
```

- [ ] **Step 6: 编译验证 CLI**

Run: `cd itsm-backend && go build ./cmd/migrate_legacy_approvals/`
Expected：编译成功，生成一个可执行文件（编译产物不需要提交，跑完确认能编译就删掉：`rm -f migrate_legacy_approvals`）。

- [ ] **Step 7: 跑整个 service 包和全量测试，确认没有回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... -v 2>&1 | tail -150`
Expected：编译通过，重点确认 Task 1、Task 2 新增的所有测试都 PASS，其它既有测试没有回归。

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok"`
Expected：没有 FAIL 输出。

- [ ] **Step 8: Commit**

```bash
cd itsm-backend
git add service/legacy_approval_migration_service.go service/legacy_approval_migration_service_unit_test.go cmd/migrate_legacy_approvals/main.go
git commit -m "feat(bpmn): add batch migration CLI for tenant-customized ApprovalWorkflow records

MigrateAllForTenant/MigrateAllTenants iterate active ApprovalWorkflow
rows and call the now-fixed Migrate() per workflow; one workflow's
failure doesn't block its tenant's other workflows. New standalone
ops CLI (cmd/migrate_legacy_approvals, mirroring cmd/provision_tenant's
shape) defaults to --dry-run so an operator can review generated BPMN
XML before committing to a real run with --dry-run=false."
```
