package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/knowledgearticle"
	"itsm-backend/ent/knowledgearticleversion"
)

// defaultCategories provides baseline knowledge-base categories so the category
// dropdown is never empty for a freshly provisioned tenant. The list is always
// merged with whatever distinct categories already exist in the article table,
// so adding more categories later does not lose them.
var defaultCategories = []string{
	"故障排查",
	"操作指南",
	"最佳实践",
	"变更发布",
	"安全合规",
	"常见问题",
	"工单模板",
	"流程说明",
}

type EntRepository struct {
	client *ent.Client
}

func NewEntRepository(client *ent.Client) *EntRepository {
	return &EntRepository{client: client}
}

// toDomain maps ent KnowledgeArticle to domain Article
func toDomain(e *ent.KnowledgeArticle) *Article {
	if e == nil {
		return nil
	}
	tags := []string{}
	if e.Tags != "" {
		tags = strings.Split(e.Tags, ",")
	}
	return &Article{
		ID:            e.ID,
		Title:         e.Title,
		Content:       e.Content,
		Category:      e.Category,
		Tags:          tags,
		AuthorID:      e.AuthorID,
		TenantID:      e.TenantID,
		IsPublished:   e.IsPublished,
		ReviewStatus:  e.ReviewStatus,
		ReviewComment: e.ReviewComment,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
	}
}

func (r *EntRepository) Create(ctx context.Context, a *Article) (*Article, error) {
	tagsStr := strings.Join(a.Tags, ",")
	e, err := r.client.KnowledgeArticle.Create().
		SetTitle(a.Title).
		SetContent(a.Content).
		SetCategory(a.Category).
		SetTags(tagsStr).
		SetAuthorID(a.AuthorID).
		SetTenantID(a.TenantID).
		SetIsPublished(a.IsPublished).
		SetReviewStatus(a.ReviewStatus).
		SetReviewComment(a.ReviewComment).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toDomain(e), nil
}

func (r *EntRepository) Get(ctx context.Context, id int, tenantID int) (*Article, error) {
	e, err := r.client.KnowledgeArticle.Query().
		Where(knowledgearticle.ID(id), knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil()).
		First(ctx)
	if err != nil {
		return nil, err
	}
	return toDomain(e), nil
}

func (r *EntRepository) List(ctx context.Context, tenantID int, page, size int, category, search, status string) ([]*Article, int, error) {
	q := r.client.KnowledgeArticle.Query().Where(knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil())

	if category != "" {
		q = q.Where(knowledgearticle.Category(category))
	}
	if search != "" {
		q = q.Where(
			knowledgearticle.Or(
				knowledgearticle.TitleContains(search),
				knowledgearticle.ContentContains(search),
			),
		)
	}
	if status != "" {
		if strings.ToLower(status) == "published" {
			q = q.Where(knowledgearticle.IsPublished(true))
		} else if strings.ToLower(status) == "draft" {
			q = q.Where(knowledgearticle.IsPublished(false))
		}
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	es, err := q.Limit(size).Offset((page - 1) * size).Order(ent.Desc(knowledgearticle.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, 0, err
	}

	var results []*Article
	for _, e := range es {
		results = append(results, toDomain(e))
	}
	return results, total, nil
}

func (r *EntRepository) Update(ctx context.Context, a *Article) (*Article, error) {
	tagsStr := strings.Join(a.Tags, ",")
	e, err := r.client.KnowledgeArticle.UpdateOneID(a.ID).
		Where(knowledgearticle.TenantID(a.TenantID), knowledgearticle.DeletedAtIsNil()).
		SetTitle(a.Title).
		SetContent(a.Content).
		SetCategory(a.Category).
		SetTags(tagsStr).
		SetIsPublished(a.IsPublished).
		SetReviewStatus(a.ReviewStatus).
		SetReviewComment(a.ReviewComment).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toDomain(e), nil
}

func (r *EntRepository) Delete(ctx context.Context, id int, tenantID int) error {
	_, err := r.client.KnowledgeArticle.Update().
		Where(knowledgearticle.ID(id), knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil()).
		SetDeletedAt(time.Now()).
		Save(ctx)
	return err
}

func (r *EntRepository) GetCategories(ctx context.Context, tenantID int) ([]string, error) {
	existing, err := r.client.KnowledgeArticle.Query().
		Where(knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil()).
		GroupBy(knowledgearticle.FieldCategory).
		Strings(ctx)
	if err != nil {
		return nil, err
	}

	// Merge defaults with whatever already exists, then dedupe and sort.
	seen := make(map[string]struct{}, len(existing)+len(defaultCategories))
	merged := make([]string, 0, len(existing)+len(defaultCategories))
	for _, c := range defaultCategories {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		merged = append(merged, c)
	}
	for _, c := range existing {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		merged = append(merged, c)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i] < merged[j] })
	return merged, nil
}

func (r *EntRepository) GetStats(ctx context.Context, tenantID int) (*Stats, error) {
	// Query all articles for this tenant
	query := r.client.KnowledgeArticle.Query().Where(knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil())

	// Get total count
	total, err := query.Count(ctx)
	if err != nil {
		return nil, err
	}

	// Get published count
	published, err := query.Clone().Where(knowledgearticle.IsPublished(true)).Count(ctx)
	if err != nil {
		return nil, err
	}

	// Draft count
	draft, err := query.Clone().Where(knowledgearticle.IsPublished(false)).Count(ctx)
	if err != nil {
		return nil, err
	}

	// Sum of view counts
	type ViewSum struct {
		TotalViews int `json:"total_views"` // matches ent aggregate alias for Scan()
	}
	var viewResult []ViewSum
	err = r.client.KnowledgeArticle.Query().
		Where(knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil()).
		Aggregate(ent.As(ent.Sum(knowledgearticle.FieldViewCount), "total_views")).
		Scan(ctx, &viewResult)
	if err != nil {
		return nil, err
	}
	totalViews := int64(0)
	if len(viewResult) > 0 {
		totalViews = int64(viewResult[0].TotalViews)
	}

	// Sum of like counts
	type LikeSum struct {
		TotalLikes int `json:"total_likes"` // matches ent aggregate alias for Scan()
	}
	var likeResult []LikeSum
	err = r.client.KnowledgeArticle.Query().
		Where(knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil()).
		Aggregate(ent.As(ent.Sum(knowledgearticle.FieldLikeCount), "total_likes")).
		Scan(ctx, &likeResult)
	if err != nil {
		return nil, err
	}
	totalLikes := int64(0)
	if len(likeResult) > 0 {
		totalLikes = int64(likeResult[0].TotalLikes)
	}

	// Get category distribution
	type CategoryGroup struct {
		Category string `json:"category"`
		Count    int    `json:"count"`
	}
	var categoryResults []CategoryGroup
	err = r.client.KnowledgeArticle.Query().
		Where(knowledgearticle.TenantID(tenantID), knowledgearticle.DeletedAtIsNil()).
		GroupBy(knowledgearticle.FieldCategory).
		Aggregate(ent.Count()).
		Scan(ctx, &categoryResults)
	if err != nil {
		return nil, err
	}

	categories := make([]CategoryStat, 0, len(categoryResults))
	for _, cr := range categoryResults {
		categories = append(categories, CategoryStat{
			Name:  cr.Category,
			Count: int64(cr.Count),
		})
	}

	return &Stats{
		Total:      int64(total),
		Published:  int64(published),
		Draft:      int64(draft),
		TotalViews: totalViews,
		TotalLikes: totalLikes,
		Categories: categories,
	}, nil
}

// ==================== 版本控制 ====================
// 租户隔离策略：knowledge_article_versions 表无 tenant_id 列（版本快照随文章继承租户）。
// 所有版本操作先通过 verifyArticleOwnership 校验文章归属租户，再按 article_id 操作——join-guard 模式。

// verifyArticleOwnership 校验文章归属租户（租户隔离守卫）
func (r *EntRepository) verifyArticleOwnership(ctx context.Context, articleID int, tenantID int) error {
	exists, err := r.client.KnowledgeArticle.Query().
		Where(knowledgearticle.IDEQ(articleID), knowledgearticle.TenantIDEQ(tenantID)).
		Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("article not found in tenant")
	}
	return nil
}

// NextVersion 返回该文章下一个版本号（无历史时为 1）
func (r *EntRepository) NextVersion(ctx context.Context, articleID int, tenantID int) (int, error) {
	if err := r.verifyArticleOwnership(ctx, articleID, tenantID); err != nil {
		return 0, err
	}
	count, err := r.client.KnowledgeArticleVersion.Query().
		Where(knowledgearticleversion.ArticleIDEQ(articleID)).
		Count(ctx)
	if err != nil {
		return 0, err
	}
	return count + 1, nil
}

// SnapshotVersion 保存文章版本快照
func (r *EntRepository) SnapshotVersion(ctx context.Context, v *ArticleVersion) (*ArticleVersion, error) {
	if err := r.verifyArticleOwnership(ctx, v.ArticleID, v.TenantID); err != nil {
		return nil, err
	}
	e, err := r.client.KnowledgeArticleVersion.Create().
		SetArticleID(v.ArticleID).
		SetVersion(v.Version).
		SetTitle(v.Title).
		SetContent(v.Content).
		SetCategory(v.Category).
		SetTags(v.Tags).
		SetAuthorID(v.AuthorID).
		SetChangeSummary(v.ChangeSummary).
		SetCreatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	v.ID = e.ID
	v.CreatedAt = e.CreatedAt
	return v, nil
}

// ListVersions 列出文章所有版本（新→旧）
func (r *EntRepository) ListVersions(ctx context.Context, articleID int, tenantID int) ([]*ArticleVersion, error) {
	if err := r.verifyArticleOwnership(ctx, articleID, tenantID); err != nil {
		return nil, err
	}
	versions, err := r.client.KnowledgeArticleVersion.Query().
		Where(knowledgearticleversion.ArticleIDEQ(articleID)).
		Order(ent.Desc(knowledgearticleversion.FieldVersion)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ArticleVersion, 0, len(versions))
	for _, e := range versions {
		out = append(out, toVersionDomain(e))
	}
	return out, nil
}

// GetVersion 获取指定版本快照
func (r *EntRepository) GetVersion(ctx context.Context, articleID int, version int, tenantID int) (*ArticleVersion, error) {
	if err := r.verifyArticleOwnership(ctx, articleID, tenantID); err != nil {
		return nil, err
	}
	e, err := r.client.KnowledgeArticleVersion.Query().
		Where(
			knowledgearticleversion.ArticleIDEQ(articleID),
			knowledgearticleversion.VersionEQ(version),
		).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return toVersionDomain(e), nil
}

// toVersionDomain maps ent KnowledgeArticleVersion to domain ArticleVersion
func toVersionDomain(e *ent.KnowledgeArticleVersion) *ArticleVersion {
	if e == nil {
		return nil
	}
	return &ArticleVersion{
		ID:            e.ID,
		ArticleID:     e.ArticleID,
		Version:       e.Version,
		Title:         e.Title,
		Content:       e.Content,
		Category:      e.Category,
		Tags:          e.Tags,
		AuthorID:      e.AuthorID,
		ChangeSummary: e.ChangeSummary,
		CreatedAt:     e.CreatedAt,
	}
}
