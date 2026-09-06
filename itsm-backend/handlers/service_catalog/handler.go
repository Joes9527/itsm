package service_catalog

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin/binding"
	"io"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"

	"github.com/gin-gonic/gin"
)

// Handler handles HTTP requests for Service Catalog
type Handler struct {
	service *Service
}

func failServiceCatalog(c *gin.Context, err error) {
	var intakeErr *creation.IntakeError
	if errors.As(err, &intakeErr) {
		codes := map[int]int{400: common.ParamErrorCode, 401: common.AuthFailedCode, 403: common.ForbiddenCode, 404: common.NotFoundCode, 409: common.ConflictCode, 500: common.InternalErrorCode, 503: common.ServiceUnavailableCode}
		common.FailWithData(c, codes[intakeErr.HTTPStatus], intakeErr.Message, gin.H{"errorCode": intakeErr.Code, "retryable": intakeErr.Retryable, "fieldErrors": intakeErr.FieldErrors})
		return
	}

	if appErr, ok := common.AsAppError(err); ok {
		switch appErr.Code {
		case common.ErrCodeBadRequest, common.ErrCodeValidation:
			common.Fail(c, common.ParamErrorCode, appErr.Message)
		case common.ErrCodeConflict:
			common.Fail(c, common.ConflictCode, appErr.Error())
		case common.ErrCodeNotFound:
			common.Fail(c, common.NotFoundErrorCode, appErr.Message)
		default:
			common.Fail(c, common.InternalErrorCode, appErr.Message)
		}
		return
	}
	if ent.IsNotFound(err) {
		common.Fail(c, common.NotFoundErrorCode, "服务目录不存在")
		return
	}
	common.Fail(c, common.InternalErrorCode, err.Error())
}

// NewHandler creates a new Handler
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// List handles partial match for GetServiceCatalogs
func (h *Handler) List(c *gin.Context) {
	var req dto.GetServiceCatalogsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.Fail(c, 1001, "参数错误: "+err.Error())
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	filters := ListFilters{
		Category: req.Category,
		Status:   req.Status,
		Page:     req.Page,
		Size:     req.Size,
	}
	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Size < 1 {
		filters.Size = 10
	}
	if filters.Size > 100 {
		filters.Size = 100
	}

	catalogs, total, err := h.service.List(c.Request.Context(), tenantID, filters)
	if err != nil {
		common.Fail(c, 5001, err.Error())
		return
	}

	var responses []dto.ServiceCatalogResponse
	for _, cat := range catalogs {
		responses = append(responses, h.toDTO(cat))
	}

	common.Success(c, dto.ServiceCatalogListResponse{
		Catalogs: responses,
		Total:    total,
		Page:     filters.Page,
		Size:     filters.Size,
	})
}

// Get handles GetServiceCatalogByID
func (h *Handler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, 1001, "无效的服务目录ID")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	catalog, err := h.service.Read(c.Request.Context(), creation.Identity{TenantID: tenantID, ActorID: c.GetInt("user_id"), RequesterID: c.GetInt("user_id"), Role: c.GetString("role"), Channel: "web"}, id)
	if err != nil {
		failServiceCatalog(c, err)
		return
	}

	common.Success(c, h.toDTO(catalog))
}

// Create handles CreateServiceCatalog
func (h *Handler) Create(c *gin.Context) {
	var req dto.CreateServiceCatalogRequest
	if err := bindCatalogJSON(c, &req); err != nil {
		common.Fail(c, 1001, "参数错误: "+err.Error())
		return
	}
	normalizeServiceCatalogRequest(&req)
	if req.CloudServiceID > 0 && req.CITypeID == 0 {
		common.Fail(c, 1001, "关联云服务时必须选择CI类型")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	catalog, err := h.service.Create(c.Request.Context(), tenantID, req)
	if err != nil {
		failServiceCatalog(c, err)
		return
	}

	common.Success(c, h.toDTO(catalog))
}

// Update handles UpdateServiceCatalog
func (h *Handler) Update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, 1001, "无效的服务目录ID")
		return
	}

	var req dto.UpdateServiceCatalogRequest
	if err := bindCatalogJSON(c, &req); err != nil {
		common.Fail(c, 1001, "参数错误: "+err.Error())
		return
	}
	normalizeUpdateServiceCatalogRequest(&req)

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	updated, err := h.service.Update(c.Request.Context(), tenantID, id, req)
	if err != nil {
		failServiceCatalog(c, err)
		return
	}

	common.Success(c, h.toDTO(updated))
}

func normalizeServiceCatalogRequest(req *dto.CreateServiceCatalogRequest) {
	if req.DeliveryTime == "" {
		req.DeliveryTime = "1"
	}
	if req.Status == "" {
		req.Status = "disabled"
	}
}

func normalizeUpdateServiceCatalogRequest(req *dto.UpdateServiceCatalogRequest) {
}

// Delete handles DeleteServiceCatalog
func (h *Handler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, 1001, "无效的服务目录ID")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	if err := h.service.Delete(c.Request.Context(), tenantID, id, c.Query("expectedCatalogVersion")); err != nil {
		failServiceCatalog(c, err)
		return
	}

	common.Success(c, nil)
}

// Search handles GET /api/v1/service-catalogs/search?q=xxx
func (h *Handler) Search(c *gin.Context) {
	keyword := c.Query("q")
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	filters := ListFilters{
		Category: c.Query("category"),
		Status:   "enabled",
		Page:     1,
		Size:     20,
	}

	catalogs, total, err := h.service.Search(c.Request.Context(), tenantID, keyword, filters)
	if err != nil {
		common.Fail(c, 5001, err.Error())
		return
	}

	var responses []dto.ServiceCatalogResponse
	for _, cat := range catalogs {
		responses = append(responses, h.toDTO(cat))
	}

	common.Success(c, dto.ServiceCatalogListResponse{
		Catalogs: responses,
		Total:    total,
		Page:     filters.Page,
		Size:     filters.Size,
	})
}

// Stats handles GET /api/v1/service-catalogs/stats
func (h *Handler) Stats(c *gin.Context) {
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	stats, err := h.service.GetStats(c.Request.Context(), tenantID)
	if err != nil {
		common.Fail(c, 5001, err.Error())
		return
	}

	common.Success(c, stats)
}

func (h *Handler) toDTO(c *ServiceCatalog) dto.ServiceCatalogResponse {
	fields := make([]map[string]interface{}, 0, len(c.Fields))
	for _, d := range c.Fields {
		fields = append(fields, map[string]interface{}{
			"name": d.Name, "label": d.Label, "type": d.FieldType,
			"required": d.Required, "options": d.Options, "sortOrder": d.SortOrder,
		})
	}
	return dto.ServiceCatalogResponse{AccessPolicy: c.AccessPolicy,
		CatalogVersion: c.CatalogVersion, FormSchemaVersion: c.FormSchemaVersion,
		ID:                   c.ID,
		Name:                 c.Name,
		Category:             c.Category,
		Description:          c.Description,
		DeliveryTime:         strconv.Itoa(c.DeliveryTime),
		CITypeID:             c.CITypeID,
		CloudServiceID:       c.CloudServiceID,
		ProcessDefinitionKey: c.ProcessDefinitionKey,
		Status:               c.Status,
		ServiceType:          c.ServiceType,
		TargetClass:          c.TargetClass,
		RequiresApproval:     c.RequiresApproval, SLAResponseTime: c.SLAResponseTime, SLAResolutionTime: c.SLAResolutionTime,
		RequiresInfraFields: RequiresInfraFields(c.ServiceType),
		Fields:              fields,
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
	}
}

func bindCatalogJSON(c *gin.Context, value any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("exactly one JSON object is required")
	}
	return binding.Validator.ValidateStruct(value)
}
