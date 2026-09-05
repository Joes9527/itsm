package service_request

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/handlers/service_catalog"
)

// ServiceRequest represents the domain entity for a service request.
// 状态/标题/审批全部委托给关联的 Ticket（TicketID）——本实体只保留服务目录来源
// 特有的字段（成本中心/合规/资源交付等）。
type ServiceRequest struct {
	ID                 int
	TenantID           int
	TicketID           int
	CatalogID          int
	RequesterID        int
	CiID               int
	FormData           map[string]interface{}
	CostCenter         string
	DataClassification string
	NeedsPublicIP      bool
	NeedsPublicIPSet   bool
	SourceIPWhitelist  []string
	ExpireAt           *time.Time
	ComplianceAck      bool
	ComplianceAckSet   bool
	// 通用层字段：所有 service_type 都适用，取代原来只进 FormData 就没人读的假字段。
	ContactName    string
	ContactEmail   string
	Quantity       int
	ExpectedAt     *time.Time
	Version        int
	ProcessorID    *int
	StartedAt      *time.Time
	CompletedAt    *time.Time
	CompletionNote string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	// TicketTitle/TicketStatus 是列表响应场景下由 Service.List 批量回填的展示字段——
	// 不是持久化列，只在内存里跟着 List 的返回值走一次，供 handler.toDTO 映射进
	// ServiceRequestResponse.TicketTitle/TicketStatus（/my-requests 列表页用，
	// 省掉前端对每条记录再单独查一次 ticket 的往返）。Get/Create/Update 路径不填这两个字段。
	TicketTitle  string
	TicketStatus string
}

// ListFilters defines filters for listing service requests
type ListFilters struct {
	UserID int // Requester ID
	Page   int
	Size   int
}

// Repository defines the interface for data persistence
type Repository interface {
	Get(ctx context.Context, id, tenantID int) (*ServiceRequest, error)
	GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error)
	List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceRequest, int, error)
	Update(ctx context.Context, req *ServiceRequest) error
	Delete(ctx context.Context, req *ServiceRequest) error
	GetUserContext(ctx context.Context, userID, tenantID int) (department, name string, err error)
}

// amount 从 FormData 中提取预估金额（float64），常见键名：amount、cost、budget。
// 返回 0 表示未填写金额或不涉及金额逻辑。
func (sr *ServiceRequest) amount() float64 {
	if sr == nil || sr.FormData == nil {
		return 0
	}
	for _, key := range []string{"amount", "cost", "budget"} {
		if v, ok := sr.FormData[key]; ok {
			switch n := v.(type) {
			case float64:
				return n
			case int:
				return float64(n)
			case string:
				// 简单转换，忽略解析错误
				var f float64
				fmt.Sscanf(n, "%f", &f)
				return f
			}
		}
	}
	return 0
}

// injectApprovalChain 将解析出的审批链步骤注入 FormData 的 _approval_chain 键中。
// 若 steps 为 nil 则不注入，避免在 form_data 中留下空键。
// hasApprovalChainSteps 判断审批链解析结果是否包含实际步骤。
// resolvedSteps 在 service.go 中已通过 len(chain.Steps) > 0 过滤，非 nil 即有效。
func hasApprovalChainSteps(steps interface{}) bool {
	return steps != nil
}

func injectApprovalChain(formData map[string]interface{}, steps interface{}) map[string]interface{} {
	if steps == nil {
		return formData
	}
	if formData == nil {
		formData = make(map[string]interface{})
	}
	formData["_approval_chain"] = steps
	return formData
}

// stripStructuredFieldKeys 返回 formData 的浅拷贝，剔除已经通过 extractServiceRequestFieldValues
// 摘出、即将写入 field_values 的结构化字段键，只保留 _approval_chain 这类系统流程上下文键
// 和没有对应字段定义的自由内容（design doc §8.3："ServiceRequest.form_data 只保留尚未结构化
// 的复合输入或流程上下文，不能与 field_values 同时成为同一字段的权威来源"）。
//
// fieldValues 是 extractServiceRequestFieldValues(formData) 的返回值，调用方在 Create() 里只
// 提取一次、两处复用（校验必填 + 这里去重），避免同一逻辑跑两遍导致行为漂移。
//
// 两种输入形状分别处理（对齐 extractServiceRequestFieldValues 的两条路径）：
//   - 数组形状：结构化字段值整体存在 formData["customFieldValues"] 这一个键下（[]{name,value}），
//     这个数组已经原样转换进了 fieldValues/field_values，直接删除这一个顶层键即可；不能按
//     fieldValues 的 key 逐个删，因为那些 key 是数组元素内部的值，不是 formData 的顶层键。
//   - 兼容的旧 map 形状：field_values 里的每个 key 本来就是 formData 顶层键，逐个删除。
func stripStructuredFieldKeys(formData map[string]interface{}, fieldValues map[string]interface{}) map[string]interface{} {
	if formData == nil {
		return nil
	}
	result := make(map[string]interface{}, len(formData))
	for k, v := range formData {
		result[k] = v
	}
	if len(fieldValues) == 0 {
		return result
	}
	if _, arrayShape := formData["customFieldValues"]; arrayShape {
		delete(result, "customFieldValues")
		return result
	}
	for k := range fieldValues {
		delete(result, k)
	}
	return result
}

// mapTargetClassToTicketType 将 catalog.target_class（WorkItem 目标类）映射为 Ticket.type。
// Wave 2 之前这里读的是 catalog.itsm_type；现在 target_class 是路由的唯一权威来源
// （design doc §7.2、AGENTS.md 禁止 itsm_type/target_class 两个字段并存做路由依据）——
// handlers/service_catalog 在 ServiceCatalog 创建/更新时已经把 itsm_type 同步计算进
// target_class（见 handlers/service_catalog/entity.go 的 ComputeTargetClass），这里不再
// 重新读取或解释 itsm_type。
// incident 类型不通过 Ticket 审批路径，调用方应在入此函数前用 isIncidentCatalog 分流；
// 这里的 "incident" 分支只是防御性兜底，正常不会走到。
// 映射规则：service_request_item → service_request, change_request → change,
// incident 不应到达此处。
func mapTargetClassToTicketType(targetClass string) string {
	switch targetClass {
	case service_catalog.TargetClassChangeRequest:
		return "change"
	case service_catalog.TargetClassIncident:
		return "incident"
	default:
		return "service_request" // service_request_item、空值（未跑回填）及兜底
	}
}

// isIncidentCatalog 判断服务目录项的 WorkItem 目标类是否为事件——事件无需审批，
// 直接分派给 Resolver，不走 SR→Ticket 审批流程。参数是 target_class（不是 itsm_type，
// 见 mapTargetClassToTicketType 的注释）。
func isIncidentCatalog(targetClass string) bool {
	return targetClass == service_catalog.TargetClassIncident
}
