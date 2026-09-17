package service_request

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	creationApplication creation.Application
	service             *Service
}

func failServiceRequest(c *gin.Context, err error) {
	var intake *creation.IntakeError
	if errors.As(err, &intake) {
		intakehttp.Fail(c, err)
		return
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01") {
		common.Conflict(c, "Service request mutation conflicts with current state", nil)
		return
	}

	if appErr, ok := common.AsAppError(err); ok {
		switch appErr.Code {
		case common.ErrCodeBadRequest, common.ErrCodeValidation:
			common.Fail(c, common.ParamErrorCode, appErr.Message)
		case common.ErrCodeUnauthorized:
			common.Fail(c, common.UnauthorizedCode, appErr.Message)
		case common.ErrCodeForbidden:
			common.Fail(c, common.ForbiddenErrorCode, appErr.Message)
		case common.ErrCodeNotFound:
			common.Fail(c, common.NotFoundErrorCode, appErr.Message)
		case common.ErrCodeConflict:
			common.Fail(c, common.ConflictCode, appErr.Error())
		default:
			common.Fail(c, common.InternalErrorCode, appErr.Message)
		}
		return
	}
	if ent.IsNotFound(err) {
		common.Fail(c, common.NotFoundErrorCode, "Service request not found")
		return
	}
	common.Fail(c, common.InternalErrorCode, "Service request operation failed")
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Map Domain to DTO
func (h *Handler) toDTO(req *ServiceRequest) *dto.ServiceRequestResponse {
	if req == nil {
		return nil
	}
	resp := &dto.ServiceRequestResponse{
		ID:                 req.ID,
		TicketID:           req.TicketID,
		CatalogID:          req.CatalogID,
		RequesterID:        req.RequesterID,
		CIID:               req.CiID,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		DataClassification: req.DataClassification,
		NeedsPublicIP:      req.NeedsPublicIP,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ComplianceAck:      req.ComplianceAck,
		ContactName:        req.ContactName,
		ContactEmail:       req.ContactEmail,
		Quantity:           req.Quantity,
		Version:            req.Version,
		ProcessorID:        req.ProcessorID,
		StartedAt:          req.StartedAt,
		CompletedAt:        req.CompletedAt,
		CompletionNote:     req.CompletionNote,
		LastError:          req.LastError,
		CreatedAt:          req.CreatedAt,
		UpdatedAt:          req.UpdatedAt,
		TicketTitle:        req.TicketTitle,
		TicketStatus:       req.TicketStatus,
	}
	if req.ExpireAt != nil {
		t := *req.ExpireAt
		resp.ExpireAt = &t
	}
	if req.ExpectedAt != nil {
		t := *req.ExpectedAt
		resp.ExpectedAt = &t
	}
	return resp
}

// toDTOWithCustomFields wraps toDTO and additionally fills in CustomFields
// from the field_values snapshot, plus actions.provision（能否发起交付，见
// service.CanProvision——同一个函数既用于这里的展示，也用于 provision 接口本身的强制校验）。
// Used by detail-style responses (Get, Create's success branch) — List intentionally
// does not call this to avoid N+1 queries, mirroring ToTicketResponse vs
// ToTicketResponseWithCustomFields.
func (h *Handler) toDTOWithCustomFields(ctx context.Context, req *ServiceRequest, client *ent.Client, actorUserID int, actorRole string) *dto.ServiceRequestResponse {
	resp := h.toDTO(req)
	if client == nil {
		return resp
	}
	resp.Actions = map[string]dto.ActionPermission{
		"provision": service.CanProvision(client, req.TenantID, actorUserID, actorRole, req.RequesterID),
	}
	if err := h.service.ValidateManualProvisioning(ctx, client, req.TenantID, req.TicketID); err != nil {
		resp.Actions["provision"] = dto.ActionPermission{Allowed: false, Reason: "此申请需通过审批流程履约"}
		resp.FulfillmentState = "unknown"
		item, readErr := client.Ticket.Query().Where(ticket.IDEQ(req.TicketID), ticket.TenantIDEQ(req.TenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil()).Only(ctx)
		if readErr == nil {
			fulfillment, projectionErr := h.service.ReadFulfillment(ctx, client, item)
			if projectionErr == nil {
				resp.FulfillmentState, resp.AccessResult = fulfillment.State, fulfillment.AccessResult
			} else {
				h.service.logger.Warnw("Service request fulfillment unavailable", "ticket_id", req.TicketID)
			}
		} else {
			h.service.logger.Warnw("Service request WorkItem unavailable", "ticket_id", req.TicketID)
		}
	}
	values, err := service.NewFieldValueService(client).ListValues(ctx, req.TenantID, "ticket", req.TicketID)
	if err != nil {
		h.service.logger.Warnw("Failed to load service request custom fields", "tenant_id", req.TenantID, "ticket_id", req.TicketID, "error", err)
		return resp
	}
	if len(values) == 0 {
		return resp
	}
	resp.CustomFields = make([]dto.CustomFieldValueResponse, 0, len(values))
	for _, v := range values {
		resp.CustomFields = append(resp.CustomFields, dto.CustomFieldValueResponse{Name: v.Name, Label: v.Label, Value: v.Value})
	}
	return resp
}

func (h *Handler) SetCreationApplication(app creation.Application) { h.creationApplication = app }
func (h *Handler) Create(c *gin.Context) {
	var req dto.CreateServiceRequestRequest
	if !intakehttp.Bind(c, &req) {
		return
	}
	tenantID, err := middleware.ResolveRequestTenantID(c)
	if middleware.AbortIfTenantError(c, err) {
		return
	}
	command, err := catalogCreationCommand(req, func(name string) bool { return intakehttp.FieldPresent(c, name) })
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	intakehttp.Execute(c, h.creationApplication, tenantID, req.RequesterID, command)
}

func (h *Handler) Get(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.Fail(c, 1001, "Invalid ID")
		return
	}
	tenantID := c.GetInt("tenant_id")

	req, err := h.service.Get(c.Request.Context(), id, tenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			common.Fail(c, 404, "Not Found")
		} else if common.IsAppError(err) {
			common.Fail(c, 5001, err.Error())
		} else {
			common.Fail(c, 5001, err.Error())
		}
		return
	}
	common.Success(c, h.toDTOWithCustomFields(c.Request.Context(), req, h.service.Client(), c.GetInt("user_id"), c.GetString("role")))
}

// GetByTicket 供 ticket 详情页渲染关联的服务请求扩展面板。
func (h *Handler) GetByTicket(c *gin.Context) {
	ticketIDStr := c.Param("ticketId")
	ticketID, err := strconv.Atoi(ticketIDStr)
	if err != nil {
		common.Fail(c, 1001, "Invalid ticket ID")
		return
	}
	tenantID := c.GetInt("tenant_id")

	req, err := h.service.GetByTicketID(c.Request.Context(), ticketID, tenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			common.Fail(c, 404, "No service request linked to this ticket")
		} else {
			common.Fail(c, 5001, err.Error())
		}
		return
	}
	common.Success(c, h.toDTOWithCustomFields(c.Request.Context(), req, h.service.Client(), c.GetInt("user_id"), c.GetString("role")))
}

func (h *Handler) List(c *gin.Context) {
	var req dto.GetServiceRequestsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.Fail(c, 1001, "Invalid parameters")
		return
	}
	tenantID := c.GetInt("tenant_id")

	// If listing "me", we need user ID
	userID := 0
	if c.Request.URL.Path == "/me" || c.Query("scope") == "me" {
		userID = c.GetInt("user_id")
	}
	// For compatibility with legacy controller which injects UserID from token into DTO if needed
	if req.UserID == 0 && (c.Request.URL.Path == "/api/v1/service-requests/me" || strings.Contains(c.Request.URL.Path, "/me")) {
		uid := c.GetInt("user_id")
		userID = uid
	}

	filters := ListFilters{
		UserID: userID,
		Page:   req.Page,
		Size:   req.Size,
	}
	if filters.Page == 0 {
		filters.Page = 1
	}
	if filters.Size == 0 {
		filters.Size = 10
	}

	list, total, err := h.service.List(c.Request.Context(), tenantID, filters)
	if err != nil {
		common.Fail(c, 5001, err.Error())
		return
	}

	dtos := make([]dto.ServiceRequestResponse, len(list))
	for i, v := range list {
		dtos[i] = *h.toDTO(v)
	}

	common.Success(c, map[string]interface{}{
		"requests": dtos,
		"total":    total,
		"page":     filters.Page,
		"size":     filters.Size,
	})
}

func (h *Handler) Update(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.Fail(c, 1001, "Invalid ID")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, 2001, "Tenant ID missing")
		return
	}

	var req dto.UpdateServiceRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 1001, "Invalid parameters: "+err.Error())
		return
	}
	normalizeUpdateServiceRequest(&req)

	domainReq := &ServiceRequest{
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		DataClassification: req.DataClassification,
		NeedsPublicIPSet:   req.NeedsPublicIP != nil,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ComplianceAckSet:   req.ComplianceAck != nil,
		ExpireAt:           req.ExpireAt,
	}
	if req.NeedsPublicIP != nil {
		domainReq.NeedsPublicIP = *req.NeedsPublicIP
	}
	if req.ComplianceAck != nil {
		domainReq.ComplianceAck = *req.ComplianceAck
	}

	userID := c.GetInt("user_id")
	role := c.GetString("role")
	updated, err := h.service.Update(c.Request.Context(), id, tenantID, userID, role, domainReq)
	if err != nil {
		failServiceRequest(c, err)
		return
	}

	fullReq, _ := h.service.Get(c.Request.Context(), updated.ID, tenantID)
	common.Success(c, h.toDTO(fullReq))
}

// CorrectClassification 处理申请项的分类纠正（PUT /service-requests/:id/classification）。
//
// 权限：路由要求 service_request:write，服务层再要求管理侧身份（申请人不能自助改分类）。
// 目标必须是完整三级且不允许清空，原因必填，证据与写入同事务。
func (h *Handler) CorrectClassification(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, 1001, "Invalid ID")
		return
	}
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, 2001, "Tenant ID missing")
		return
	}
	var req dto.CorrectServiceRequestClassificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 1001, "请求参数无效")
		return
	}
	updated, err := h.service.CorrectClassification(c.Request.Context(), ClassificationCorrection{
		Meta: workitemmutation.Meta{
			TenantID:        tenantID,
			ActorID:         c.GetInt("user_id"),
			Source:          "http",
			ExpectedVersion: req.Version,
			CorrelationID:   c.GetHeader("X-Correlation-ID"),
		},
		ServiceRequestID: id,
		TargetCategoryID: req.CategoryID,
		Reason:           req.Reason,
		ActorRole:        c.GetString("role"),
	})
	if err != nil {
		failServiceRequest(c, err)
		return
	}
	common.Success(c, map[string]any{"id": id, "categoryId": updated.CategoryID, "version": updated.Version})
}

func (h *Handler) Delete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.Fail(c, 1001, "Invalid ID")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, 2001, "Tenant ID missing")
		return
	}

	err = h.service.Delete(c.Request.Context(), id, workitemmutation.Meta{TenantID: tenantID, ActorID: c.GetInt("user_id"), Source: "http"})
	if err != nil {
		failServiceRequest(c, err)
		return
	}

	common.Success(c, nil)
}

func normalizeUpdateServiceRequest(req *dto.UpdateServiceRequestRequest) {
}
