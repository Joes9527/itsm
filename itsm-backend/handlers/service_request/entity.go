package service_request

import (
	"context"
	"fmt"
	"time"
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
	Version            int
	ProcessorID        *int
	StartedAt          *time.Time
	CompletedAt        *time.Time
	CompletionNote     string
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time

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
	Create(ctx context.Context, req *ServiceRequest) (*ServiceRequest, error)
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

// mapITSMType 将 catalog.itsm_type 映射为 Ticket.type。Incident 类型不通过
// Ticket 审批路径，调用方应在入此函数前分流。
// 映射规则：Request → service_request, Change → change, Incident 不应到达此处。
func mapITSMType(itsmType string) string {
	switch itsmType {
	case "Change":
		return "change"
	case "Incident":
		return "incident"
	default:
		return "service_request" // Request 及兜底
	}
}

// isIncidentCatalog 判断服务目录项的 ITSM 类型是否为事件——事件无需审批，
// 直接分派给 Resolver，不走 SR→Ticket 审批流程。
func isIncidentCatalog(itsmType string) bool {
	return itsmType == "Incident"
}
