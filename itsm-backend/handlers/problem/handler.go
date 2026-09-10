package problem

import (
	"errors"
	"strconv"
	"strings"

	"itsm-backend/common"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/dto"
	"itsm-backend/ent"
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
	client              *ent.Client
}

func NewHandler(service *Service, client *ent.Client) *Handler {
	return &Handler{service: service, client: client}
}

func resolveProblemTenantID(c *gin.Context) (int, bool) {
	tenantID, err := middleware.ResolveRequestTenantID(c)
	if middleware.AbortIfTenantError(c, err) {
		return 0, false
	}
	return tenantID, true
}

// ToResponse maps the already-authorized projection, preserving omitted mutation relations.
func ToResponse(p *Problem) *dto.ProblemResponse {
	if p == nil {
		return nil
	}

	resp := dto.ProblemResponse{
		Number:           p.Number,
		Relations:        relationProjection(p.Relations),
		Version:          p.Version,
		VerifiedVersion:  p.VerifiedVersion,
		VerificationNote: p.VerificationNote,
		ID:               p.ID,
		Title:            p.Title,
		Description:      p.Description,
		Status:           p.Status,
		Priority:         p.Priority,
		Category:         p.Category,
		CategoryID:       categoryValue(p.CategoryID),
		RootCause:        p.RootCause,
		Workaround:       p.Workaround,
		Resolution:       p.Resolution,
		Impact:           p.Impact,
		CreatedBy:        p.CreatedBy,
		TenantID:         p.TenantID,
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
		WorkItemID:       p.WorkItemID,
	}
	if p.AssigneeID != nil {
		resp.AssigneeID = p.AssigneeID
	}

	return &resp
}

func (h *Handler) SetCreationApplication(app creation.Application) { h.creationApplication = app }

// Create API contract.
// @Summary Create
// @Description Creation returns immutable intake receipt; replay is HTTP 200.
// @Tags problems
// @Accept json
// @Produce json
// @Param body body dto.CreateProblemRequest true "Request"
// @Success 201 {object} common.Response{data=creation.CreateWorkItemResult}
// @Success 200 {object} common.Response{data=creation.CreateWorkItemResult} "Replay"
// @Router /api/v1/problems [post]
func (h *Handler) Create(c *gin.Context) {
	var req dto.CreateProblemRequest
	if !intakehttp.Bind(c, &req) {
		return
	}
	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}
	if req.ImpactScope != "" {
		intakehttp.Fail(c, intakehttp.Invalid("impactScope", "impactScope is unsupported; use impact"))
		return
	}
	requesterID := 0
	if req.RequesterID != nil {
		requesterID = *req.RequesterID
	}
	intakehttp.Execute(c, h.creationApplication, tenantID, requesterID, creation.CreateWorkItemCommand{RecordClass: creation.RecordClassProblem, IntakeKind: creation.IntakeKindProblem, Title: req.Title, Description: req.Description, Priority: req.Priority, CTI: req.CTI, Problem: &creation.ProblemInput{RootCause: req.RootCause, Impact: req.Impact}})
}

// Get API contract.
// @Summary Get
// @Description Current actor RR read; relations is an authoritative array, including empty array.
// @Tags problems
// @Accept json
// @Produce json
// @Param id path int true "Professional extension ID"
// @Success 200 {object} common.Response{data=dto.ProblemResponse}
// @Router /api/v1/problems/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "invalid id")
		return
	}

	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}

	actor, ok := h.problemActionActor(c, tenantID)
	if !ok {
		return
	}

	p, err := h.service.Get(c.Request.Context(), id, workitemmutation.Meta{TenantID: actor.TenantID, ActorID: actor.UserID, Source: "http"})
	if err != nil {
		RespondCommandError(c, err)
		return
	}
	response := ToResponse(p)
	response.Actions = BuildProblemActions(actor, p)
	common.Success(c, response)
}

// problemActorUserID 从请求上下文取出当前操作人 ID，用于 WorkItemRelation.created_by_id。
func problemActorUserID(c *gin.Context) (int, bool) {
	v, exists := c.Get("user_id")
	if !exists {
		common.Fail(c, common.AuthErrorCode, "invalid user context")
		return 0, false
	}
	userID, ok := v.(int)
	if !ok || userID <= 0 {
		common.Fail(c, common.AuthErrorCode, "invalid user context")
		return 0, false
	}
	return userID, true
}

func (h *Handler) problemActionActor(c *gin.Context, tenantID int) (service.ActionActor, bool) {
	userValue, userExists := c.Get("user_id")
	userID, userOK := userValue.(int)
	role := strings.TrimSpace(c.GetString("role"))

	if tenantID <= 0 || !userExists || !userOK || userID <= 0 || role == "" {
		common.Fail(c, common.AuthErrorCode, "invalid action actor context")
		return service.ActionActor{}, false
	}

	return service.ActionActor{
		Client:   h.client,
		TenantID: tenantID,
		UserID:   userID,
		Role:     role,
	}, true
}

// List API contract.
// @Summary List
// @Description Current read scope applies before pagination and count; each relation endpoint is authorized.
// @Tags problems
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=dto.ListProblemsResponse}
// @Router /api/v1/problems [get]
func (h *Handler) List(c *gin.Context) {
	var req dto.ListProblemsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}

	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}

	// Convert DTO filters to map
	filters := make(map[string]interface{})
	if req.Status != "" {
		filters["status"] = req.Status
	}
	if req.Priority != "" {
		filters["priority"] = req.Priority
	}
	if req.Category != "" {
		filters["category"] = req.Category
	}
	if req.Keyword != "" {
		filters["keyword"] = req.Keyword
	}

	list, total, err := h.service.List(c.Request.Context(), workitemmutation.Meta{TenantID: tenantID, ActorID: c.GetInt("user_id"), Source: "http"}, req.Page, req.PageSize, filters)
	if err != nil {
		RespondCommandError(c, err)
		return
	}

	// Map to DTO response
	dtoProblems := make([]*dto.ProblemResponse, 0, len(list))
	for _, p := range list {
		item := ToResponse(p)
		dtoProblems = append(dtoProblems, item)
	}

	page, pageSize := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	common.Success(c, &dto.ListProblemsResponse{
		Problems:   dtoProblems,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: (total + pageSize - 1) / pageSize,
	})
}

// Update API contract.
// @Summary Update
// @Description Committed metadata response omits relations. Refresh separately; denied refresh does not undo the committed update.
// @Tags problems
// @Accept json
// @Produce json
// @Param id path int true "Professional extension ID"
// @Param body body dto.UpdateProblemRequest true "Request"
// @Success 200 {object} common.Response{data=dto.ProblemResponse}
// @Router /api/v1/problems/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "invalid id")
		return
	}

	var req dto.UpdateProblemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}

	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}

	// 将 DTO 指针字段转换为 domain entity
	updates := &Problem{Version: req.Version}
	if req.Title != nil {
		updates.Title = *req.Title
	}
	if req.Description != nil {
		updates.Description = *req.Description
	}
	if req.Status != nil {
		updates.Status = *req.Status
	}
	if req.Priority != nil {
		updates.Priority = *req.Priority
	}
	updates.CategoryID = req.CategoryID
	if req.RootCause != nil {
		updates.RootCause = *req.RootCause
	}
	if req.Impact != nil {
		updates.Impact = *req.Impact
	}

	updated, err := h.service.Update(c.Request.Context(), tenantID, id, updates)
	if err != nil {
		if _, ok := common.AsAppError(err); ok || common.IsVersionConflictError(err) {
			RespondCommandError(c, err)
			return
		}
		common.Fail(c, common.InternalErrorCode, err.Error())
		return
	}

	common.Success(c, ToResponse(updated))
}

func (h *Handler) InvestigateProblem(c *gin.Context) { h.command(c, "investigate") }
func (h *Handler) ResolveProblem(c *gin.Context)     { h.command(c, "resolve") }
func (h *Handler) VerifyResolution(c *gin.Context)   { h.command(c, "verify_resolution") }
func (h *Handler) ReopenProblem(c *gin.Context)      { h.command(c, "reopen") }
func (h *Handler) SelectResolution(c *gin.Context)   { h.command(c, "select_resolution") }
func (h *Handler) command(c *gin.Context, action string) {
	id, tenantID, ok := problemRequestContext(c)
	if !ok {
		return
	}
	var req struct {
		Version          int    `json:"version" binding:"required,gt=0"`
		OperationID      string `json:"operationId" binding:"required,max=200"`
		Reason           string `json:"reason"`
		VerificationNote string `json:"verificationNote"`
		SolutionID       int    `json:"solutionId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}
	actorID, ok := problemActorUserID(c)
	if !ok {
		return
	}
	result, err := h.service.ApplyCommand(c.Request.Context(), Command{Meta: workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, ExpectedVersion: req.Version, OperationID: req.OperationID, Source: "http", CorrelationID: c.GetString("request_id")}, ProblemID: id, Action: action, Reason: req.Reason, VerificationNote: req.VerificationNote, SolutionID: req.SolutionID})
	if err != nil {
		RespondCommandError(c, err)
		return
	}
	common.Success(c, result)
}

func RespondCommandError(c *gin.Context, err error) {
	var intake *creation.IntakeError
	if errors.As(err, &intake) {
		intakehttp.Fail(c, err)
		return
	}
	var conflict *workitemmutation.OperationConflictError
	var state interface{ SQLState() string }
	if common.IsVersionConflictError(err) || errors.As(err, &conflict) || (errors.As(err, &state) && (state.SQLState() == "40001" || state.SQLState() == "40P01")) {
		common.Conflict(c, "Problem mutation conflicts with current state", nil)
		return
	}
	if ent.IsNotFound(err) {
		common.NotFound(c, "Problem not found")
		return
	}
	if app, ok := common.AsAppError(err); ok {
		switch app.Code {
		case common.ErrCodeValidation, common.ErrCodeBadRequest:
			common.ParamError(c, app.Message)
		case common.ErrCodeUnauthorized:
			common.Fail(c, common.AuthFailedCode, app.Message)
		case common.ErrCodeForbidden:
			common.Forbidden(c, app.Message)
		case common.ErrCodeNotFound:
			common.NotFound(c, app.Message)
		case common.ErrCodeConflict:
			common.Conflict(c, app.Message, nil)
		default:
			common.InternalError(c, "Problem mutation failed")
		}
		return
	}
	common.InternalError(c, "Problem mutation failed")
}

// UpdateRootCause API contract.
// @Summary UpdateRootCause
// @Description Committed metadata response omits relations; refresh separately.
// @Tags problems
// @Accept json
// @Produce json
// @Param id path int true "Professional extension ID"
// @Param body body dto.UpdateProblemRootCauseRequest true "Request"
// @Success 200 {object} common.Response{data=dto.ProblemResponse}
// @Router /api/v1/problems/{id}/root-cause [put]
func (h *Handler) UpdateRootCause(c *gin.Context) {
	id, tenantID, ok := problemRequestContext(c)
	if !ok {
		return
	}
	var req dto.UpdateProblemRootCauseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}
	updated, err := h.service.UpdateRootCause(c.Request.Context(), tenantID, id, req.Version, req.RootCause)
	h.respondProblemMutation(c, updated, err)
}

// UpdateSolution API contract.
// @Summary UpdateSolution
// @Description Committed metadata response omits relations; refresh separately.
// @Tags problems
// @Accept json
// @Produce json
// @Param id path int true "Professional extension ID"
// @Param body body dto.UpdateProblemResolutionRequest true "Request"
// @Success 200 {object} common.Response{data=dto.ProblemResponse}
// @Router /api/v1/problems/{id}/solution [put]
func (h *Handler) UpdateSolution(c *gin.Context) {
	id, tenantID, ok := problemRequestContext(c)
	if !ok {
		return
	}
	var req dto.UpdateProblemResolutionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}
	resolution := req.Resolution
	if resolution == "" {
		resolution = req.Solution
	}
	updated, err := h.service.UpdateSolution(c.Request.Context(), tenantID, id, req.Version, req.Workaround, resolution)
	h.respondProblemMutation(c, updated, err)
}

func (h *Handler) CloseProblem(c *gin.Context) { h.command(c, "close") }

func problemRequestContext(c *gin.Context) (int, int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.Fail(c, common.ParamErrorCode, "invalid id")
		return 0, 0, false
	}
	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return 0, 0, false
	}
	return id, tenantID, true
}

func (h *Handler) respondProblemMutation(c *gin.Context, updated *Problem, err error) {
	if err != nil {
		if _, ok := common.AsAppError(err); ok || common.IsVersionConflictError(err) {
			RespondCommandError(c, err)
			return
		}
		if ent.IsNotFound(err) {
			common.Fail(c, common.NotFoundErrorCode, "Problem not found")
		} else if strings.Contains(err.Error(), "required") {
			common.Fail(c, common.ParamErrorCode, err.Error())
		} else {
			common.Fail(c, common.InternalErrorCode, err.Error())
		}
		return
	}
	common.Success(c, ToResponse(updated))
}

func (h *Handler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "invalid id")
		return
	}

	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}
	actorID, ok := problemActorUserID(c)
	if !ok {
		return
	}
	err = h.service.Delete(c.Request.Context(), id, workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, Source: "http"})
	if err != nil {
		RespondCommandError(c, err)
		return
	}

	common.Success(c, nil)
}

func (h *Handler) GetStats(c *gin.Context) {
	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}
	stats, err := h.service.GetStats(c.Request.Context(), tenantID)
	if err != nil {
		common.Fail(c, common.InternalErrorCode, err.Error())
		return
	}

	// Map domain stats to DTO
	resp := &dto.ProblemStatsResponse{
		Total:        stats.Total,
		Open:         stats.Open,
		InProgress:   stats.InProgress,
		Resolved:     stats.Resolved,
		Closed:       stats.Closed,
		HighPriority: stats.HighPriority,
	}
	common.Success(c, resp)
}

func categoryValue(id *int) int {
	if id == nil {
		return 0
	}
	return *id
}

// Nil means no authorized relation projection was performed (metadata response).
func relationProjection(v []relationmeta.View) *[]relationmeta.View {
	if v == nil {
		return nil
	}
	return &v
}
