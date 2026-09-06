package intake

import (
	"github.com/gin-gonic/gin"
	"itsm-backend/common"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/middleware"
	"strconv"
)

// RegisterMappingRoutes is attached only to the existing authenticated tenant
// route group. Service authorization reads current RBAC inside each transaction.
func (h *Handler) RegisterMappingRoutes(group gin.IRoutes) {
	group.GET("/intake/identity-mappings", middleware.RequirePermission("intake_identity_mapping", "read"), h.ListMappings)
	group.POST("/intake/identity-mappings", middleware.RequirePermission("intake_identity_mapping", "write"), h.CreateMapping)
	group.PATCH("/intake/identity-mappings/:id", middleware.RequirePermission("intake_identity_mapping", "write"), h.UpdateMapping)
}
func mappingActor(c *gin.Context) creation.Identity {
	return creation.Identity{TenantID: c.GetInt("tenant_id"), ActorID: c.GetInt("user_id"), RequesterID: c.GetInt("user_id"), Role: c.GetString("role"), Channel: "http"}
}
func (h *Handler) ListMappings(c *gin.Context) {
	result, err := h.mappings.List(c.Request.Context(), mappingActor(c))
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	common.Success(c, result)
}
func (h *Handler) CreateMapping(c *gin.Context) {
	var input CreateIdentityMapping
	if err := decodeIdentityBody(c, &input); err != nil {
		intakehttp.Fail(c, err)
		return
	}
	result, err := h.mappings.Create(c.Request.Context(), mappingActor(c), input)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	c.JSON(201, common.Response{Code: 0, Message: "success", Data: result})
}
func (h *Handler) UpdateMapping(c *gin.Context) {
	var input struct {
		Version int   `json:"version"`
		Active  *bool `json:"active"`
	}
	if err := decodeIdentityBody(c, &input); err != nil {
		intakehttp.Fail(c, err)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || input.Active == nil {
		intakehttp.Fail(c, creation.NewInvalidCommand("mapping id, version and active required", creation.FieldError{}, nil))
		return
	}
	result, err := h.mappings.Update(c.Request.Context(), mappingActor(c), id, input.Version, *input.Active)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	common.Success(c, result)
}
