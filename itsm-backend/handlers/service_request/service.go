package service_request

import (
	"context"
	"fmt"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/service"

	"go.uber.org/zap"
)

type Service struct {
	execution     *database.ExecutionPolicy
	directory     database.DirectorySnapshot
	repo          Repository
	client        *ent.Client
	logger        *zap.SugaredLogger
	chainResolver *service.ApprovalChainResolver
}

func NewService(repo Repository, client *ent.Client, logger *zap.SugaredLogger, chainResolver *service.ApprovalChainResolver, execution *database.ExecutionPolicy) *Service {
	return &Service{execution: execution, repo: repo, client: client, logger: logger, chainResolver: chainResolver}
}

// IsUnfinished follows Requested Item workflow completion and access cancellation
// rules. Catalog Tasks have no implemented lifecycle owner yet and fail closed.
func (s *Service) IsUnfinished(_ context.Context, _ *ent.Client, item *ent.Ticket) (bool, error) {
	if item == nil || item.RecordClass != "service_request_item" {
		//lint:ignore ST1005 Preserve the existing domain term in this public error message.
		return false, fmt.Errorf("Requested Item lifecycle requires service_request_item")
	}
	switch item.Status {
	case "new", "open", "assigned", "pending", "in_progress":
		return true, nil
	case "resolved", "closed", "cancelled", "rejected":
		return false, nil
	default:
		return false, fmt.Errorf("unsupported Requested Item status %q", item.Status)
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
		if appErr, ok := common.AsAppError(err); ok {
			return nil, appErr
		}
		s.logger.Errorw("Failed to update service request", "error", err)
		return nil, common.NewInternalError("Failed to update service request", err)
	}

	return s.repo.Get(ctx, id, tenantID)
}

// canManageServiceRequest 判断角色是否有 service_request:write 权限（按权限而非角色名判断）。
func (s *Service) canManageServiceRequest(ctx context.Context, role string, tenantID int) bool {
	return authorization.HasResourcePermission(s.client, role, "service_request", "write", tenantID)
}
