package service

import (
	"context"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
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
}

func (s *LegacyApprovalMigrationService) Migrate(ctx context.Context, workflow *ent.ApprovalWorkflow, dryRun bool) (*LegacyApprovalMigrationResult, error) {
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
	businessType := dto.BusinessType(workflow.TicketType)
	if businessType == "" {
		businessType = dto.BusinessTypeTicket
	}
	conditions := map[string]interface{}{}
	if workflow.Priority != "" {
		conditions["priority"] = workflow.Priority
	}
	_, err = s.binding.CreateBinding(ctx, &dto.ProcessBinding{BusinessType: businessType, ProcessDefinitionKey: key, ProcessVersion: 1, Priority: 50, IsActive: workflow.IsActive, TenantID: workflow.TenantID, Conditions: conditions})
	if err != nil {
		return nil, err
	}
	return result, nil
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
