package service_request

import (
	"context"
	"strconv"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/repository/ticket"
	"itsm-backend/service"

	"go.uber.org/zap"
)

type Service struct {
	repo          Repository
	client        *ent.Client
	logger        *zap.SugaredLogger
	chainResolver *service.ApprovalChainResolver
}

func NewService(repo Repository, client *ent.Client, logger *zap.SugaredLogger, chainResolver *service.ApprovalChainResolver) *Service {
	return &Service{repo: repo, client: client, logger: logger, chainResolver: chainResolver}
}

// Client exposes the underlying ent client so the handler layer can query
// side-channel data (e.g. custom field values) for detail responses without
// duplicating that dependency on Handler.
func (s *Service) Client() *ent.Client { return s.client }

type ticketWorkflowStarter interface {
	TriggerWorkflowForExistingTicket(ctx context.Context, tkt *ticket.Ticket, tenantID int, workflowDefinitionKey string, approvalChain interface{}) error
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

// List loads complete aggregates through the repository's required WorkItem edge.
func (s *Service) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceRequest, int, error) {
	return s.repo.List(ctx, tenantID, filters)
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

	return s.repo.Get(ctx, id, tenantID)
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
