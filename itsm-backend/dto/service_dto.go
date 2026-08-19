package dto

import (
	"strconv"
	"time"

	"itsm-backend/ent"
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
	ProcessDefinitionKey string                   `json:"processDefinitionKey,omitempty"`
	Status               string                   `json:"status"`
	Fields               []map[string]interface{} `json:"fields,omitempty"`
	CreatedAt            time.Time                `json:"createdAt"`
	UpdatedAt            time.Time                `json:"updatedAt"`
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

// ToServiceCatalogResponse 转换为服务目录响应
func ToServiceCatalogResponse(catalog *ent.ServiceCatalog) *ServiceCatalogResponse {
	return &ServiceCatalogResponse{
		ID:                   catalog.ID,
		Name:                 catalog.Name,
		Category:             catalog.Category,
		Description:          catalog.Description,
		DeliveryTime:         strconv.Itoa(catalog.DeliveryTime),
		CITypeID:             catalog.CiTypeID,
		CloudServiceID:       catalog.CloudServiceID,
		ProcessDefinitionKey: catalog.ProcessDefinitionKey,
		Status:               string(catalog.Status),
		CreatedAt:            catalog.CreatedAt,
		UpdatedAt:            catalog.UpdatedAt,
	}
}

// ToServiceRequestResponse 转换为服务请求响应
func ToServiceRequestResponse(request *ent.ServiceRequest) *ServiceRequestResponse {
	var expireAt *time.Time
	if !request.ExpireAt.IsZero() {
		t := request.ExpireAt
		expireAt = &t
	}
	resp := &ServiceRequestResponse{
		ID:                 request.ID,
		TicketID:           request.TicketID,
		CatalogID:          request.CatalogID,
		RequesterID:        request.RequesterID,
		CIID:               request.CiID,
		FormData:           request.FormData,
		CostCenter:         request.CostCenter,
		DataClassification: request.DataClassification,
		NeedsPublicIP:      request.NeedsPublicIP,
		SourceIPWhitelist:  request.SourceIPWhitelist,
		ExpireAt:           expireAt,
		ComplianceAck:      request.ComplianceAck,
		Version:            request.Version,
		CreatedAt:          request.CreatedAt,
		UpdatedAt:          request.UpdatedAt,
	}

	return resp
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
}
