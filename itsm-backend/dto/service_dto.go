package dto

import (
	"time"
)

// UserResponse 用户响应
type UserResponse struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

// CreateServiceRequestRequest 创建服务请求请求
type CreateServiceRequestRequest struct {
	CatalogID int            `json:"catalogId" binding:"omitempty,min=1"`
	Title     string         `json:"title" binding:"omitempty,max=255"`
	Reason    string         `json:"reason" binding:"omitempty,max=500"`
	FormData  map[string]any `json:"formData" binding:"omitempty"`

	CostCenter         string     `json:"costCenter" binding:"omitempty,max=100"`
	DataClassification string     `json:"dataClassification" binding:"omitempty,oneof=public internal confidential restricted"`
	NeedsPublicIP      bool       `json:"needsPublicIp"`
	SourceIPWhitelist  []string   `json:"sourceIpWhitelist" binding:"omitempty"`
	ExpireAt           *time.Time `json:"expireAt" binding:"omitempty"`
	ComplianceAck      bool       `json:"complianceAck"`

	// 通用层字段：所有 service_type 都适用。
	ContactName  string     `json:"contactName" binding:"omitempty,max=100"`
	ContactEmail string     `json:"contactEmail" binding:"omitempty,max=255,email"`
	Quantity     int        `json:"quantity" binding:"omitempty,min=1,max=1000"`
	ExpectedAt   *time.Time `json:"expectedAt" binding:"omitempty"`
}

// UpdateServiceRequestRequest 更新服务请求请求
//
// Title/Reason 已移除：它们现在是 ticket-owned 字段，只在创建时设置一次
// （委托给关联的 Ticket，详见 handlers/service_request/service.go 的 Create）。
// Update 从未读取过它们；保留会让客户端以为传 title 能生效，实际被静默丢弃。
type UpdateServiceRequestRequest struct {
	FormData map[string]any `json:"formData" binding:"omitempty"`

	CostCenter         string     `json:"costCenter" binding:"omitempty,max=100"`
	DataClassification string     `json:"dataClassification" binding:"omitempty,oneof=public internal confidential restricted"`
	NeedsPublicIP      *bool      `json:"needsPublicIp"`
	SourceIPWhitelist  []string   `json:"sourceIpWhitelist" binding:"omitempty"`
	ExpireAt           *time.Time `json:"expireAt" binding:"omitempty"`
	ComplianceAck      *bool      `json:"complianceAck"`
}

// GetServiceCatalogsRequest 获取服务目录请求
type GetServiceCatalogsRequest struct {
	Page     int    `json:"page" form:"page" binding:"omitempty,min=1"`
	Size     int    `json:"size" form:"size" binding:"omitempty,min=1,max=100"`
	Category string `json:"category" form:"category"`
	Status   string `json:"status" form:"status" binding:"omitempty,oneof=enabled disabled"`
}

// GetServiceRequestsRequest 获取服务请求列表请求
type GetServiceRequestsRequest struct {
	Page   int    `json:"page" form:"page" binding:"omitempty,min=1"`
	Size   int    `json:"size" form:"size" binding:"omitempty,min=1,max=100"`
	Status string `json:"status" form:"status" binding:"omitempty"`
	UserID int    `json:"-"` // 从认证中间件获取
}

// ServiceCatalogResponse 服务目录响应
type ServiceCatalogResponse struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	DeliveryTime   string `json:"deliveryTime"`
	CITypeID       int    `json:"ciTypeId,omitempty"`
	CloudServiceID int    `json:"cloudServiceId,omitempty"`
	// ProcessDefinitionKey 是该目录条目专属的 BPMN 流程定义 Key（可选），非空时优先于
	// businessType+businessSubType 的通用流程绑定解析。
	ProcessDefinitionKey string `json:"processDefinitionKey,omitempty"`
	Status               string `json:"status"`
	// ServiceType 服务类型：vm|rds|network|database|storage|oss|security|access|custom 等，
	// 决定 RequiresInfraFields 的计算结果，同时供管理端编辑页回显"服务类型"下拉框。
	ServiceType string `json:"serviceType,omitempty"`
	// RequiresInfraFields 由后端根据 service_type 计算：vm/rds/network/database/storage/oss
	// 为 true（security 及其余类型为 false）。前端申请表单据此决定是否渲染"成本中心/数据分级/
	// 公网IP/IP白名单/资源过期时间/合规确认"这组基础设施字段，不自行判断 service_type
	// （见 handlers/service_catalog/entity.go 的 RequiresInfraFields 函数注释）。
	RequiresInfraFields bool `json:"requiresInfraFields"`
	// TargetClass 是该目录项对应的 WorkItem 目标类：service_request_item|incident|change_request，
	// 由调用方在创建/更新时显式提供并由后端校验，是 service_request 域路由判断的唯一权威
	// 依据；已退役的 itsm_type 不再承担这个职责（design doc §7.2）。
	TargetClass string                   `json:"targetClass,omitempty"`
	Fields      []map[string]interface{} `json:"fields,omitempty"`
	CreatedAt   time.Time                `json:"createdAt"`
	UpdatedAt   time.Time                `json:"updatedAt"`
}

// ServiceRequestResponse 服务请求响应
type ServiceRequestResponse struct {
	ID          int            `json:"id"`
	TicketID    int            `json:"ticketId"`
	CatalogID   int            `json:"catalogId"`
	RequesterID int            `json:"requesterId"`
	CIID        int            `json:"ciId,omitempty"`
	FormData    map[string]any `json:"formData,omitempty"`

	CostCenter         string     `json:"costCenter,omitempty"`
	DataClassification string     `json:"dataClassification,omitempty"`
	NeedsPublicIP      bool       `json:"needsPublicIp"`
	SourceIPWhitelist  []string   `json:"sourceIpWhitelist,omitempty"`
	ExpireAt           *time.Time `json:"expireAt,omitempty"`
	ComplianceAck      bool       `json:"complianceAck"`

	ContactName  string     `json:"contactName,omitempty"`
	ContactEmail string     `json:"contactEmail,omitempty"`
	Quantity     int        `json:"quantity"`
	ExpectedAt   *time.Time `json:"expectedAt,omitempty"`

	Version        int        `json:"version"`
	ProcessorID    *int       `json:"processorId,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	CompletionNote string     `json:"completionNote,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`

	Catalog      *ServiceCatalogResponse    `json:"catalog,omitempty"`
	Requester    *UserResponse              `json:"requester,omitempty"`
	CustomFields []CustomFieldValueResponse `json:"customFields,omitempty"`

	// TicketTitle/TicketStatus 是列表场景（GET /api/v1/service-requests/me 等）下批量回填的
	// 关联 ticket 展示字段，不是持久化数据——状态/标题的唯一事实来源仍然是 Ticket。
	// /my-requests 列表页用它们替代已经删掉的 ServiceRequest.status/title 列。
	TicketTitle  string `json:"ticketTitle,omitempty"`
	TicketStatus string `json:"ticketStatus,omitempty"`

	Actions map[string]ActionPermission `json:"actions,omitempty"`
}

// ServiceCatalogListResponse 服务目录列表响应
type ServiceCatalogListResponse struct {
	Catalogs []ServiceCatalogResponse `json:"catalogs"`
	Total    int                      `json:"total"`
	Page     int                      `json:"page"`
	Size     int                      `json:"size"`
}

// ServiceRequestListResponse 服务请求列表响应
type ServiceRequestListResponse struct {
	Items []ServiceRequestResponse `json:"items"`
	Total int                      `json:"total"`
	Page  int                      `json:"page"`
	Size  int                      `json:"size"`
}

// CreateServiceCatalogRequest 创建服务目录请求
type CreateServiceCatalogRequest struct {
	Name           string `json:"name" binding:"required,max=255"`
	Category       string `json:"category" binding:"required,max=100"`
	Description    string `json:"description" binding:"omitempty,max=1000"`
	DeliveryTime   string `json:"deliveryTime" binding:"omitempty,max=50"`
	CITypeID       int    `json:"ciTypeId,omitempty"`
	CloudServiceID int    `json:"cloudServiceId,omitempty"`
	// ProcessDefinitionKey 可选，指定该目录条目提交后走哪个 BPMN 流程定义，
	// 不填则沿用 businessType+businessSubType 的通用流程绑定解析。
	ProcessDefinitionKey string                   `json:"processDefinitionKey" binding:"omitempty,max=255"`
	Status               string                   `json:"status" binding:"omitempty,oneof=enabled disabled"`
	Fields               []map[string]interface{} `json:"fields,omitempty"`
	// ServiceType 决定是否需要基础设施字段，见 handlers/service_catalog.RequiresInfraFields。
	// 取值：vm|rds|oss|network|storage|security|custom（ent schema servicecatalog.go 字段注释）。
	ServiceType string `json:"serviceType" binding:"omitempty,max=50"`
	// TargetClass 是该目录项对应的 WorkItem 目标类，创建时必填——不再由后端从已退役的
	// itsm_type 派生（design doc §7.2，migration 024_service_catalog_target_class_authority）。
	TargetClass string `json:"targetClass" binding:"required,oneof=service_request_item incident change_request"`
}

// UpdateServiceCatalogRequest 更新服务目录请求
type UpdateServiceCatalogRequest struct {
	Name                 string                   `json:"name" binding:"omitempty,max=255"`
	Category             string                   `json:"category" binding:"omitempty,max=100"`
	Description          string                   `json:"description" binding:"omitempty,max=1000"`
	DeliveryTime         string                   `json:"deliveryTime" binding:"omitempty,max=50"`
	CITypeID             int                      `json:"ciTypeId,omitempty"`
	CloudServiceID       int                      `json:"cloudServiceId,omitempty"`
	ProcessDefinitionKey string                   `json:"processDefinitionKey" binding:"omitempty,max=255"`
	Status               string                   `json:"status" binding:"omitempty,oneof=enabled disabled"`
	Fields               []map[string]interface{} `json:"fields,omitempty"`
	ServiceType          string                   `json:"serviceType" binding:"omitempty,max=50"`
	// TargetClass 省略时保留目录项当前值；提供时必须是三个合法取值之一
	// （Handler.Update/Service.Update 的"保留-或-校验替换"语义，见 handler.go/service.go）。
	TargetClass string `json:"targetClass,omitempty" binding:"omitempty,oneof=service_request_item incident change_request"`
}
