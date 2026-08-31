package service_request

import (
	"context"
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
