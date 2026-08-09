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
