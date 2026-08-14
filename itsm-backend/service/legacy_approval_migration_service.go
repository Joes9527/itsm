package service

import (
	"context"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/approvalworkflow"
	"itsm-backend/ent/processdefinition"
)

type LegacyApprovalMigrationService struct {
	client     *ent.Client
	deployment *BPMNDeploymentService
	binding    *ProcessBindingService
}

func NewLegacyApprovalMigrationService(client *ent.Client) *LegacyApprovalMigrationService {
	return &LegacyApprovalMigrationService{client: client, deployment: NewBPMNDeploymentService(client), binding: NewProcessBindingService(client)}
}

type LegacyApprovalMigrationResult struct {
	WorkflowID           int    `json:"workflowId"`
	ProcessDefinitionKey string `json:"processDefinitionKey"`
	BPMNXML              string `json:"bpmnXml,omitempty"`
	Skipped              bool   `json:"skipped"`
	Error                string `json:"error,omitempty"`
}

func (s *LegacyApprovalMigrationService) Migrate(ctx context.Context, workflow *ent.ApprovalWorkflow, dryRun bool) (*LegacyApprovalMigrationResult, error) {
	// 没有工单类型的遗留工作流没法产出可达的 ProcessBinding（见下面创建绑定处的注释），
	// 在最前面就失败——dry-run 预览也应该看到这个错误，而不是等到真正部署后才发现。
	if workflow.TicketType == "" {
		return nil, fmt.Errorf("workflow %q has no ticket type -- cannot create a reachable ProcessBinding, migration aborted", workflow.Name)
	}
	key := fmt.Sprintf("legacy_approval_%d", workflow.ID)
	bpmnXML, err := buildLegacyApprovalBPMN(key, workflow.Name, workflow.Nodes)
	if err != nil {
		return nil, err
	}
	result := &LegacyApprovalMigrationResult{WorkflowID: workflow.ID, ProcessDefinitionKey: key, BPMNXML: bpmnXML}
	if dryRun {
		return result, nil
	}
	exists, err := s.client.ProcessDefinition.Query().Where(processdefinition.Key(key), processdefinition.TenantID(workflow.TenantID)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		result.Skipped = true
		return result, nil
	}
	_, err = s.deployment.DeployProcessDefinition(ctx, &DeployProcessDefinitionRequest{Name: workflow.Name, Description: "Migrated from legacy approval workflow", BPMNXML: bpmnXML, TenantID: workflow.TenantID})
	if err != nil {
		return nil, err
	}
	// ProcessBinding 必须写成 business_type="ticket" + business_sub_type=<具体工单类型> ——
	// 这是 ProcessResolver.FindBestBinding（service/process_resolver.go 用
	// dto.BusinessTypeTicket + ticket.Type 查询）唯一能命中的形状，也是 config/seed/default.json
	// 种子数据已经修正成的形状。写成 business_type=workflow.TicketType 的绑定行永远不可达。
	businessSubType := workflow.TicketType
	conditions := map[string]interface{}{}
	if workflow.Priority != "" {
		conditions["priority"] = workflow.Priority
	}
	_, err = s.binding.CreateBinding(ctx, &dto.ProcessBinding{BusinessType: dto.BusinessTypeTicket, BusinessSubType: businessSubType, ProcessDefinitionKey: key, ProcessVersion: 1, Priority: 50, IsActive: workflow.IsActive, TenantID: workflow.TenantID, Conditions: conditions})
	if err != nil {
		return nil, err
	}
	return result, nil
}

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
		id := fmt.Sprintf("Approval_%d", i+1)
		var attr, value string

		// ApproverIDs（固定审批人 ID 列表）优先级高于 AssigneeType/AssigneeValue——跟遗留运行时
		// ApprovalService 的 trigger-approval 路径（service/approval_service.go:724-732）保持一致：
		// 非空的 ApproverIDs 直接生效，AssigneeType/AssigneeValue 只是它为空时的兜底。
		// 写成 candidateUsers 的十进制 ID CSV 是引擎已支持的形状：resolveFixedScopeAssignee
		// （service/bpmn_process_engine.go）本身就产出 strconv.Itoa(userID)，authorizeTaskActor
		// 的候选人匹配也接受 ID 字符串或用户名，不需要额外做用户名查找。
		// 过滤掉 <=0 的 ID（脏数据/未选中的占位值）——前端 normalizeNodes 已经这样过滤，
		// 但走 API 直接写入的数据不保证同样干净，一个 "0" 会让 candidateUsers 非空，
		// 短路掉 bpmn_process_engine.go 的自动候选人解析，产出一个没人能认领的任务。
		validIDs := make([]string, 0, len(cfg.ApproverIDs))
		for _, approverID := range cfg.ApproverIDs {
			if approverID > 0 {
				validIDs = append(validIDs, strconv.Itoa(approverID))
			}
		}
		if len(validIDs) > 0 {
			attr, value = "candidateUsers", strings.Join(validIDs, ",")
		} else {
			// ApproverType 兜底到 AssigneeType——复用 ApprovalService.parseWorkflowNodes
			// （service/approval_service.go）同样的约定，不是这里新发明的规则。
			assigneeType := cfg.AssigneeType
			if assigneeType == "" {
				switch cfg.ApproverType {
				case dto.ApprovalNodeTypeDeptManager, dto.ApprovalNodeTypeTeamLeader,
					dto.ApprovalNodeTypeProjectManager, dto.ApprovalNodeTypeTempTeamLeader,
					dto.ApprovalNodeTypeAmountBased:
					assigneeType = string(cfg.ApproverType)
				case dto.ApprovalNodeTypeRole:
					// "role" 是 defaultApprovalNode 的默认选中值（admin/approvals/page.tsx），
					// 且"动态解析类型"下拉框本来就不提供 role 选项——操作员在这种情况下唯一能
					// 表达角色名的地方就是"解析值"（AssigneeValue）文本框。不兜底的话，任何
					// 留在默认审批人类型、没手动切换 assigneeType 的节点都会打到下面的
					// default 分支报错中止,而这是最常见的默认状态,不是异常数据。
					assigneeType = string(cfg.ApproverType)
				}
			}

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
		}

		// cfg.ApprovalMode 透传给 BPMN，而不是硬编码 "single"：createUserTask
		// （service/bpmn_process_engine.go:809）只有 task.ApprovalMode 非空且不等于 "single"
		// 时才会建会签任务。硬编码 "single" 会把遗留数据里 approvalMode="all"（3 个 ApproverIDs
		// 必须全部通过）悄悄降级成 1 个候选人认领就算通过的单人审批——在 ApproverIDs 支持加进来
		// 之前这个硬编码是死代码（没有节点能产出多个候选人），现在会真的削弱审批强度。
		approvalMode := string(cfg.ApprovalMode)
		if approvalMode == "" {
			approvalMode = "single"
		}
		fmt.Fprintf(&tasks, `<bpmn:userTask id="%s" name="%s" itsm:taskPurpose="approval" itsm:approvalMode="%s" itsm:%s="%s" itsm:commentRequiredOnReject="true"/>`, id, escape(cfg.Name), escape(approvalMode), attr, escape(value))
		fmt.Fprintf(&flows, `<bpmn:sequenceFlow id="Flow_%d" sourceRef="%s" targetRef="%s"/>`, i+1, previous, id)
		previous = id
	}
	fmt.Fprintf(&flows, `<bpmn:sequenceFlow id="Flow_%d" sourceRef="%s" targetRef="EndEvent_1"/>`, len(configs)+1, previous)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:itsm="https://github.com/heidsoft/itsm/schema/bpmn" id="Definitions_%s" targetNamespace="https://github.com/heidsoft/itsm"><bpmn:process id="%s" name="%s" isExecutable="true"><bpmn:startEvent id="StartEvent_1"/>%s<bpmn:endEvent id="EndEvent_1"/>%s</bpmn:process></bpmn:definitions>`, escape(key), escape(key), escape(name), tasks.String(), flows.String()), nil
}
