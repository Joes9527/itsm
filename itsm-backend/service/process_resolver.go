package service

import (
	"context"

	"itsm-backend/dto"
	"itsm-backend/ent"
)

// ProcessResolver 解析工单应该使用哪个 BPMN 流程
// 优先级：1.请求指定 2.ProcessBinding 3.兜底
type ProcessResolver struct {
	client         *ent.Client
	bindingService ProcessBindingServiceInterface
}

// NewProcessResolver 创建流程解析器
func NewProcessResolver(client *ent.Client, bindingService ProcessBindingServiceInterface) *ProcessResolver {
	return &ProcessResolver{
		client:         client,
		bindingService: bindingService,
	}
}

// Resolve 解析工单应该使用的流程 Key
func (r *ProcessResolver) Resolve(ctx context.Context, ticket *ent.Ticket, reqKey string) (string, error) {
	// 优先级 1：请求参数显式指定
	if reqKey != "" {
		return reqKey, nil
	}

	// 优先级 2：ProcessBinding 表查询（按工单类型匹配）
	if r.bindingService != nil {
		binding, err := r.bindingService.FindBestBinding(
			ctx,
			dto.BusinessTypeTicket, // businessType
			ticket.Type,            // businessSubType (incident/problem/change/service_request)
			ticket.TenantID,
		)
		if err == nil && binding != nil {
			return binding.ProcessDefinitionKey, nil
		}
		// 查询失败或未找到，继续兜底
	}

	// 优先级 3：兜底默认
	return "ticket_general_flow", nil
}

// ResolveWithPriority 考虑优先级的解析（通用工单场景）
func (r *ProcessResolver) ResolveWithPriority(ctx context.Context, ticket *ent.Ticket, reqKey string) (string, error) {
	// 先走标准解析
	processKey, err := r.Resolve(ctx, ticket, reqKey)
	if err != nil {
		return "", err
	}

	// 如果是通用工单（没有匹配到特定类型），根据优先级调整
	if processKey == "ticket_general_flow" {
		if ticket.Priority == "high" || ticket.Priority == "urgent" {
			return "ticket_urgent_flow", nil
		}
	}

	// 服务请求场景同理：高/紧急优先级路由到独立的紧急服务请求流程。这条特判是
	// ProcessBinding.Conditions 机制在工单这条路径上走不到（TriggerProcess 只在
	// ProcessDefinitionKey 为空时才会去查会求值 Conditions 的 ProcessRoutingService，
	// 而这里的 processKey 永远非空）之后选定的替代方案，跟上面 ticket_general_flow
	// 那条保持同样的实现方式，不是发明新机制。
	if processKey == "service_request_flow" {
		if ticket.Priority == "high" || ticket.Priority == "urgent" {
			return "service_request_urgent_flow", nil
		}
	}

	return processKey, nil
}
