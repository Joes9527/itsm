package change

import (
	"errors"
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"

	"time"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	creationApplication creation.Application
	svc                 *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func resolveChangeTenantID(c *gin.Context) (int, bool) {
	tenantID, err := middleware.ResolveRequestTenantID(c)
	if middleware.AbortIfTenantError(c, err) {
		return 0, false
	}
	return tenantID, true
}

func optionalChangeDate(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	return value
}

// Map domain to DTO
func toDTO(c *Change) *dto.ChangeResponse {
	if c == nil {
		return nil
	}
	res := &dto.ChangeResponse{
		Number:  c.Number,
		ID:      c.ID,
		Version: c.Version, Outcome: c.Outcome, OutcomeEvidence: c.OutcomeEvidence, ReviewEvidence: c.ReviewEvidence, ReviewedBy: c.ReviewedBy, StandardTemplateID: c.StandardTemplateID,
		Title:              c.Title,
		Description:        c.Description,
		Justification:      c.Justification,
		Type:               dto.ChangeType(c.Type),
		Status:             dto.ChangeStatus(c.Status),
		Priority:           dto.ChangePriority(c.Priority),
		ImpactScope:        dto.ChangeImpact(c.ImpactScope),
		RiskLevel:          dto.ChangeRisk(c.RiskLevel),
		AssigneeID:         c.AssigneeID,
		CreatedBy:          c.CreatedBy,
		TenantID:           c.TenantID,
		PlannedStartDate:   optionalChangeDate(c.PlannedStartDate),
		PlannedEndDate:     optionalChangeDate(c.PlannedEndDate),
		ActualStartDate:    optionalChangeDate(c.ActualStartDate),
		ActualEndDate:      optionalChangeDate(c.ActualEndDate),
		ImplementationPlan: c.ImplementationPlan,
		RollbackPlan:       c.RollbackPlan,
		AffectedCIs:        c.AffectedCIs,
		RelatedTickets:     c.RelatedTickets,
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
		WorkItemID:         c.WorkItemID,
	}
	if !c.ReviewedAt.IsZero() {
		res.ReviewedAt = &c.ReviewedAt
	}
	if c.Assignee != nil {
		res.AssigneeName = &c.Assignee.Name
	}
	if c.CreatedByUser != nil {
		res.CreatedByName = c.CreatedByUser.Name
	}
	return res
}

// CreateChange handles POST /api/v1/changes
func (h *Handler) SetCreationApplication(app creation.Application) { h.creationApplication = app }
func (h *Handler) CreateChange(c *gin.Context) {
	var req dto.CreateChangeRequest
	if !intakehttp.Bind(c, &req) {
		return
	}
	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}
	start, end := "", ""
	if req.PlannedStartDate != nil {
		start = req.PlannedStartDate.UTC().Format(time.RFC3339Nano)
	}
	if req.PlannedEndDate != nil {
		end = req.PlannedEndDate.UTC().Format(time.RFC3339Nano)
	}
	requesterID := 0
	if req.RequesterID != nil {
		requesterID = *req.RequesterID
	}
	intakehttp.Execute(c, h.creationApplication, tenantID, requesterID, creation.CreateWorkItemCommand{RecordClass: creation.RecordClassChangeRequest, IntakeKind: creation.IntakeKindChangeRequest, Title: req.Title, Description: req.Description, Priority: req.Priority, Change: &creation.ChangeInput{Justification: req.Justification, Type: req.Type, ImpactScope: req.ImpactScope, RiskLevel: req.RiskLevel, PlannedStartDate: start, PlannedEndDate: end, ImplementationPlan: req.ImplementationPlan, RollbackPlan: req.RollbackPlan, AffectedCIs: req.AffectedCIs, RelatedTicketNumbers: req.RelatedTickets}})
}

// GetChange handles GET /api/v1/changes/:id
func (h *Handler) GetChange(c *gin.Context) {
	id, meta, ok := changeHTTPIdentity(c)
	if !ok {
		return
	}
	result, actions, tasks, err := h.svc.GetChangeActionView(c.Request.Context(), id, meta)
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}
	response := toDTO(result)
	response.Actions = actions
	response.CurrentTasks = tasks
	common.Success(c, response)
}

// GetRiskAssessment handles GET /api/v1/changes/:id/risk-assessment
func (h *Handler) GetRiskAssessment(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}
	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	ra, err := h.svc.GetRisk(c.Request.Context(), id, tenantID)
	if err != nil {
		common.InternalError(c, "获取风险评估失败: "+err.Error())
		return
	}

	if ra == nil {
		common.Success(c, nil)
		return
	}

	common.Success(c, dto.ChangeRiskAssessment{
		ID:                 ra.ID,
		ChangeID:           ra.ChangeID,
		RiskLevel:          dto.ChangeRisk(ra.RiskLevel),
		RiskDescription:    ra.RiskDescription,
		ImpactAnalysis:     ra.ImpactAnalysis,
		MitigationMeasures: ra.MitigationMeasures,
		ContingencyPlan:    ra.ContingencyPlan,
		RiskOwner:          ra.RiskOwner,
		RiskReviewDate:     ra.RiskReviewDate,
		CreatedAt:          ra.CreatedAt,
		UpdatedAt:          ra.UpdatedAt,
	})
}

// GetCMDBImpactSummary handles GET /api/v1/changes/:id/cmdb-impact
func (h *Handler) GetCMDBImpactSummary(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	summary, err := h.svc.GetCMDBImpactSummary(c.Request.Context(), id, tenantID)
	if err != nil {
		common.InternalError(c, "获取CMDB影响摘要失败: "+err.Error())
		return
	}

	common.Success(c, summary)
}

// ListChanges handles GET /api/v1/changes
func (h *Handler) ListChanges(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	status := c.Query("status")
	search := c.Query("search")
	// 支持 risk_level 与 riskLevel 两种命名，前端一般发 camelCase
	riskLevel := c.Query("risk_level")
	if riskLevel == "" {
		riskLevel = c.Query("riskLevel")
	}
	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	list, total, err := h.svc.ListChanges(c.Request.Context(), tenantID, page, pageSize, status, search, riskLevel)
	if err != nil {
		common.InternalError(c, "查询变更列表失败: "+err.Error())
		return
	}

	var dtos []dto.ChangeResponse
	for _, item := range list {
		dtos = append(dtos, *toDTO(item))
	}

	common.Success(c, gin.H{
		"changes":  dtos,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// GetStats handles GET /api/v1/changes/stats
func (h *Handler) GetStats(c *gin.Context) {
	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}
	res, err := h.svc.GetStats(c.Request.Context(), tenantID)
	if err != nil {
		common.InternalError(c, "获取统计信息失败: "+err.Error())
		return
	}
	// Map domain stats -> DTO so the response shape stays governed by dto.ChangeStatsResponse
	// (Project rule: Controller must return DTO, never the domain struct directly.)
	common.Success(c, toStatsDTO(res))
}

// toStatsDTO maps the change.Stats domain struct to dto.ChangeStatsResponse.
func toStatsDTO(s *Stats) *dto.ChangeStatsResponse {
	if s == nil {
		return &dto.ChangeStatsResponse{}
	}
	return &dto.ChangeStatsResponse{
		Draft: s.Draft, SuccessfulOutcomes: s.SuccessfulOutcomes, FailedOutcomes: s.FailedOutcomes, RolledBackOutcomes: s.RolledBackOutcomes,
		Total:      s.Total,
		Pending:    s.Pending,
		Approved:   s.Approved,
		Scheduled:  s.Scheduled,
		InProgress: s.InProgress,
		Completed:  s.Completed,
		Failed:     s.Failed,
		RolledBack: s.RolledBack,
		Rejected:   s.Rejected,
		Cancelled:  s.Cancelled,
	}
}

// GetApprovals handles GET /api/v1/changes/:id/approvals
func (h *Handler) GetApprovals(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}
	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}
	history, err := h.svc.GetApprovalHistory(c.Request.Context(), id, tenantID)
	if err != nil {
		common.InternalError(c, "获取审批历史失败: "+err.Error())
		return
	}
	common.Success(c, history)
}

// DeleteChange handles DELETE /api/v1/changes/:id
func (h *Handler) DeleteChange(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}
	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	if err := h.svc.DeleteChange(c.Request.Context(), id, tenantID); err != nil {
		common.InternalError(c, "删除变更失败: "+err.Error())
		return
	}
	common.Success(c, gin.H{"message": "deleted"})
}

// GetCalendar handles GET /api/v1/changes/calendar
func (h *Handler) GetCalendar(c *gin.Context) {
	var req dto.ChangeCalendarRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ParamError(c, "Invalid query parameters: "+err.Error())
		return
	}

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	res, err := h.svc.GetCalendarView(c.Request.Context(), tenantID, req.StartDate, req.EndDate, req.Status)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, res)
}

// ==================== PIR (Post-Implementation Review) Handlers ====================

// CreatePIR handles POST /api/v1/changes/:id/pir
func (h *Handler) CreatePIR(c *gin.Context) {
	changeID, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}
	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(int)

	var req dto.CreateChangePIRRequest
	if !bindChangeMutation(c, &req) {
		return
	}
	if req.ChangeID != 0 && req.ChangeID != changeID {
		common.ParamError(c, "changeId must match route")
		return
	}
	req.ChangeID = changeID

	pir, err := h.svc.CreatePIR(c.Request.Context(), &req, workitemmutation.Meta{ActorID: userID, TenantID: tenantID, ExpectedVersion: req.ExpectedVersion, OperationID: req.OperationID, Source: "http"})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}

	common.Success(c, pir)
}

// GetPIR handles GET /api/v1/changes/:id/pir
func (h *Handler) GetPIR(c *gin.Context) {
	changeID, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	pir, err := h.svc.GetPIRByChange(c.Request.Context(), changeID, tenantID)
	if err != nil {
		if strings.Contains(err.Error(), "无PIR记录") {
			common.NotFound(c, err.Error())
			return
		}
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, pir)
}

// ListPIRs handles GET /api/v1/changes/pirs
func (h *Handler) ListPIRs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	result := c.Query("result")

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	pirs, err := h.svc.ListPIRs(c.Request.Context(), tenantID, page, pageSize, result)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, pirs)
}

// UpdatePIR handles PUT /api/v1/changes/pir/:id
func (h *Handler) UpdatePIR(c *gin.Context) {
	pirID, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	var req dto.UpdateChangePIRRequest
	if !bindChangeMutation(c, &req) {
		return
	}

	pir, err := h.svc.UpdatePIR(c.Request.Context(), pirID, &req, workitemmutation.Meta{ActorID: c.GetInt("user_id"), TenantID: tenantID, ExpectedVersion: req.ExpectedVersion, OperationID: req.OperationID, Source: "http"})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}

	common.Success(c, pir)
}

// DeletePIR handles DELETE /api/v1/changes/pir/:id
func (h *Handler) DeletePIR(c *gin.Context) {
	pirID, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantID, ok := resolveChangeTenantID(c)
	if !ok {
		return
	}

	var req dto.DeleteChangePIRRequest
	if !bindChangeMutation(c, &req) {
		return
	}
	result, err := h.svc.DeletePIR(c.Request.Context(), pirID, &req, workitemmutation.Meta{ActorID: c.GetInt("user_id"), TenantID: tenantID, ExpectedVersion: req.ExpectedVersion, OperationID: req.OperationID, Source: "http"})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}

	common.Success(c, result)
}

func respondPIRMutationError(c *gin.Context, err error) {
	var intake *creation.IntakeError
	if errors.As(err, &intake) {
		switch intake.HTTPStatus {
		case 400:
			common.ParamError(c, intake.Message)
		case 401:
			common.AuthFailed(c, intake.Message)
		case 403:
			common.Forbidden(c, intake.Message)
		case 404:
			common.NotFound(c, intake.Message)
		case 409:
			common.Conflict(c, intake.Message, nil)
		default:
			common.InternalError(c, "Change mutation failed")
		}
		return
	}

	var conflict *workitemmutation.OperationConflictError
	var state interface{ SQLState() string }
	if common.IsVersionConflictError(err) || errors.As(err, &conflict) || (errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01")) {
		common.Conflict(c, "Change mutation conflicts with current state", nil)
		return
	}
	if app, ok := common.AsAppError(err); ok {
		switch app.Code {
		case common.ErrCodeValidation, common.ErrCodeBadRequest:
			common.ParamError(c, app.Message)
		case common.ErrCodeForbidden:
			common.Forbidden(c, app.Message)
		case common.ErrCodeNotFound:
			common.NotFound(c, app.Message)
		case common.ErrCodeConflict:
			common.Conflict(c, app.Message, nil)
		default:
			common.InternalError(c, "Change mutation failed")
		}
		return
	}
	common.InternalError(c, "Change mutation failed")
}
