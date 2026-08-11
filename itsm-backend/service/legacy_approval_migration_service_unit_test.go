package service

import (
	"context"
	"strings"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processbinding"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestBuildLegacyApprovalBPMN_ApproverIDs 锁定固定审批人 ID 列表的迁移：管理端"指定用户"
// （approverType=user + 固定审批人ID）配出来的节点没有 assigneeType/assigneeValue，以前会掉进
// default 分支整条工作流迁移失败。ApproverIDs 应该写成 candidateUsers 的十进制 ID CSV。
func TestBuildLegacyApprovalBPMN_ApproverIDs(t *testing.T) {
	nodes := []map[string]interface{}{
		{"level": 1, "name": "指定审批人", "approverType": "user", "approverIds": []int{5, 7}, "approvalMode": "any"},
	}
	xml, err := buildLegacyApprovalBPMN("legacy_8", "指定审批人工作流", nodes)
	require.NoError(t, err, "只配了 ApproverIDs 的节点不应该被当成无法识别的 assignee type 而中止迁移")
	assert.Contains(t, xml, `itsm:candidateUsers="5,7"`, "ApproverIDs 应该转成 candidateUsers 的十进制 ID CSV")
	assert.Contains(t, xml, `itsm:taskPurpose="approval"`)

	parsed, err := NewBPMNParser().ParseXML([]byte(xml))
	require.NoError(t, err)
	assert.Equal(t, "5,7", parsed.Processes[0].UserTasks[0].CandidateUsers, "生成的 candidateUsers 必须能被 BPMN 解析器读回来")
}

// TestBuildLegacyApprovalBPMN_ApproverIDsWinOverAssigneeType 锁定优先级顺序，跟遗留运行时
// ApprovalService（service/approval_service.go:724-732）保持一致：非空 ApproverIDs 优先，
// AssigneeType/AssigneeValue 只在 ApproverIDs 为空时才被采纳。
func TestBuildLegacyApprovalBPMN_ApproverIDsWinOverAssigneeType(t *testing.T) {
	nodes := []map[string]interface{}{
		{
			"level": 1, "name": "双配置节点",
			"approverIds":   []int{11},
			"assigneeType":  "role",
			"assigneeValue": "manager",
			"approvalMode":  "any",
		},
	}
	xml, err := buildLegacyApprovalBPMN("legacy_9", "优先级测试", nodes)
	require.NoError(t, err)
	assert.Contains(t, xml, `itsm:candidateUsers="11"`, "同时配了 ApproverIDs 和 AssigneeType 时，ApproverIDs 优先")
	assert.NotContains(t, xml, "assigneeRole", "ApproverIDs 非空时不应该再落到 assigneeType 分支")
}

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

// TestLegacyApprovalMigrationService_Migrate_CreatesReachableProcessBinding 锁定迁移产出的
// ProcessBinding 行形状。以前 Migrate 把 business_type 写成 workflow.TicketType（如
// "service_request"）且 business_sub_type 留空，这种行 FindBestBinding 永远查不到——
// migrated=1 报成功，但没有任何工单会路由到新部署的流程定义。正确形状是
// business_type="ticket" + business_sub_type=<工单类型>，跟 config/seed/default.json 一致。
func TestLegacyApprovalMigrationService_Migrate_CreatesReachableProcessBinding(t *testing.T) {
	client := newMigrationTestClient(t)
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Binding Shape Tenant").SetCode("binding-shape-tenant").
		SetDomain("binding-shape.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	workflow, err := client.ApprovalWorkflow.Create().
		SetName("服务请求审批").
		SetTicketType("service_request").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{
			{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"},
		}).
		Save(ctx)
	require.NoError(t, err)

	svc := NewLegacyApprovalMigrationService(client)
	result, err := svc.Migrate(ctx, workflow, false)
	require.NoError(t, err)
	require.False(t, result.Skipped)

	binding, err := client.ProcessBinding.Query().
		Where(processbinding.ProcessDefinitionKey(result.ProcessDefinitionKey)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, string(dto.BusinessTypeTicket), binding.BusinessType, "business_type 必须是 ticket，不是工单子类型")
	assert.Equal(t, workflow.TicketType, binding.BusinessSubType, "工单类型应该写进 business_sub_type")
	assert.False(t, binding.IsDefault, "迁移出来的按子类型绑定不应该抢占租户的默认绑定")

	// 用 ProcessResolver 验证这行绑定真的可达——跟 process_resolver_test.go 的验证方式一致。
	resolver := NewProcessResolver(client, NewProcessBindingService(client))
	ticket := &ent.Ticket{Type: "service_request", Priority: "medium", TenantID: tenant.ID}
	key, err := resolver.Resolve(ctx, ticket, "")
	require.NoError(t, err)
	assert.Equal(t, result.ProcessDefinitionKey, key, "迁移出来的绑定必须能被 resolver 匹配到，而不是落回 ticket_general_flow 兜底")
}

// TestLegacyApprovalMigrationService_Migrate_EmptyTicketTypeBindingIsReachable 覆盖
// workflow.TicketType 为空的情况：子类型兜底成 "ticket"，绑定依然可达（通用工单路径）。
func TestLegacyApprovalMigrationService_Migrate_EmptyTicketTypeBindingIsReachable(t *testing.T) {
	client := newMigrationTestClient(t)
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Empty Type Tenant").SetCode("empty-type-tenant").
		SetDomain("empty-type.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	workflow, err := client.ApprovalWorkflow.Create().
		SetName("无类型审批").SetIsActive(true).SetTenantID(tenant.ID).
		SetNodes([]map[string]interface{}{
			{"level": 1, "name": "经理审批", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any"},
		}).
		Save(ctx)
	require.NoError(t, err)

	svc := NewLegacyApprovalMigrationService(client)
	result, err := svc.Migrate(ctx, workflow, false)
	require.NoError(t, err)

	binding, err := client.ProcessBinding.Query().
		Where(processbinding.ProcessDefinitionKey(result.ProcessDefinitionKey)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, string(dto.BusinessTypeTicket), binding.BusinessType)
	assert.Equal(t, string(dto.BusinessTypeTicket), binding.BusinessSubType, "TicketType 为空时子类型兜底成 ticket")

	resolver := NewProcessResolver(client, NewProcessBindingService(client))
	key, err := resolver.Resolve(ctx, &ent.Ticket{Type: "ticket", Priority: "medium", TenantID: tenant.ID}, "")
	require.NoError(t, err)
	assert.Equal(t, result.ProcessDefinitionKey, key)
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
