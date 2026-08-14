package knowledge

import (
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// toArticleDTO maps domain Article to DTO
func toArticleDTO(a *Article) *dto.KnowledgeArticleResponse {
	if a == nil {
		return nil
	}
	status := "draft"
	if a.IsPublished {
		status = "published"
	}
	return &dto.KnowledgeArticleResponse{
		ID:        a.ID,
		Title:     a.Title,
		Content:   a.Content,
		Category:  a.Category,
		Tags:      a.Tags,
		Status:    status,
		TenantID:  a.TenantID,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
}

// CreateArticle handles POST /api/v1/knowledge-articles
func (h *Handler) CreateArticle(c *gin.Context) {
	var req dto.CreateKnowledgeArticleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ParamError(c, "Invalid request body")
		return
	}

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	userIDVal, ok := c.Get("user_id")
	if !ok {
		common.ParamError(c, "User ID not found")
		return
	}

	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}
	userID, ok := userIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid user ID")
		return
	}

	article := &Article{
		Title:    req.Title,
		Content:  req.Content,
		Category: req.Category,
		Tags:     req.Tags,
		AuthorID: userID,
		TenantID: tenantID,
	}

	res, err := h.svc.CreateArticle(c.Request.Context(), article)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, toArticleDTO(res))
}

// GetArticle handles GET /api/v1/knowledge-articles/:id
func (h *Handler) GetArticle(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	res, err := h.svc.GetArticle(c.Request.Context(), id, tenantID)
	if err != nil {
		common.NotFound(c, "Article not found")
		return
	}

	common.Success(c, toArticleDTO(res))
}

// ListArticles handles GET /api/v1/knowledge-articles
func (h *Handler) ListArticles(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	category := c.Query("category")
	search := c.Query("search")
	status := c.Query("status")

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	list, total, err := h.svc.ListArticles(c.Request.Context(), tenantID, page, pageSize, category, search, status)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	var dtos []dto.KnowledgeArticleResponse
	for _, item := range list {
		dtos = append(dtos, *toArticleDTO(item))
	}

	common.Success(c, dto.KnowledgeArticleListResponse{
		Articles: dtos,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// UpdateArticle handles PUT /api/v1/knowledge-articles/:id
func (h *Handler) UpdateArticle(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	var req dto.UpdateKnowledgeArticleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ParamError(c, "Invalid request body")
		return
	}

	existing, err := h.svc.GetArticle(c.Request.Context(), id, tenantID)
	if err != nil {
		common.NotFound(c, "Article not found")
		return
	}

	// 更新前快照当前状态为历史版本
	userID, _ := c.Get("user_id")
	operatorID, _ := userID.(int)
	nextVersion, err := h.svc.NextVersion(c.Request.Context(), id, tenantID)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}
	if _, err := h.svc.SnapshotVersion(c.Request.Context(), &ArticleVersion{
		ArticleID:     existing.ID,
		Version:       nextVersion,
		Title:         existing.Title,
		Content:       existing.Content,
		Category:      existing.Category,
		Tags:          strings.Join(existing.Tags, ","),
		AuthorID:      operatorID,
		ChangeSummary: "编辑文章",
		TenantID:      tenantID,
	}); err != nil {
		common.InternalError(c, err.Error())
		return
	}

	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Content != nil {
		existing.Content = *req.Content
	}
	if req.Category != nil {
		existing.Category = *req.Category
	}
	if req.Tags != nil {
		existing.Tags = req.Tags
	}
	if req.Status != nil {
		existing.IsPublished = *req.Status == "published"
	}

	res, err := h.svc.UpdateArticle(c.Request.Context(), existing)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, toArticleDTO(res))
}

// PublishArticle handles POST /api/v1/knowledge/articles/:id/publish
func (h *Handler) PublishArticle(c *gin.Context) {
	h.setArticlePublished(c, true)
}

// UnpublishArticle handles POST /api/v1/knowledge/articles/:id/unpublish
func (h *Handler) UnpublishArticle(c *gin.Context) {
	h.setArticlePublished(c, false)
}

func (h *Handler) setArticlePublished(c *gin.Context, published bool) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	article, err := h.svc.GetArticle(c.Request.Context(), id, tenantID)
	if err != nil {
		common.NotFound(c, "Article not found")
		return
	}

	article.IsPublished = published
	res, err := h.svc.UpdateArticle(c.Request.Context(), article)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, toArticleDTO(res))
}

// DeleteArticle handles DELETE /api/v1/knowledge-articles/:id
func (h *Handler) DeleteArticle(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	if err := h.svc.DeleteArticle(c.Request.Context(), id, tenantID); err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, nil)
}

// GetArticleComments handles GET /api/v1/knowledge/articles/:id/comments
func (h *Handler) GetArticleComments(c *gin.Context) {
	// Stub implementation
	common.Success(c, gin.H{
		"comments": []interface{}{},
		"total":    0,
	})
}

// AddArticleComment handles POST /api/v1/knowledge/articles/:id/comments
func (h *Handler) AddArticleComment(c *gin.Context) {
	// Stub implementation
	common.Success(c, gin.H{
		"id":        "stub_comment_id",
		"content":   "This is a stub comment",
		"createdAt": "2024-01-01T00:00:00Z",
	})
}

// SearchArticles handles POST /api/v1/knowledge/search
func (h *Handler) SearchArticles(c *gin.Context) {
	var req struct {
		Query    string `json:"query" binding:"required"`
		Category string `json:"category"`
		Limit    int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ParamError(c, "参数错误: "+err.Error())
		return
	}

	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantIDInt, ok := tenantIDVal.(int)
	if !ok || tenantIDInt == 0 {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	articles, total, err := h.svc.ListArticles(c.Request.Context(), tenantIDInt, 1, limit, req.Category, req.Query, "")
	if err != nil {
		common.InternalError(c, "搜索失败: "+err.Error())
		return
	}

	items := make([]interface{}, 0, len(articles))
	for _, a := range articles {
		items = append(items, map[string]interface{}{
			"id":           a.ID,
			"title":        a.Title,
			"category":     a.Category,
			"snippet":      snippet(a.Content, 200),
			"tags":         a.Tags,
			"is_published": a.IsPublished,
			"score":        0.8,
			"search_type":  "keyword",
		})
	}

	common.Success(c, gin.H{
		"items": items,
		"total": total,
	})
}

func snippet(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// GetRecommendations handles GET /api/v1/knowledge/recommendations
func (h *Handler) GetRecommendations(c *gin.Context) {
	// Stub implementation
	common.Success(c, []interface{}{})
}

// GetRecentArticles handles GET /api/v1/knowledge/recent
func (h *Handler) GetRecentArticles(c *gin.Context) {
	// Stub implementation
	common.Success(c, []interface{}{})
}

// GetCategories handles GET /api/v1/knowledge-articles/categories
func (h *Handler) GetCategories(c *gin.Context) {
	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	list, err := h.svc.GetCategories(c.Request.Context(), tenantID)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, list)
}

// GetStats handles GET /api/v1/knowledge/stats
func (h *Handler) GetStats(c *gin.Context) {
	tenantIDVal, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	tenantID, ok := tenantIDVal.(int)
	if !ok {
		common.ParamError(c, "Invalid tenant ID")
		return
	}

	stats, err := h.svc.GetStats(c.Request.Context(), tenantID)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	common.Success(c, stats)
}

// ==================== 版本控制 ====================

// ListArticleVersions handles GET /api/v1/knowledge/articles/:id/versions
func (h *Handler) ListArticleVersions(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}
	tenantID, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}

	versions, err := h.svc.ListVersions(c.Request.Context(), id, tenantID.(int))
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}

	// 契约：前端期望 camelCase ArticleVersion[]
	items := make([]map[string]interface{}, 0, len(versions))
	for _, v := range versions {
		items = append(items, map[string]interface{}{
			"version":       v.Version,
			"content":       v.Content,
			"changeLog":     v.ChangeSummary,
			"createdBy":     v.AuthorID,
			"createdByName": "",
			"createdAt":     v.CreatedAt,
		})
	}
	common.Success(c, items)
}

// RestoreArticleVersion handles POST /api/v1/knowledge/articles/:id/versions/:version/restore
func (h *Handler) RestoreArticleVersion(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version <= 0 {
		common.ParamError(c, "Invalid version")
		return
	}
	tenantID, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}
	userID, _ := c.Get("user_id")
	operatorID, _ := userID.(int)

	article, err := h.svc.RestoreVersion(c.Request.Context(), id, version, tenantID.(int), operatorID)
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}
	common.Success(c, toArticleDTO(article))
}

// CompareArticleVersions handles GET /api/v1/knowledge/articles/:id/versions/compare?from=&to=
func (h *Handler) CompareArticleVersions(c *gin.Context) {
	id, ok := common.ParsePositiveID(c, "id")
	if !ok {
		return
	}
	from, err1 := strconv.Atoi(c.Query("from"))
	to, err2 := strconv.Atoi(c.Query("to"))
	if err1 != nil || err2 != nil || from <= 0 || to <= 0 {
		common.ParamError(c, "from and to are required")
		return
	}
	tenantID, ok := c.Get("tenant_id")
	if !ok {
		common.ParamError(c, "Tenant ID not found")
		return
	}

	result, err := h.svc.CompareVersions(c.Request.Context(), id, from, to, tenantID.(int))
	if err != nil {
		common.InternalError(c, err.Error())
		return
	}
	common.Success(c, result)
}
