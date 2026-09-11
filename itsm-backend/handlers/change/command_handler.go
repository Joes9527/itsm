package change

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
)

type MutationRequest struct {
	ExpectedVersion int    `json:"expectedVersion" binding:"required,gt=0"`
	OperationID     string `json:"operationId" binding:"required"`
}
type MetadataRequest struct {
	MutationRequest
	dto.UpdateChangeRequest
}
type ActionRequest struct {
	MutationRequest
	TaskID       string     `json:"taskId,omitempty"`
	Evidence     string     `json:"evidence,omitempty"`
	Outcome      string     `json:"outcome,omitempty"`
	PlannedStart *time.Time `json:"plannedStartDate,omitempty"`
	PlannedEnd   *time.Time `json:"plannedEndDate,omitempty"`
	ActualEnd    *time.Time `json:"actualEndDate,omitempty"`
	PIRID        int        `json:"pirId,omitempty"`
}

func bindChangeMutation(c *gin.Context, target any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, common.MaxJSONBodyBytes))
	if err == nil {
		var fields map[string]any
		fields, err = common.DecodeJSONObject(raw)
		if err == nil {
			allowed := map[string]bool{}
			var collect func(reflect.Type)
			collect = func(typ reflect.Type) {
				for typ.Kind() == reflect.Pointer {
					typ = typ.Elem()
				}
				for i := 0; i < typ.NumField(); i++ {
					field := typ.Field(i)
					name := strings.Split(field.Tag.Get("json"), ",")[0]
					if field.Anonymous && name == "" {
						collect(field.Type)
					} else if name != "" && name != "-" {
						allowed[name] = true
					}
				}
			}
			collect(reflect.TypeOf(target))
			for name, value := range fields {
				if !allowed[name] || value == nil {
					err = fmt.Errorf("unsupported or null field %s", name)
					break
				}
			}
		}
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(target)
	}
	if err == nil {
		err = binding.Validator.ValidateStruct(target)
	}
	if err != nil {
		common.ParamError(c, "Invalid Change command body: "+err.Error())
		return false
	}
	return true
}
func changeHTTPIdentity(c *gin.Context) (int, workitemmutation.Meta, bool) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return 0, workitemmutation.Meta{}, false
	}
	tenant, ok := resolveChangeTenantID(c)
	if !ok {
		return 0, workitemmutation.Meta{}, false
	}
	actor, ok := c.Get("user_id")
	actorID, typed := actor.(int)
	if !ok || !typed || actorID <= 0 {
		common.AuthFailed(c, "authenticated actor required")
		return 0, workitemmutation.Meta{}, false
	}
	return id, workitemmutation.Meta{TenantID: tenant, ActorID: actorID, Source: "http", CorrelationID: c.GetString("request_id")}, true
}

// UpdateChange API contract.
// @Summary UpdateChange
// @Description Immutable mutation receipt. RelatedTickets is unsupported; refresh separately.
// @Tags changes
// @Accept json
// @Produce json
// @Param id path int true "Professional extension ID"
// @Param body body MetadataRequest true "Request"
// @Success 200 {object} common.Response{data=workitemmutation.Result}
// @Router /api/v1/changes/{id} [put]
func (h *Handler) UpdateChange(c *gin.Context) {
	id, meta, ok := changeHTTPIdentity(c)
	if !ok {
		return
	}
	var req MetadataRequest
	if !bindChangeMutation(c, &req) {
		return
	}
	meta.ExpectedVersion, meta.OperationID = req.ExpectedVersion, req.OperationID
	result, err := h.svc.ApplyMetadata(c.Request.Context(), MetadataCommand{Meta: meta, ChangeID: id, Patch: req.UpdateChangeRequest})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}
	common.Success(c, result)
}

func (h *Handler) AssignChange(c *gin.Context) {
	id, meta, ok := changeHTTPIdentity(c)
	if !ok {
		return
	}
	var req struct {
		MutationRequest
		AssigneeID       int    `json:"assigneeId" binding:"required,gt=0"`
		AssignmentReason string `json:"assignmentReason"`
	}
	if !bindChangeMutation(c, &req) {
		return
	}
	meta.ExpectedVersion, meta.OperationID = req.ExpectedVersion, req.OperationID
	result, err := h.svc.ApplyMetadata(c.Request.Context(), MetadataCommand{Meta: meta, ChangeID: id, Patch: dto.UpdateChangeRequest{AssigneeID: &req.AssigneeID, AssignmentReason: req.AssignmentReason}})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}
	common.Success(c, result)
}
func (h *Handler) UpdateRisk(c *gin.Context) {
	id, meta, ok := changeHTTPIdentity(c)
	if !ok {
		return
	}
	var req struct {
		MutationRequest
		dto.ChangeRiskPatch
		RiskLevel *dto.ChangeRisk `json:"riskLevel"`
	}
	if !bindChangeMutation(c, &req) {
		return
	}
	meta.ExpectedVersion, meta.OperationID = req.ExpectedVersion, req.OperationID
	result, err := h.svc.ApplyMetadata(c.Request.Context(), MetadataCommand{Meta: meta, ChangeID: id, Patch: dto.UpdateChangeRequest{RiskLevel: req.RiskLevel, ChangeRiskPatch: req.ChangeRiskPatch}})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}
	common.Success(c, result)
}
func (h *Handler) ExecuteAction(c *gin.Context) {
	parts := strings.Split(c.FullPath(), "/")
	h.executeAction(c, parts[len(parts)-1])
}
func (h *Handler) executeAction(c *gin.Context, action string) {
	id, meta, ok := changeHTTPIdentity(c)
	if !ok {
		return
	}
	var req ActionRequest
	if !bindChangeMutation(c, &req) {
		return
	}
	meta.ExpectedVersion, meta.OperationID = req.ExpectedVersion, req.OperationID
	if action == "record-outcome" {
		action = "record_outcome"
	}
	cmd := Command{Meta: meta, ChangeID: id, Action: action, Evidence: req.Evidence, Outcome: req.Outcome, PlannedStart: req.PlannedStart, PlannedEnd: req.PlannedEnd, ActualEnd: req.ActualEnd, PIRID: req.PIRID}
	if action == "submit" || action == "cancel" {
		if req.TaskID != "" || req.Outcome != "" || req.PlannedStart != nil || req.PlannedEnd != nil || req.ActualEnd != nil || req.PIRID != 0 {
			common.ParamError(c, "facts do not belong to this action")
			return
		}
		result, err := h.svc.ApplyCommand(c.Request.Context(), cmd)
		if err != nil {
			respondPIRMutationError(c, err)
			return
		}
		common.Success(c, result)
		return
	}
	result, err := h.svc.CompleteChangeTask(c.Request.Context(), TaskCommand{Command: cmd, TaskID: req.TaskID})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}
	respondChangeProgress(c, result)
}
func respondChangeProgress(c *gin.Context, result TaskProgress) {
	code, message := common.SuccessCode, "success"
	if result.HTTPStatus() == 409 {
		code, message = common.ConflictCode, "Change workflow is blocked"
	}
	c.JSON(result.HTTPStatus(), common.Response{Code: code, Message: message, Data: result})
}
func (h *Handler) GetTaskProgress(c *gin.Context) {
	id, meta, ok := changeHTTPIdentity(c)
	if !ok {
		return
	}
	meta.OperationID = c.Query("operationId")
	result, err := h.svc.GetTaskProgress(c.Request.Context(), TaskProgressQuery{Meta: meta, ChangeID: id, Action: c.Query("action")})
	if err != nil {
		respondPIRMutationError(c, err)
		return
	}
	respondChangeProgress(c, result)
}
