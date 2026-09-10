package problem

import (
	"errors"
	"strconv"
	"strings"

	"itsm-backend/common"
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

func (h *Handler) toDTO(p *Problem) *dto.ProblemResponse {
	resp := ToResponse(p)
	if resp == nil {
		return nil
	}

	// 映射关联数据
	if p.Tickets != nil {
		resp.AssociatedTickets = make([]*dto.AssociatedItemResponse, 0, len(p.Tickets))
		for _, t := range p.Tickets {
			resp.AssociatedTickets = append(resp.AssociatedTickets, &dto.AssociatedItemResponse{
				ID:     t.ID,
				Title:  t.Title,
				Status: t.Status,
				Number: t.Number,
				Type:   t.Type,
			})
		}
	}
	if p.Incidents != nil {
		resp.AssociatedIncidents = make([]*dto.AssociatedItemResponse, 0, len(p.Incidents))
		for _, inc := range p.Incidents {
			resp.AssociatedIncidents = append(resp.AssociatedIncidents, &dto.AssociatedItemResponse{
				ID:     inc.ID,
				Title:  inc.Title,
				Status: inc.Status,
				Number: inc.Number,
				Type:   inc.Type,
			})
		}
	}
	if p.Changes != nil {
		resp.AssociatedChanges = make([]*dto.AssociatedItemResponse, 0, len(p.Changes))
		for _, ch := range p.Changes {
			resp.AssociatedChanges = append(resp.AssociatedChanges, &dto.AssociatedItemResponse{
				ID:     ch.ID,
				Title:  ch.Title,
				Status: ch.Status,
				Number: ch.Number,
				Type:   ch.Type,
			})
		}
	}

	return resp
}

// ToResponse maps the Problem base fields to the public API contract. Handlers
// that load associations enrich this response in their own wrapper.
func ToResponse(p *Problem) *dto.ProblemResponse {
	if p == nil {
		return nil
	}

	resp := dto.ProblemResponse{
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

	p, err := h.service.GetWithAssociations(c.Request.Context(), id, actor.TenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			common.Fail(c, common.NotFoundErrorCode, "Problem not found")
		} else {
			common.Fail(c, common.InternalErrorCode, err.Error())
		}
		return
	}
	response := h.toDTO(p)
	response.Actions = BuildProblemActions(actor, p)
	common.Success(c, response)
}

// GetAssociations 获取问题的关联项
func (h *Handler) GetAssociations(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "invalid id")
		return
	}

	tenantID, ok := resolveProblemTenantID(c)
	if !ok {
		return
	}
	p, err := h.service.GetWithAssociations(c.Request.Context(), id, tenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			common.Fail(c, common.NotFoundErrorCode, "Problem not found")
		} else {
			common.Fail(c, common.InternalErrorCode, err.Error())
		}
		return
	}

	resp := &dto.ProblemAssociationResponse{
		Tickets:   make([]*dto.AssociatedItemResponse, 0),
		Incidents: make([]*dto.AssociatedItemResponse, 0),
		Changes:   make([]*dto.AssociatedItemResponse, 0),
	}
	for _, t := range p.Tickets {
		resp.Tickets = append(resp.Tickets, &dto.AssociatedItemResponse{
			ID: t.ID, Title: t.Title, Status: t.Status, Number: t.Number, Type: t.Type,
		})
	}
	for _, inc := range p.Incidents {
		resp.Incidents = append(resp.Incidents, &dto.AssociatedItemResponse{
			ID: inc.ID, Title: inc.Title, Status: inc.Status, Number: inc.Number, Type: inc.Type,
		})
	}
	for _, ch := range p.Changes {
		resp.Changes = append(resp.Changes, &dto.AssociatedItemResponse{
			ID: ch.ID, Title: ch.Title, Status: ch.Status, Number: ch.Number, Type: ch.Type,
		})
	}
	common.Success(c, resp)
}

// AddAssociation adds one explicit relation through the shared owner.
func (h *Handler) AddAssociation(c *gin.Context) { h.applyRelation(c, false) }

func (h *Handler) applyRelation(c *gin.Context, remove bool) {
	id, tenantID, ok := problemRequestContext(c)
	if !ok {
		return
	}
	var req dto.WorkItemRelationRequest
	if !intakehttp.Bind(c, &req) {
		return
	}
	// Bind has already rejected duplicate and unknown members. Relation commands
	// additionally reject explicit nulls, including the typed metadata object.
	value, _ := c.Get("intake.http.body")
	fields, _ := value.(map[string]any)
	for field, value := range fields {
		if value == nil {
			intakehttp.Fail(c, intakehttp.Invalid(field, "null is not supported"))
			return
		}
	}
	if metadata, ok := fields["metadata"].(map[string]any); ok {
		for field, value := range metadata {
			if value == nil {
				intakehttp.Fail(c, intakehttp.Invalid("metadata."+field, "null is not supported"))
				return
			}
		}
	}

	actorID, ok := problemActorUserID(c)
	if !ok {
		return
	}
	result, err := h.service.ApplyRelation(c.Request.Context(), id, service.RelationCommand{
		Meta:     workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, ExpectedVersion: req.ExpectedVersion, OperationID: req.OperationID, Source: "http"},
		SourceID: req.SourceWorkItemID, TargetID: req.TargetWorkItemID, Type: req.RelationType, Required: req.Metadata.Required,
	}, remove)
	if err != nil {
		RespondCommandError(c, err)
		return
	}
	common.Success(c, result)
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

// RemoveAssociation removes one relation with the original source identity.
func (h *Handler) RemoveAssociation(c *gin.Context) { h.applyRelation(c, true) }

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

	list, total, err := h.service.List(c.Request.Context(), tenantID, req.Page, req.PageSize, filters)
	if err != nil {
		common.Fail(c, common.InternalErrorCode, err.Error())
		return
	}

	// Map to DTO response
	dtoProblems := make([]*dto.ProblemResponse, 0, len(list))
	for _, p := range list {
		item := &dto.ProblemResponse{
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
			Impact:           p.Impact,
			CreatedBy:        p.CreatedBy,
			TenantID:         p.TenantID,
			CreatedAt:        p.CreatedAt,
			UpdatedAt:        p.UpdatedAt,
			WorkItemID:       p.WorkItemID,
		}
		if p.AssigneeID != nil {
			item.AssigneeID = p.AssigneeID
		}
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

	common.Success(c, h.toDTO(updated))
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
	common.Success(c, h.toDTO(updated))
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
