package service_request

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	entticket "itsm-backend/ent/ticket"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/ticket"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"go.uber.org/zap"
)

// TicketServiceInterface 是 Create 需要的最小 Ticket 创建能力，用接口而非具体类型
// 避免 handlers/service_request 直接依赖 service.TicketService 的完整实现。
type TicketServiceInterface interface {
	CreateTicket(ctx context.Context, req *dto.CreateTicketRequest, tenantID int) (*ticket.Ticket, error)
}

// catalogIntakeCreator is the minimal Unified Intake ApplicationService
// capability Create needs to divert Incident/Change-target Catalog
// submissions through the same CreateWorkItemCommand path every other Intake
// caller uses (Task 11's IncidentController, Task 12's BPMN callback).
type catalogIntakeCreator interface {
	Create(ctx context.Context, identity intake.Identity, command intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error)
}

type Service struct {
	repo            Repository
	scRepo          service_catalog.Repository
	cmdbRepo        cmdb.Repository
	client          *ent.Client
	numberAllocator workitemnumber.Allocator
	logger          *zap.SugaredLogger
	ticketSvc       TicketServiceInterface
	chainResolver   *service.ApprovalChainResolver
	intakeService   catalogIntakeCreator
}

func NewService(repo Repository, scRepo service_catalog.Repository, cmdbRepo cmdb.Repository, entClient *ent.Client, allocator workitemnumber.Allocator, logger *zap.SugaredLogger, ticketSvc TicketServiceInterface, chainResolver *service.ApprovalChainResolver, intakeService catalogIntakeCreator) *Service {
	if allocator == nil {
		panic("work item number allocator is required")
	}
	return &Service{
		repo:            repo,
		scRepo:          scRepo,
		cmdbRepo:        cmdbRepo,
		client:          entClient,
		numberAllocator: allocator,
		logger:          logger,
		ticketSvc:       ticketSvc,
		chainResolver:   chainResolver,
		intakeService:   intakeService,
	}
}

// Client exposes the underlying ent client so the handler layer can query
// side-channel data (e.g. custom field values) for detail responses without
// duplicating that dependency on Handler.
func (s *Service) Client() *ent.Client { return s.client }

// Create submits a new service request. Its WorkItem base record and the
// ServiceRequest extension are one aggregate and must commit together. BPMN is
// triggered only after that transaction commits.
func (s *Service) Create(ctx context.Context, tenantID, requesterID int, catalogID int, reqData *ServiceRequest, idempotencyKey string) (*ServiceRequest, error) {
	_, _, requesterRole, err := s.repo.GetUserContext(ctx, requesterID, tenantID)
	if err != nil {
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

	// 1b. Incident/Change 类型：直接通过 Unified Intake ApplicationService 创建事件/变更，
	// 跳过 SR 和审批流程。判断依据是 target_class（WorkItem 目标类）——见 entity.go
	// isIncidentCatalog 的注释。target_class 现在是 service_catalogs 表的 NOT NULL 权威列
	// （migration 024_service_catalog_target_class_authority 已经回填全部存量行并删除退役的
	// itsm_type 列），不存在"未回填"这种中间状态。
	if isIncidentCatalog(cat.TargetClass) || cat.TargetClass == service_catalog.TargetClassChangeRequest {
		if strings.TrimSpace(idempotencyKey) == "" {
			return nil, common.NewBadRequestError("Idempotency-Key header is required", nil)
		}
		return s.createFromCatalogViaIntake(ctx, tenantID, requesterID, requesterRole, cat, reqData, idempotencyKey)
	}

	// 2. Validate Request Data
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Request title is required", nil)
	}
	// 基础设施字段组（成本中心/数据分级/公网IP/IP白名单/资源过期时间/合规确认）只对
	// vm/network/database 三类目录项强制要求——这条判断只在 service_catalog.RequiresInfraFields
	// 一处实现，见该函数注释。custom/access/security/software/devops 等类型（如 Copilot
	// 采购申请）不再被要求填写这组跟业务无关的基础设施字段。
	if service_catalog.RequiresInfraFields(cat.ServiceType) {
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
		switch reqData.DataClassification {
		case "public", "internal", "confidential", "restricted":
		default:
			return nil, common.NewBadRequestError("Invalid data classification", nil)
		}
	}

	// 2a-2. 结构化字段值只提取一次，后面三处复用（必填校验 / 从 form_data 里去重 / 写入
	// field_values），避免同一份提取逻辑跑三遍导致行为漂移（见 stripStructuredFieldKeys 和
	// 下方第 5 步的注释）。
	fieldValues := extractServiceRequestFieldValues(reqData.FormData)

	// 2b. Validate required dynamic custom fields (server-side enforcement — the admin-configured
	// "required" flag on a service catalog's field definitions must not be trust-the-frontend-only).
	if s.client != nil {
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

	// 3. Build the WorkItem base record. The transaction below owns persistence of
	// both this Ticket and its ServiceRequest extension.
	ticketReq := &dto.CreateTicketRequest{
		Title:       title,
		Description: reqData.reason(),
		Priority:    "medium",
		Type:        mapTargetClassToTicketType(cat.TargetClass),
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
	newReq := &ServiceRequest{
		TenantID:           tenantID,
		CatalogID:          catalogID,
		RequesterID:        requesterID,
		ComplianceAck:      reqData.ComplianceAck,
		NeedsPublicIP:      reqData.NeedsPublicIP,
		DataClassification: reqData.DataClassification,
		// form_data 不再原样落库整份 FormData——已经通过 field_values 权威持久化的结构化
		// 字段键先被 stripStructuredFieldKeys 剔除，只留 _approval_chain 这类系统流程上下文
		// 和没有字段定义覆盖的自由内容，停止 §8.3 描述的 form_data/field_values 双写。
		FormData:          injectApprovalChain(stripStructuredFieldKeys(reqData.FormData, fieldValues), resolvedSteps),
		CostCenter:        reqData.CostCenter,
		SourceIPWhitelist: reqData.SourceIPWhitelist,
		ExpireAt:          reqData.ExpireAt,
		ContactName:       reqData.ContactName,
		ContactEmail:      reqData.ContactEmail,
		Quantity:          reqData.Quantity,
		ExpectedAt:        reqData.ExpectedAt,
	}

	if cat.CITypeID > 0 {
		ciID, err := s.ensureLinkedCI(ctx, tenantID, cat, reqData)
		if err != nil {
			return nil, err
		}
		newReq.CiID = ciID
	}

	// 4. Persist both records atomically. A Service Request may never leave a
	// classified WorkItem without its one required extension.
	createdTicket, created, err := s.createWorkItemAndExtension(ctx, tenantID, ticketReq, newReq)
	if err != nil {
		return nil, common.NewInternalError("Failed to create service request", err)
	}
	s.triggerWorkflowAfterServiceRequestCommit(createdTicket, tenantID, ticketReq.WorkflowDefinitionKey, ticketReq.ApprovalChain)

	// 5. Persist dynamic custom field values against the TICKET now, not the SR
	// (entity_type/entity_id 归属改成 ticket，这样工单详情页能像其他自定义字段一样直接展示)。
	// 复用 2a-2 提取的 fieldValues，不重新提取一遍。
	if s.client != nil && len(fieldValues) > 0 {
		if err := service.NewFieldValueService(s.client).CreateValues(ctx, tenantID, "service_catalog", catalogID, "ticket", createdTicket.ID, fieldValues); err != nil {
			s.logger.Warnw("Failed to persist service request custom field values", "error", err, "ticket_id", createdTicket.ID)
		}
	}

	return created, nil
}

func (s *Service) createWorkItemAndExtension(ctx context.Context, tenantID int, ticketReq *dto.CreateTicketRequest, req *ServiceRequest) (*ticket.Ticket, *ServiceRequest, error) {
	if s.client == nil {
		return nil, nil, fmt.Errorf("service request Ent client is required")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("start service request transaction: %w", err)
	}
	rollback := func(cause error) (*ticket.Ticket, *ServiceRequest, error) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return nil, nil, fmt.Errorf("%w (rollback also failed: %v)", cause, rollbackErr)
		}
		return nil, nil, cause
	}

	workItemCreator := ticket.NewTransactionalCreator(s.numberAllocator)
	workItem, err := workItemCreator.CreateInTransaction(ctx, tx.Client(), &ticket.CreateParams{
		Title:       ticketReq.Title,
		Description: ticketReq.Description,
		Type:        ticket.TypeServiceRequest,
		Priority:    ticket.Priority(ticketReq.Priority),
		RequesterID: ticketReq.RequesterID,
		Source:      ticketReq.Source,
	}, tenantID)
	if err != nil {
		return rollback(fmt.Errorf("create service request work item: %w", err))
	}

	req.TicketID = workItem.ID
	created, err := NewEntRepository(tx.Client()).Create(ctx, req)
	if err != nil {
		return rollback(fmt.Errorf("create service request extension: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit service request transaction: %w", err)
	}
	return workItem, created, nil
}

type ticketWorkflowStarter interface {
	TriggerWorkflowForExistingTicket(ctx context.Context, tkt *ticket.Ticket, tenantID int, workflowDefinitionKey string, approvalChain interface{}) error
}

func (s *Service) triggerWorkflowAfterServiceRequestCommit(tkt *ticket.Ticket, tenantID int, workflowDefinitionKey string, approvalChain interface{}) {
	starter, ok := s.ticketSvc.(ticketWorkflowStarter)
	if !ok {
		s.logger.Warnw("Ticket service does not support post-commit workflow triggering", "ticket_id", tkt.ID)
		return
	}
	go func() {
		workflowCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := starter.TriggerWorkflowForExistingTicket(workflowCtx, tkt, tenantID, workflowDefinitionKey, approvalChain); err != nil {
			s.logger.Warnw("Workflow trigger failed", "error", err, "ticket_id", tkt.ID)
		}
	}()
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
	return authorization.HasResourcePermission(s.client, role, "service_request", "write", tenantID)
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

// createFromCatalogViaIntake routes Catalog items whose target_class is
// incident or change_request through the Unified Intake ApplicationService,
// which resolves the concrete ProfessionalCreator (IncidentCreator /
// ChangeCreator) from cat.TargetClass itself (see resolver.go's
// catalog.TargetClass -> resolved.RecordClass assignment) -- this function
// does not need to branch on TargetClass again. service_request_item stays
// on the existing rich Create body below, untouched by this function.
//
// requesterRole is threaded in (beyond the brief's literal signature) because
// intake.Identity.Role is not optional downstream: Identity.ValidateCommand
// rejects an empty Role outright, and Resolver.Resolve separately gates on it
// for "intake:create" / "service_catalog:read" / "incident:write"|"change:write"
// permission checks (handlers/intake/identity.go, resolver.go). Every existing
// production Intake caller (IncidentController, the BPMN incidentTaskIntakeAdapter)
// already sources Role from the authenticated actor's context for the same
// reason -- omitting it here would make every Catalog-derived Incident/Change
// submission fail closed with 401 against the real intake.Service, even though
// this package's own tests (which use the recordingIntake fake, not the real
// Resolver) would not catch that.
func (s *Service) createFromCatalogViaIntake(ctx context.Context, tenantID, requesterID int, requesterRole string, cat *service_catalog.ServiceCatalog, reqData *ServiceRequest, idempotencyKey string) (*ServiceRequest, error) {
	title := strings.TrimSpace(reqData.title())
	if title == "" {
		return nil, common.NewBadRequestError("Request title is required", nil)
	}
	description := reqData.reason()
	if description == "" {
		description = title
	}
	if s.intakeService == nil {
		return nil, common.NewInternalError("intake service not configured", nil)
	}
	formValues, err := s.catalogFormValues(ctx, tenantID, cat.ID, reqData.FormData)
	if err != nil {
		return nil, err
	}
	identity := intake.Identity{TenantID: tenantID, ActorID: requesterID, RequesterID: requesterID, Role: requesterRole, Channel: "service_catalog"}
	// ChangeInput is intentionally left nil: reqData.FormData (the generic Catalog
	// dynamic-field bag) has no confirmed mapping to ChangeInput's
	// Justification/ImpactScope/RiskLevel/etc today, the same way the Incident
	// branch never populated Severity/Impact/Urgency from the Catalog form
	// either -- ChangeCreator.Prepare's existing defaultString(..., "normal"/"medium")
	// fallback applies, exactly as it does for a nil Command.Change. Recorded,
	// deliberate simplification, not a gap left open to future judgment.
	command := intake.CreateWorkItemCommand{
		IdempotencyKey: idempotencyKey,
		IntakeKind:     intake.IntakeKindCatalogItem,
		CatalogItemID:  &cat.ID,
		Title:          title,
		Description:    description,
		FormValues:     formValues,
	}
	result, err := s.intakeService.Create(ctx, identity, command)
	if err != nil {
		return nil, mapIntakeErrorToAppError(err)
	}
	// Stub, non-persisted ServiceRequest carrying the professional reference ID
	// in the borrowed ID field -- the same response contract
	// createIncidentFromCatalog used, now extended to cover Change too.
	return &ServiceRequest{
		ID: result.ProfessionalReference.ID, TenantID: tenantID, CatalogID: cat.ID, RequesterID: requesterID,
		TicketID: result.WorkItemID, IntakeRecordClass: result.RecordClass,
	}, nil
}

// catalogFormValues extracts the submitted custom-field values from formData
// (via the same extractServiceRequestFieldValues helper the generic path uses
// at 2a-2 above) and filters them down to only the keys this catalog item
// actually defines as field_definitions (entity_type="service_catalog",
// entity_id=catalogID). This filtering is required, not optional:
// handlers/intake's Resolver.resolveForm hard-rejects any FormValues key that
// isn't one of the catalog's defined fields -- unlike RecordClassServiceRequestItem,
// Incident/Change have no "professional field" allowlist
// (isServiceRequestProfessionalField) to fall back on, so passing the raw form
// bag through unfiltered would trade "required fields unreachable" (FormValues
// always nil, every required field 400s) for "any extra ambient form key
// always rejects" (e.g. a stray customFieldValues wrapper key). Reuses the
// same ListDefinitions(..., "service_catalog", catalogID) lookup the generic
// path's own required-field check (2b above) already performs, rather than
// inventing a second way to load field definitions.
//
// A definitions-lookup failure fails the whole Create (matching 2b's own
// common.NewInternalError precedent) instead of silently degrading to no
// FormValues -- degrading silently would either falsely reject every required
// field (if the omission makes them look absent) or silently drop values the
// requester actually submitted (if there happen to be no required fields),
// and both are worse than a clear, retryable internal error.
func (s *Service) catalogFormValues(ctx context.Context, tenantID, catalogID int, formData map[string]interface{}) (map[string]any, error) {
	if s.client == nil {
		return nil, nil
	}
	submitted := extractServiceRequestFieldValues(formData)
	if len(submitted) == 0 {
		return nil, nil
	}
	defs, err := service.NewFieldDefinitionService(s.client).ListDefinitions(ctx, tenantID, "service_catalog", catalogID)
	if err != nil {
		return nil, common.NewInternalError("Failed to load service catalog fields", err)
	}
	if len(defs) == 0 {
		return nil, nil
	}
	filtered := make(map[string]any, len(defs))
	for _, def := range defs {
		if v, ok := submitted[def.Name]; ok {
			filtered[def.Name] = v
		}
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	return filtered, nil
}

// mapIntakeErrorToAppError translates an *intake.IntakeError into this
// package's existing common.AppError convention so failServiceRequest (in
// handler.go) needs no separate branch for Intake-originated errors -- one
// error-response path, not two.
func mapIntakeErrorToAppError(err error) error {
	var appErr *intake.IntakeError
	if !errors.As(err, &appErr) {
		return common.NewInternalError("创建失败", err)
	}
	switch appErr.Code {
	case intake.AuthenticationRequired:
		return common.NewUnauthorizedError(appErr.Message)
	case intake.PermissionDenied:
		return common.NewForbiddenError(appErr.Message)
	case intake.ReferenceNotFound:
		return common.NewNotFoundError(appErr.Message)
	case intake.IdempotencyConflict:
		return common.NewConflictError("request", appErr.Message)
	case intake.InvalidCommand, intake.DomainValidationFailed, intake.UnsupportedRecordClass, intake.WorkflowBindingRequired:
		return common.NewBadRequestError(appErr.Message, nil)
	default:
		return common.NewInternalError("创建失败", appErr)
	}
}
