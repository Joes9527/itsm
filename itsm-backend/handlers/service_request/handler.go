package service_request

import (
	"context"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func failServiceRequest(c *gin.Context, err error) {
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
	common.Fail(c, common.InternalErrorCode, err.Error())
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
// from the field_values snapshot. Used by detail-style responses (Get,
// Create's success branch) — List intentionally does not call this to avoid
// N+1 queries, mirroring ToTicketResponse vs ToTicketResponseWithCustomFields.
func (h *Handler) toDTOWithCustomFields(req *ServiceRequest, client *ent.Client) *dto.ServiceRequestResponse {
	resp := h.toDTO(req)
	if client == nil {
		return resp
	}
	values, err := service.NewFieldValueService(client).ListValues(context.Background(), req.TenantID, "ticket", req.TicketID)
	if err != nil {
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

func (h *Handler) Create(c *gin.Context) {
	var req dto.CreateServiceRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 1001, "Invalid parameters: "+err.Error())
		return
	}
	normalizeCreateServiceRequest(&req)
	if req.CatalogID == 0 {
		common.Fail(c, 1001, "catalogId is required")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, 2001, "Tenant ID missing")
		return
	}
	userID := c.GetInt("user_id")
	if userID == 0 {
		common.Fail(c, 2001, "User ID missing")
		return
	}

	expireAt := req.ExpireAt

	domainReq := &ServiceRequest{
		ComplianceAck:      req.ComplianceAck,
		NeedsPublicIP:      req.NeedsPublicIP,
		DataClassification: req.DataClassification,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ExpireAt:           expireAt,
		ContactName:        req.ContactName,
		ContactEmail:       req.ContactEmail,
		Quantity:           req.Quantity,
		ExpectedAt:         req.ExpectedAt,
	}
	if domainReq.FormData == nil {
		domainReq.FormData = map[string]interface{}{}
	}
	domainReq.FormData["title"] = req.Title
	domainReq.FormData["reason"] = req.Reason

	created, err := h.service.Create(c.Request.Context(), tenantID, userID, req.CatalogID, domainReq)
	if err != nil {
		failServiceRequest(c, err)
		return
	}

	fullReq, err := h.service.Get(c.Request.Context(), created.ID, tenantID)
	if err != nil {
		h.service.logger.Errorw("Create: failed to get created service request", "error", err, "id", created.ID)
		// Return the created object even if Get fails - created.ID is valid
		common.Success(c, h.toDTO(created))
		return
	}
	common.Success(c, h.toDTOWithCustomFields(fullReq, h.service.Client()))
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
	common.Success(c, h.toDTOWithCustomFields(req, h.service.Client()))
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
	common.Success(c, h.toDTOWithCustomFields(req, h.service.Client()))
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

	err = h.service.Delete(c.Request.Context(), id, tenantID, c.GetInt("user_id"), c.GetString("role"))
	if err != nil {
		failServiceRequest(c, err)
		return
	}

	common.Success(c, nil)
}

func normalizeCreateServiceRequest(req *dto.CreateServiceRequestRequest) {
	if req.FormData == nil {
		req.FormData = map[string]any{}
	}
	if req.Title == "" {
		if title, ok := req.FormData["title"].(string); ok {
			req.Title = title
		}
	}
	if req.Reason == "" {
		if reason, ok := req.FormData["reason"].(string); ok {
			req.Reason = reason
		}
	}
	if req.CostCenter == "" {
		if costCenter, ok := req.FormData["cost_center"].(string); ok {
			req.CostCenter = costCenter
		}
	}
	if req.DataClassification == "" {
		if classification, ok := req.FormData["data_classification"].(string); ok {
			req.DataClassification = classification
		}
	}
	if req.DataClassification == "" {
		req.DataClassification = "internal"
	}
	if len(req.SourceIPWhitelist) == 0 {
		if whitelist, ok := req.FormData["source_ip_whitelist"].([]string); ok {
			req.SourceIPWhitelist = whitelist
		}
	}
	if req.ExpireAt == nil {
		if expireAt, ok := req.FormData["expire_at"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, expireAt); err == nil {
				req.ExpireAt = &parsed
			}
		}
	}
	if req.ExpireAt == nil {
		defaultExpireAt := time.Now().Add(30 * 24 * time.Hour)
		req.ExpireAt = &defaultExpireAt
	}
	if ack, ok := req.FormData["compliance_ack"].(bool); ok {
		req.ComplianceAck = ack
	}
}

func normalizeUpdateServiceRequest(req *dto.UpdateServiceRequestRequest) {
}
