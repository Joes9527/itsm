package service_request

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	entticket "itsm-backend/ent/ticket"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/middleware"
	"itsm-backend/repository/ticket"
	"itsm-backend/service"

	"go.uber.org/zap"
)

// TicketServiceInterface 是 Create 需要的最小 Ticket 创建能力，用接口而非具体类型
// 避免 handlers/service_request 直接依赖 service.TicketService 的完整实现。
type TicketServiceInterface interface {
	CreateTicket(ctx context.Context, req *dto.CreateTicketRequest, tenantID int) (*ticket.Ticket, error)
}

// IncidentCreator 是 Create 在 ITSM 类型为 Incident 时所需的最小事件创建能力。
type IncidentCreator interface {
	CreateIncident(ctx context.Context, tenantID, requesterID int, title, description string, catalogID int) (incidentID int, err error)
}

type Service struct {
	repo          Repository
	scRepo        service_catalog.Repository
	cmdbRepo      cmdb.Repository
	client        *ent.Client
	logger        *zap.SugaredLogger
	ticketSvc     TicketServiceInterface
	chainResolver *service.ApprovalChainResolver
	incidentSvc   IncidentCreator
}

func NewService(repo Repository, scRepo service_catalog.Repository, cmdbRepo cmdb.Repository, entClient *ent.Client, logger *zap.SugaredLogger, ticketSvc TicketServiceInterface, chainResolver *service.ApprovalChainResolver, incidentSvc IncidentCreator) *Service {
	return &Service{
		repo:          repo,
		scRepo:        scRepo,
		cmdbRepo:      cmdbRepo,
		client:        entClient,
		logger:        logger,
		ticketSvc:     ticketSvc,
		chainResolver: chainResolver,
		incidentSvc:   incidentSvc,
	}
}

// Client exposes the underlying ent client so the handler layer can query
// side-channel data (e.g. custom field values) for detail responses without
// duplicating that dependency on Handler.
func (s *Service) Client() *ent.Client { return s.client }

// Create submits a new service request. 先创建关联 Ticket（承担状态/审批/工作流），
// 再创建瘦身后的 ServiceRequest 行——两步顺序执行，不在同一数据库事务里（TicketService.CreateTicket
// 是自包含的高层编排方法，不暴露外部事务句柄）。若第二步失败，Ticket 已经存在但缺少关联的
// SR 扩展数据——这与 CreateTicket 自己写 field_values 的失败处理是同一种"创建后写卫星数据，
// 失败只警告不回滚主记录"模式，不是新发明的容错策略。
func (s *Service) Create(ctx context.Context, tenantID, requesterID int, catalogID int, reqData *ServiceRequest) (*ServiceRequest, error) {
	if _, _, err := s.repo.GetUserContext(ctx, requesterID, tenantID); err != nil {
		return nil, common.NewBadRequestError("Requester not found or inactive", err)
	}
	// 1. Validate Service Catalog
	cat, err := s.scRepo.Get(ctx, tenantID, catalogID)
	if err != nil {
		return nil, common.NewNotFoundError("Service Catalog not found")
	}
	if cat.CloudServiceID > 0 && cat.CITypeID == 0 {
		return nil, common.NewBadRequestError("关联云服务时必须配置CI类型", nil)
	}
	if cat.Status != "enabled" && cat.Status != "active" {
		return nil, common.NewBadRequestError("Service Catalog is not enabled", nil)
	}

	// 1b. Incident 类型：直接创建事件，跳过 SR 和审批流程。
	if isIncidentCatalog(cat.ITSMType) {
		return s.createIncidentFromCatalog(ctx, tenantID, requesterID, catalogID, reqData)
	}

	// 2. Validate Request Data
	if !reqData.ComplianceAck {
		return nil, common.NewBadRequestError("Compliance acknowledgement required", nil)
	}
	if reqData.NeedsPublicIP && len(reqData.SourceIPWhitelist) == 0 {
		return nil, common.NewBadRequestError("Source IP whitelist required for public IP", nil)
	}
	if reqData.ExpireAt == nil {
		return nil, common.NewBadRequestError("Expiration date required", nil)
	}
	if !reqData.ExpireAt.After(time.Now()) {
		return nil, common.NewBadRequestError("Expiration date must be in the future", nil)
	}
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Request title is required", nil)
	}
	switch reqData.DataClassification {
	case "public", "internal", "confidential", "restricted":
	default:
		return nil, common.NewBadRequestError("Invalid data classification", nil)
	}

	// 2b. Validate required dynamic custom fields (server-side enforcement — the admin-configured
	// "required" flag on a service catalog's field definitions must not be trust-the-frontend-only).
	if s.client != nil {
		fieldValues := extractServiceRequestFieldValues(reqData.FormData)
		defs, err := service.NewFieldDefinitionService(s.client).ListDefinitions(ctx, tenantID, "service_catalog", catalogID)
		if err != nil {
			return nil, common.NewInternalError("Failed to load service catalog fields", err)
		}
		for _, def := range defs {
			if !def.Required {
				continue
			}
			val, ok := fieldValues[def.Name]
			if !ok || val == nil || val == "" {
				return nil, common.NewBadRequestError(fmt.Sprintf("字段「%s」为必填项", def.Label), nil)
			}
		}
	}

	// 2c. 解析审批链（阶段二）：根据服务目录项属性和请求金额，从 ApprovalChain
	// (entity_type=service_request) 中解析出实际生效的审批步骤。解析结果存入
	// ServiceRequest.FormData._approval_chain 供 BPMN 流程 / 前端引用。
	// 若租户未配置 service_request 类型的审批链，解析结果为 nil——不影响创建流程。
	var resolvedSteps interface{}
	if s.chainResolver != nil {
		chain, resolveErr := s.chainResolver.ResolveForServiceRequest(ctx, tenantID, reqData.amount(), "", 0)
		if resolveErr != nil {
			s.logger.Warnw("Failed to resolve approval chain for service request", "error", resolveErr,
				"tenant_id", tenantID, "catalog_id", catalogID)
		} else if chain != nil && len(chain.Steps) > 0 {
			resolvedSteps = chain.Steps
		}
	}

	// 3. 先创建关联 Ticket——source="service_catalog" 标记来源，
	// description 用申请原因（reqData.reason() 从 FormData 兜底取，逻辑同下方 title()）。
	ticketReq := &dto.CreateTicketRequest{
		Title:       title,
		Description: reqData.reason(),
		Priority:    "medium",
		Type:        mapITSMType(cat.ITSMType),
		RequesterID: requesterID,
		Source:      "service_catalog",
	}
	if hasApprovalChainSteps(resolvedSteps) {
		ticketReq.ApprovalChain = resolvedSteps
	}
	// 目录条目配置了专属流程时优先生效，跳过 businessType+businessSubType 的通用绑定解析
	// （见 ticket_service.go triggerWorkflowForTicket 里 workflowDefinitionKey 的优先级）。
	if strings.TrimSpace(cat.ProcessDefinitionKey) != "" {
		ticketReq.WorkflowDefinitionKey = strings.TrimSpace(cat.ProcessDefinitionKey)
	}
	createdTicket, err := s.ticketSvc.CreateTicket(ctx, ticketReq, tenantID)
	if err != nil {
		return nil, common.NewInternalError("Failed to create linked ticket", err)
	}

	newReq := &ServiceRequest{
		TenantID:           tenantID,
		TicketID:           createdTicket.ID,
		CatalogID:          catalogID,
		RequesterID:        requesterID,
		ComplianceAck:      reqData.ComplianceAck,
		NeedsPublicIP:      reqData.NeedsPublicIP,
		DataClassification: reqData.DataClassification,
		FormData:           injectApprovalChain(reqData.FormData, resolvedSteps),
		CostCenter:         reqData.CostCenter,
		SourceIPWhitelist:  reqData.SourceIPWhitelist,
		ExpireAt:           reqData.ExpireAt,
	}

	if cat.CITypeID > 0 {
		ciID, err := s.ensureLinkedCI(ctx, tenantID, cat, reqData)
		if err != nil {
			return nil, err
		}
		newReq.CiID = ciID
	}

	// 4. Save
	created, err := s.repo.Create(ctx, newReq)
	if err != nil {
		s.logger.Errorw("Failed to create service request (linked ticket already created)", "error", err, "ticket_id", createdTicket.ID)
		return nil, common.NewInternalError("Failed to create service request", err)
	}

	// 5. Persist dynamic custom field values against the TICKET now, not the SR
	// (entity_type/entity_id 归属改成 ticket，这样工单详情页能像其他自定义字段一样直接展示)。
	if s.client != nil {
		if fieldValues := extractServiceRequestFieldValues(reqData.FormData); len(fieldValues) > 0 {
			if err := service.NewFieldValueService(s.client).CreateValues(ctx, tenantID, "service_catalog", catalogID, "ticket", createdTicket.ID, fieldValues); err != nil {
				s.logger.Warnw("Failed to persist service request custom field values", "error", err, "ticket_id", createdTicket.ID)
			}
		}
	}

	return created, nil
}

// title/reason 这两个展示字段现在只是"创建 ticket 时的初始值"，不再持久化在 SR 表上——
// 从 FormData 兜底读，和 handler.go normalizeCreateServiceRequest 已经做的事保持一致。
func (r *ServiceRequest) title() string {
	if v, ok := r.FormData["title"].(string); ok {
		return v
	}
	return ""
}
func (r *ServiceRequest) reason() string {
	if v, ok := r.FormData["reason"].(string); ok {
		return v
	}
	return ""
}

func (s *Service) ensureLinkedCI(ctx context.Context, tenantID int, cat *service_catalog.ServiceCatalog, reqData *ServiceRequest) (int, error) {
	_ = cat
	cloudResourceRefID := parseIntField(reqData.FormData, "cloud_resource_ref_id")
	if cloudResourceRefID > 0 {
		existing, err := s.cmdbRepo.GetCIByCloudResourceRefID(ctx, tenantID, cloudResourceRefID)
		if err == nil && existing != nil {
			return existing.ID, nil
		}
		if err != nil && !ent.IsNotFound(err) {
			return 0, common.NewInternalError("查询关联CI失败", err)
		}
	}
	// 新 CI 必须在审批完成后的 provisioning 阶段由履约器/连接器创建，
	// 提交申请时不能提前向 CMDB 写入 active 资产。
	return 0, nil
}

func parseIntField(formData map[string]interface{}, key string) int {
	if formData == nil {
		return 0
	}
	switch v := formData[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return 0
		}
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return 0
}

func (s *Service) Get(ctx context.Context, id, tenantID int) (*ServiceRequest, error) {
	return s.repo.Get(ctx, id, tenantID)
}

// GetByTicketID 供 ticket 详情页查询关联的 SR 扩展数据（Task 2 前端用）。
// 找不到时返回的 error 用 ent.IsNotFound 判断——不是每个 ticket 都有关联 SR，
// 调用方（ticket handler）要能区分"这不是服务目录来源的 ticket"和真正的查询失败。
func (s *Service) GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error) {
	return s.repo.GetByTicketID(ctx, ticketID, tenantID)
}

// List 返回服务请求列表，并批量回填每条记录关联 ticket 的 title/status（展示用，
// 非持久化字段——见 entity.go 上 TicketTitle/TicketStatus 的注释）。批量查一次
// `WHERE id IN (...)`，不是逐条查，避免 N+1（同 FieldDefinitionService.ListDefinitionsForEntities
// 的批量加载先例）。批量回填失败只记警告、不让整个列表请求失败——title/status 是展示增强，
// 不是列表能否返回的必要条件。
func (s *Service) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceRequest, int, error) {
	list, total, err := s.repo.List(ctx, tenantID, filters)
	if err != nil {
		return nil, 0, err
	}
	s.attachTicketSummaries(ctx, tenantID, list)
	return list, total, nil
}

// attachTicketSummaries 批量查询 list 里每条记录关联的 Ticket，把 title/status 写回
// 对应记录的 TicketTitle/TicketStatus 字段。
func (s *Service) attachTicketSummaries(ctx context.Context, tenantID int, list []*ServiceRequest) {
	if s.client == nil || len(list) == 0 {
		return
	}
	ticketIDs := make([]int, 0, len(list))
	for _, r := range list {
		if r.TicketID > 0 {
			ticketIDs = append(ticketIDs, r.TicketID)
		}
	}
	if len(ticketIDs) == 0 {
		return
	}

	tickets, err := s.client.Ticket.Query().
		Where(entticket.IDIn(ticketIDs...), entticket.TenantID(tenantID)).
		All(ctx)
	if err != nil {
		s.logger.Warnw("Failed to batch-load linked tickets for service request list", "error", err)
		return
	}

	byID := make(map[int]*ent.Ticket, len(tickets))
	for _, t := range tickets {
		byID[t.ID] = t
	}
	for _, r := range list {
		if t, ok := byID[r.TicketID]; ok {
			r.TicketTitle = t.Title
			r.TicketStatus = t.Status
		}
	}
}

// Update updates a service request
func (s *Service) Update(ctx context.Context, id, tenantID, actorID int, actorRole string, reqData *ServiceRequest) (*ServiceRequest, error) {
	// 1. Get existing request
	req, err := s.repo.Get(ctx, id, tenantID)
	if err != nil {
		return nil, common.NewNotFoundError("Service Request not found")
	}
	if actorID != req.RequesterID && !s.canManageServiceRequest(ctx, actorRole, tenantID) {
		return nil, common.NewForbiddenError("Only the requester or an administrator can edit this request")
	}

	// 2. Update fields
	if reqData.FormData != nil {
		req.FormData = reqData.FormData
	}
	if reqData.CostCenter != "" {
		req.CostCenter = reqData.CostCenter
	}
	if reqData.DataClassification != "" {
		req.DataClassification = reqData.DataClassification
	}
	if reqData.NeedsPublicIPSet {
		req.NeedsPublicIP = reqData.NeedsPublicIP
	}
	if reqData.SourceIPWhitelist != nil {
		req.SourceIPWhitelist = reqData.SourceIPWhitelist
	}
	if reqData.ExpireAt != nil {
		req.ExpireAt = reqData.ExpireAt
	}
	if reqData.ComplianceAckSet {
		req.ComplianceAck = reqData.ComplianceAck
	}

	// 3. Save
	if err := s.repo.Update(ctx, req); err != nil {
		s.logger.Errorw("Failed to update service request", "error", err)
		return nil, common.NewInternalError("Failed to update service request", err)
	}

	return req, nil
}

// Delete deletes a service request
func (s *Service) Delete(ctx context.Context, id, tenantID, actorID int, actorRole string) error {
	// 1. Get existing request
	req, err := s.repo.Get(ctx, id, tenantID)
	if err != nil {
		return common.NewNotFoundError("Service Request not found")
	}
	if actorID != req.RequesterID && !s.canManageServiceRequest(ctx, actorRole, tenantID) {
		return common.NewForbiddenError("Only the requester or an administrator can delete this request")
	}

	// 2. Delete
	if err := s.repo.Delete(ctx, req); err != nil {
		s.logger.Errorw("Failed to delete service request", "error", err)
		return common.NewInternalError("Failed to delete service request", err)
	}

	return nil
}

// canManageServiceRequest 判断角色是否有 service_request:write 权限（按权限而非角色名判断）。
func (s *Service) canManageServiceRequest(ctx context.Context, role string, tenantID int) bool {
	return middleware.HasResourcePermission(s.client, role, "service_request", "write", tenantID)
}

// serviceRequestSystemFormDataKeys 是 handler.go normalizeCreateServiceRequest 已经从
// FormData 摘出、写进 ServiceRequest 专用列的系统已知键。这些键即使恰好跟某个字段定义
// 同名，也不应该被当成动态自定义字段再收编一次进 field_values。
var serviceRequestSystemFormDataKeys = map[string]bool{
	"title": true, "reason": true, "cost_center": true,
	"data_classification": true, "source_ip_whitelist": true,
	"expire_at": true, "compliance_ack": true,
}

// parseServiceRequestFieldValuesArray 把 formData["customFieldValues"] 解析成 [{name,value}] 数组形状，
// 转成内部用的 map[name]value。数组形状是必须的——字段名作为数组元素里的字符串值
// （而不是对象的 key）传输，这样才能绕开前端 http-client.ts 那个全局的、不区分
// 契约字段和用户数据的 snake_case→camelCase 请求体转换（那个转换会把 map 形状里
// 带下划线的字段名 key 悄悄改写，导致匹配失败、值静默丢失）。
// 解析不出数组形状返回 nil，调用方会退回到兼容 map 形状的旧逻辑。
func parseServiceRequestFieldValuesArray(formData map[string]interface{}) map[string]interface{} {
	if formData == nil {
		return nil
	}
	rawValues, ok := formData["customFieldValues"].([]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]interface{}, len(rawValues))
	for _, raw := range rawValues {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		if val, ok := entry["value"]; ok {
			result[name] = val
		}
	}
	return result
}

func extractServiceRequestFieldValues(formData map[string]interface{}) map[string]interface{} {
	if formData == nil {
		return nil
	}
	if values := parseServiceRequestFieldValuesArray(formData); values != nil {
		return values
	}
	// 兼容旧的 map 形状
	result := make(map[string]interface{}, len(formData))
	for k, v := range formData {
		if serviceRequestSystemFormDataKeys[k] {
			continue
		}
		result[k] = v
	}
	return result
}

// createIncidentFromCatalog 为 ITSM 类型为 Incident 的服务目录项直接创建事件，
// 跳过 ServiceRequest 和审批流程。返回的 "stub" ServiceRequest 仅携带 IncidentID 供
// handler 层返回给前端，不做持久化。
func (s *Service) createIncidentFromCatalog(ctx context.Context, tenantID, requesterID, catalogID int, reqData *ServiceRequest) (*ServiceRequest, error) {
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Incident title is required", nil)
	}
	description := reqData.reason()
	if description == "" {
		description = title
	}

	incidentID, err := s.incidentSvc.CreateIncident(ctx, tenantID, requesterID, title, description, catalogID)
	if err != nil {
		return nil, common.NewInternalError("Failed to create incident from service catalog", err)
	}

	// 返回一个非持久化的 stub ServiceRequest，仅供 handler 构建响应用。
	return &ServiceRequest{
		ID:          incidentID, // 借用 ID 字段传递 incidentID
		TenantID:    tenantID,
		CatalogID:   catalogID,
		RequesterID: requesterID,
	}, nil
}
