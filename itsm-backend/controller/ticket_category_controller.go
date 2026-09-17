package controller

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// respondCategoryError 把分类维护的结构化失败映射到既有错误码。
// 业务拒绝不再统一伪装成 500，也不泄露跨租户对象或引用对象的名称/计数。
func respondCategoryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrCTICategoryNotFound):
		common.Fail(c, common.NotFoundCode, "分类不存在")
	case errors.Is(err, service.ErrCTICategoryHasChildren):
		common.Fail(c, common.ConflictCode, "请先处理该分类的下级分类")
	case errors.Is(err, service.ErrCTICategoryReferenced):
		common.Fail(c, common.ConflictCode, "该分类已被工单或配置引用，无法删除或移动")
	case errors.Is(err, service.ErrCTICategoryPublishedCatalog):
		common.Fail(c, common.ConflictCode, "已发布的服务目录正在使用该分类或其下级，请先调整目录")
	case errors.Is(err, service.ErrCTICategoryCodeImmutable):
		common.Fail(c, common.ParamErrorCode, "分类代码创建后不可修改")
	case errors.Is(err, service.ErrCTIPathIncomplete):
		common.Fail(c, common.ParamErrorCode, "请补齐完整的三级工单分类")
	case errors.Is(err, service.ErrCTIPathTooDeep):
		common.Fail(c, common.ParamErrorCode, "工单分类最多三级")
	case errors.Is(err, service.ErrCTIPathInactive):
		common.Fail(c, common.ParamErrorCode, "所选分类已停用，请重新选择")
	case errors.Is(err, service.ErrCTIPathHierarchy), errors.Is(err, service.ErrCTIPathOutsideTenant):
		common.Fail(c, common.ParamErrorCode, "分类层级或租户不一致")
	default:
		common.Fail(c, common.InternalErrorCode, err.Error())
	}
}

type TicketCategoryController struct {
	categoryService *service.TicketCategoryService
	logger          *zap.SugaredLogger
}

type ticketCategoryImportRow struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
	ParentCode  string `json:"parentCode"`
	SortOrder   int    `json:"sortOrder"`
	IsActive    bool   `json:"isActive"`
}

func NewTicketCategoryController(categoryService *service.TicketCategoryService, logger *zap.SugaredLogger) *TicketCategoryController {
	return &TicketCategoryController{
		categoryService: categoryService,
		logger:          logger,
	}
}

// CreateCategory 创建工单分类
// @Summary 创建工单分类
// @Description 创建新的工单分类
// @Tags 工单分类
// @Accept json
// @Produce json
// @Param request body service.CreateCategoryRequest true "分类信息"
// @Success 200 {object} common.Response
// @Router /api/v1/ticket-categories [post]
func (tc *TicketCategoryController) CreateCategory(c *gin.Context) {
	var req service.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}
	req.TenantID = tenantID

	category, err := tc.categoryService.CreateCategory(c.Request.Context(), &req)
	if err != nil {
		tc.logger.Errorw("Failed to create ticket category", "error", err, "tenant_id", tenantID)
		respondCategoryError(c, err)
		return
	}

	common.Success(c, dto.ToTicketCategoryResponse(category))
}

// GetCategoryReferences 返回分类（含子树）的真实引用，并按调用者 RBAC 过滤明细。
//
// 说明：Referenced 来自不受 RBAC 影响的安全扫描（执行维护操作必须知道"存在引用"），
// 而无权查看某类对象的调用者不会拿到该类的名称、条数与 ID。
// @Summary 分类引用清单
// @Tags 工单分类
// @Produce json
// @Param id path int true "分类ID"
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数（最大100）"
// @Success 200 {object} common.Response{data=service.CTIReferenceView}
// @Router /api/v1/ticket-categories/{id}/references [get]
func (tc *TicketCategoryController) GetCategoryReferences(c *gin.Context) {
	categoryID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的分类ID")
		return
	}
	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("pageSize"))
	view, err := tc.categoryService.ReferenceView(c.Request.Context(), service.CTIReferenceQuery{
		TenantID:   tenantID,
		ActorRole:  c.GetString("role"),
		CategoryID: categoryID,
		Page:       page,
		PageSize:   pageSize,
		// 工单引用计数按 ticket:read 过滤，避免泄露不可见工单的规模。
		IncludeWorkItems: true,
	})
	if err != nil {
		respondCategoryError(c, err)
		return
	}
	common.Success(c, view)
}

// UpdateCategory 更新工单分类
// @Summary 更新工单分类
// @Description 更新工单分类信息
// @Tags 工单分类
// @Accept json
// @Produce json
// @Param id path int true "分类ID"
// @Param request body service.CreateCategoryRequest true "分类信息"
// @Success 200 {object} common.Response
// @Router /api/v1/ticket-categories/{id} [put]
func (tc *TicketCategoryController) UpdateCategory(c *gin.Context) {
	categoryID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的分类ID")
		return
	}

	var req service.UpdateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	category, err := tc.categoryService.UpdateCategory(c.Request.Context(), categoryID, &req, tenantID)
	if err != nil {
		tc.logger.Errorw("Failed to update ticket category", "error", err, "category_id", categoryID)
		respondCategoryError(c, err)
		return
	}

	common.Success(c, dto.ToTicketCategoryResponse(category))
}

// DeleteCategory 删除工单分类
func (tc *TicketCategoryController) DeleteCategory(c *gin.Context) {
	categoryID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的分类ID")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	err = tc.categoryService.DeleteCategory(c.Request.Context(), categoryID, tenantID)
	if err != nil {
		tc.logger.Errorw("Failed to delete ticket category", "error", err, "category_id", categoryID)
		respondCategoryError(c, err)
		return
	}

	common.Success(c, gin.H{"message": "分类删除成功"})
}

// PreviewImport 预览工单分类导入数据
func (tc *TicketCategoryController) PreviewImport(c *gin.Context) {
	rows, err := tc.parseImportRows(c)
	if err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}

	common.Success(c, rows)
}

// ExecuteImport 执行工单分类导入
func (tc *TicketCategoryController) ExecuteImport(c *gin.Context) {
	rows, err := tc.parseImportRows(c)
	if err != nil {
		common.Fail(c, common.ParamErrorCode, err.Error())
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	// 导入按 parent_code 重建层级，并复用与手工创建相同的分类服务校验（三级上限、
	// 租户内父子关系、编码唯一）。父行可能排在子行之后，因此最多重试到最大层级+1 轮；
	// 仍无法解析的行才算失败，不会被静默降级成一级分类。
	type pendingRow struct {
		row      ticketCategoryImportRow
		lastErr  error
		failFast bool
	}
	pending := make([]pendingRow, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.Name) == "" || strings.TrimSpace(row.Code) == "" {
			pending = append(pending, pendingRow{row: row, lastErr: fmt.Errorf("分类名称和代码不能为空"), failFast: true})
			continue
		}
		pending = append(pending, pendingRow{row: row})
	}

	success := 0
	for pass := 0; pass <= service.CTIMaxDepth && len(pending) > 0; pass++ {
		attempted := pending
		pending = pending[:0:0]
		progress := 0
		for _, item := range attempted {
			if item.failFast {
				pending = append(pending, item)
				continue
			}
			parentID := 0
			if strings.TrimSpace(item.row.ParentCode) != "" {
				parent, err := tc.categoryService.GetCategoryByCode(c.Request.Context(), tenantID, item.row.ParentCode)
				if err != nil {
					item.lastErr = fmt.Errorf("父分类代码不存在: %s", item.row.ParentCode)
					pending = append(pending, item)
					continue
				}
				parentID = parent.ID
			}
			_, err := tc.categoryService.CreateCategory(c.Request.Context(), &service.CreateCategoryRequest{
				Name:        item.row.Name,
				Code:        item.row.Code,
				Description: item.row.Description,
				SortOrder:   item.row.SortOrder,
				IsActive:    item.row.IsActive,
				ParentID:    parentID,
				TenantID:    tenantID,
			})
			if err != nil {
				item.lastErr = err
				pending = append(pending, item)
				continue
			}
			success++
			progress++
		}
		if progress == 0 {
			break
		}
	}
	failed := 0
	for _, item := range pending {
		failed++
		tc.logger.Warnw("Failed to import ticket category", "error", item.lastErr, "code", item.row.Code, "tenant_id", tenantID)
	}

	common.Success(c, gin.H{"success": success, "failed": failed})
}

func (tc *TicketCategoryController) parseImportRows(c *gin.Context) ([]ticketCategoryImportRow, error) {
	const maxCategoryImportSize = 2 << 20 // 2 MiB
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return nil, err
	}
	if fileHeader.Size <= 0 || fileHeader.Size > maxCategoryImportSize {
		return nil, fmt.Errorf("导入文件大小必须在 1 字节到 2 MiB 之间")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxCategoryImportSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxCategoryImportSize {
		return nil, fmt.Errorf("导入文件超过 2 MiB 限制")
	}
	if len(content) == 0 {
		return nil, io.ErrUnexpectedEOF
	}

	trimmed := strings.TrimSpace(string(content))
	if strings.HasPrefix(trimmed, "[") {
		var rows []ticketCategoryImportRow
		if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
			return nil, err
		}
		return normalizeImportRows(rows), nil
	}

	reader := csv.NewReader(strings.NewReader(trimmed))
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return []ticketCategoryImportRow{}, nil
	}

	headerIndex := make(map[string]int, len(records[0]))
	for i, header := range records[0] {
		headerIndex[strings.ToLower(strings.TrimSpace(header))] = i
	}

	rows := make([]ticketCategoryImportRow, 0, len(records)-1)
	for _, record := range records[1:] {
		row := ticketCategoryImportRow{
			Name:        importCell(record, headerIndex, "name"),
			Code:        importCell(record, headerIndex, "code"),
			Description: importCell(record, headerIndex, "description"),
			ParentCode:  importCell(record, headerIndex, "parent_code"),
			SortOrder:   parseImportInt(importCell(record, headerIndex, "sort_order")),
			IsActive:    parseImportBool(importCell(record, headerIndex, "is_active"), true),
		}
		rows = append(rows, row)
	}

	return normalizeImportRows(rows), nil
}

func normalizeImportRows(rows []ticketCategoryImportRow) []ticketCategoryImportRow {
	for i := range rows {
		rows[i].Name = strings.TrimSpace(rows[i].Name)
		rows[i].Code = strings.TrimSpace(rows[i].Code)
		rows[i].Description = strings.TrimSpace(rows[i].Description)
		rows[i].ParentCode = strings.TrimSpace(rows[i].ParentCode)
		if rows[i].Code == "" {
			rows[i].Code = rows[i].Name
		}
	}
	return rows
}

func importCell(record []string, headerIndex map[string]int, key string) string {
	index, ok := headerIndex[key]
	if !ok || index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func parseImportInt(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func parseImportBool(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// GetCategory 获取分类详情
func (tc *TicketCategoryController) GetCategory(c *gin.Context) {
	categoryID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的分类ID")
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	category, err := tc.categoryService.GetCategory(c.Request.Context(), categoryID, tenantID)
	if err != nil {
		tc.logger.Errorw("Failed to get ticket category", "error", err, "category_id", categoryID)
		common.Fail(c, common.NotFoundCode, "分类不存在")
		return
	}

	common.Success(c, dto.ToTicketCategoryResponse(category))
}

// ListCategories 获取分类列表
func (tc *TicketCategoryController) ListCategories(c *gin.Context) {
	tenantID := c.GetInt("tenant_id")
	parentIDStr := c.Query("parent_id")
	levelStr := c.Query("level")
	activeStr := c.Query("active")

	var parentID *int
	var level int
	var active *bool

	if parentIDStr != "" {
		if id, err := strconv.Atoi(parentIDStr); err == nil {
			parentID = &id
		}
	}

	if levelStr != "" {
		if l, err := strconv.Atoi(levelStr); err == nil {
			level = l
		}
	}

	if activeStr != "" {
		if a, err := strconv.ParseBool(activeStr); err == nil {
			active = &a
		}
	}

	page := 1
	pageSize := 100
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(c.Query("pageSize")); err == nil && ps > 0 {
		pageSize = ps
	}
	pageSize = min(pageSize, 500)

	req := &service.ListCategoriesRequest{
		Page:     page,
		PageSize: pageSize,
		ParentID: parentID,
		Level:    level,
		IsActive: active,
		TenantID: tenantID,
	}

	categories, total, err := tc.categoryService.ListCategories(c.Request.Context(), req)
	if err != nil {
		tc.logger.Errorw("Failed to list ticket categories", "error", err, "tenant_id", tenantID)
		common.Fail(c, common.InternalErrorCode, err.Error())
		return
	}

	common.Success(c, gin.H{
		"categories": dto.ToTicketCategoryResponseList(categories),
		"total":      total,
	})
}

// GetCategoryTree 获取分类树形结构。
// includeInactive=true 供维护界面使用（显示状态、恢复停用节点）；默认只返回启用节点，
// 保持选择器/申请入口的既有契约。
func (tc *TicketCategoryController) GetCategoryTree(c *gin.Context) {
	tenantID := c.GetInt("tenant_id")

	includeInactive := false
	if raw := c.Query("includeInactive"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			common.Fail(c, common.ParamErrorCode, "includeInactive 必须是布尔值")
			return
		}
		includeInactive = parsed
	}

	tree, err := tc.categoryService.GetCategoryTree(c.Request.Context(), tenantID, includeInactive)
	if err != nil {
		tc.logger.Errorw("Failed to get category tree", "error", err, "tenant_id", tenantID)
		common.Fail(c, common.InternalErrorCode, err.Error())
		return
	}

	common.Success(c, tree)
}

// MoveCategory 移动分类位置
func (tc *TicketCategoryController) MoveCategory(c *gin.Context) {
	categoryID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.Fail(c, common.ParamErrorCode, "无效的分类ID")
		return
	}

	var req service.MoveCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, common.ParamErrorCode, "请求参数错误: "+err.Error())
		return
	}

	tenantID := c.GetInt("tenant_id")
	if tenantID == 0 {
		common.Fail(c, common.AuthFailedCode, "租户信息缺失")
		return
	}

	category, err := tc.categoryService.MoveCategory(c.Request.Context(), categoryID, &req, tenantID)
	if err != nil {
		tc.logger.Errorw("Failed to move ticket category", "error", err, "category_id", categoryID)
		respondCategoryError(c, err)
		return
	}

	common.Success(c, dto.ToTicketCategoryResponse(category))
}
