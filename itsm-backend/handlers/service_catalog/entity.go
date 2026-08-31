package service_catalog

import (
	"context"
	"time"

	"itsm-backend/service"
)

// ServiceCatalog represents the core domain entity
type ServiceCatalog struct {
	ID          int
	Name        string
	Category    string
	Description string
	// ITSMType 历史字段：Request|Incident|Change。Wave 2（ServiceRequest 层级规范化）之后
	// 不再是路由权威——TargetClass 才是。保留只是因为 ent schema 列还在、且是 TargetClass
	// 的计算输入（见 repository_impl.go 调用的 ComputeTargetClass），不代表两个字段并存做路由
	// 决策：所有实际路由判断（handlers/service_request 的 isIncidentCatalog/
	// mapTargetClassToTicketType）已经改读 TargetClass。
	ITSMType string
	// TargetClass 是 WorkItem 目标类：service_request_item|incident|change_request，
	// 在 repository_impl.go 的 Create/Update 里从 ITSMType 同步计算落库（design doc §7.2）。
	TargetClass    string
	ServiceType    string // vm|rds|network|database|storage|oss|security|access|custom 等，决定是否需要基础设施字段（见 RequiresInfraFields）
	DeliveryTime   int
	CITypeID       int
	CloudServiceID int
	// ProcessDefinitionKey 是该目录条目专属的 BPMN 流程定义 Key（可选）。非空时优先于
	// businessType+businessSubType 的通用流程绑定解析，见 ticket_service.go
	// triggerWorkflowForTicket 的 workflowDefinitionKey 参数。
	ProcessDefinitionKey string
	Status               string
	TenantID             int
	Fields               []service.FieldDefinitionInput
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Version              string
}

// Repository defines the interface for data persistence
type Repository interface {
	Create(ctx context.Context, catalog *ServiceCatalog) (*ServiceCatalog, error)
	Get(ctx context.Context, tenantID int, id int) (*ServiceCatalog, error)
	List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceCatalog, int, error)
	Search(ctx context.Context, tenantID int, keyword string, filters ListFilters) ([]*ServiceCatalog, int, error)
	Update(ctx context.Context, tenantID int, catalog *ServiceCatalog) (*ServiceCatalog, error)
	Delete(ctx context.Context, tenantID int, id int) error
	Count(ctx context.Context, tenantID int, filters ListFilters) (int, error)
	CountByCategory(ctx context.Context, tenantID int) (map[string]int, error)
	NameExists(ctx context.Context, tenantID int, name string, excludeID int) (bool, error)
	ValidateReferences(ctx context.Context, tenantID, ciTypeID, cloudServiceID int) error
}

// ListFilters defines available filters for listing catalogs
type ListFilters struct {
	Category string
	Status   string
	Page     int
	Size     int
}

// ServiceStats holds statistics for service catalog
type ServiceStats struct {
	TotalServices     int            `json:"totalServices"`
	PublishedServices int            `json:"publishedServices"`
	Categories        map[string]int `json:"categories"`
}

// RequiresInfraFields 判断该服务类型是否需要基础设施类字段（成本中心/数据分级/
// 需要公网IP/来源IP白名单/资源过期时间/合规确认）。这条业务规则只在这一处实现——
// 前端只读取 ServiceCatalogResponse.RequiresInfraFields，不自行判断 service_type，
// 后端 Create 校验也调用这同一个函数，避免两处各写一份导致漂移。
//
// 取值对齐 ent/schema/servicecatalog.go 的 service_type 字段注释：
// vm|rds|oss|network|storage|security|custom。其中 "database"/"rds" 与 "storage"/"oss"
// 分别是同一类资源在不同代码路径/种子数据里使用的不同命名，都算需要基础设施字段。
// "security"（安全扫描类）故意不包含在内：它是一次性动作，不是需要成本中心/过期时间的
// 被置备资源，这是设计该集合时的既有产品决策，不在这里重新讨论。
func RequiresInfraFields(serviceType string) bool {
	switch serviceType {
	case "vm", "rds", "network", "database", "storage", "oss":
		return true
	default:
		return false
	}
}

// WorkItem target_class 取值（design doc §7.2、§4 术语表 record_class 词表的子集）。
const (
	TargetClassServiceRequestItem = "service_request_item"
	TargetClassIncident           = "incident"
	TargetClassChangeRequest      = "change_request"
)

// ComputeTargetClass 把历史 itsm_type（Request|Incident|Change）映射为受约束的
// target_class。这是 target_class 的唯一计算入口——repository_impl.go 的 Create/Update
// 在写入前调用它同步落库，cmd/backfill_servicecatalog_target_class 对存量行做一次性回填时
// 复用同一份映射，避免两处各写一份导致漂移。空值/未知值一律 fail-safe 落到
// service_request_item（对齐 ent schema itsm_type 字段的 Default("Request")）。
func ComputeTargetClass(itsmType string) string {
	switch itsmType {
	case "Incident":
		return TargetClassIncident
	case "Change":
		return TargetClassChangeRequest
	default:
		return TargetClassServiceRequestItem
	}
}
