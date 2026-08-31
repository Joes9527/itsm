package service_request

import (
	"context"

	"itsm-backend/common"
	"itsm-backend/ent"
	entticket "itsm-backend/ent/ticket"
	"itsm-backend/middleware"

	"go.uber.org/zap"
)

type Service struct {
	repo   Repository
	client *ent.Client
	logger *zap.SugaredLogger
}

func NewService(repo Repository, entClient *ent.Client, logger *zap.SugaredLogger) *Service {
	return &Service{
		repo: repo, client: entClient, logger: logger,
	}
}

// Client exposes the underlying ent client so the handler layer can query
// side-channel data (e.g. custom field values) for detail responses without
// duplicating that dependency on Handler.
func (s *Service) Client() *ent.Client { return s.client }

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
