package knowledge

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/dto"
)

type Service struct {
	repo   Repository
	logger *zap.SugaredLogger
}

func NewService(repo Repository, logger *zap.SugaredLogger) *Service {
	return &Service{
		repo:   repo,
		logger: logger,
	}
}

func (s *Service) CreateArticle(ctx context.Context, a *Article) (*Article, error) {
	// XSS 消毒：Title 走 strict（纯文本），Content 走 UGC（保留富文本白名单，剥离 script/on*/javascript:）
	a.Title = common.SanitizeText(a.Title)
	a.Content = common.SanitizeHTML(a.Content)
	s.logger.Infow("Creating Knowledge Article", "title", a.Title, "category", a.Category)
	return s.repo.Create(ctx, a)
}

func (s *Service) GetArticle(ctx context.Context, id int, tenantID int) (*Article, error) {
	return s.repo.Get(ctx, id, tenantID)
}

func (s *Service) ListArticles(ctx context.Context, tenantID int, page, size int, category, search, status string) ([]*Article, int, error) {
	return s.repo.List(ctx, tenantID, page, size, category, search, status)
}

func (s *Service) UpdateArticle(ctx context.Context, a *Article) (*Article, error) {
	// XSS 消毒
	a.Title = common.SanitizeText(a.Title)
	a.Content = common.SanitizeHTML(a.Content)
	s.logger.Infow("Updating Knowledge Article", "id", a.ID, "title", a.Title)
	return s.repo.Update(ctx, a)
}

func (s *Service) DeleteArticle(ctx context.Context, id int, tenantID int) error {
	s.logger.Infow("Deleting Knowledge Article", "id", id)
	return s.repo.Delete(ctx, id, tenantID)
}

func (s *Service) GetCategories(ctx context.Context, tenantID int) ([]string, error) {
	return s.repo.GetCategories(ctx, tenantID)
}

func (s *Service) GetStats(ctx context.Context, tenantID int) (*dto.KnowledgeStatsResponse, error) {
	stats, err := s.repo.GetStats(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Calculate average rating based on total likes / total articles
	// Since we only have likes (not a 1-5 star rating), we'll use likes as a proxy for rating
	var avgRating float64
	if stats.Total > 0 {
		avgRating = float64(stats.TotalLikes) / float64(stats.Total)
	}

	// Convert categories to DTO format
	categoryStats := make([]dto.CategoryStats, 0, len(stats.Categories))
	for _, cat := range stats.Categories {
		categoryStats = append(categoryStats, dto.CategoryStats{
			Name:  cat.Name,
			Count: int(cat.Count),
		})
	}

	return &dto.KnowledgeStatsResponse{
		Total:      int(stats.Total),
		Published:  int(stats.Published),
		Draft:      int(stats.Draft),
		Views:      stats.TotalViews,
		Rating:     avgRating,
		Categories: categoryStats,
	}, nil
}

// ==================== 版本控制 ====================

// ListVersions 列出文章版本历史
func (s *Service) ListVersions(ctx context.Context, articleID int, tenantID int) ([]*ArticleVersion, error) {
	return s.repo.ListVersions(ctx, articleID, tenantID)
}

// RestoreVersion 回滚文章到指定版本（生成新版本快照，版本号递增）
func (s *Service) RestoreVersion(ctx context.Context, articleID int, version int, tenantID int, operatorID int) (*Article, error) {
	snapshot, err := s.repo.GetVersion(ctx, articleID, version, tenantID)
	if err != nil {
		return nil, err
	}

	current, err := s.repo.Get(ctx, articleID, tenantID)
	if err != nil {
		return nil, err
	}

	// 应用快照内容
	current.Title = snapshot.Title
	current.Content = snapshot.Content
	current.Category = snapshot.Category
	if snapshot.Tags != "" {
		current.Tags = []string{snapshot.Tags}
	}
	current.AuthorID = operatorID
	updated, err := s.repo.Update(ctx, current)
	if err != nil {
		return nil, err
	}

	// 回滚动作本身也产生新版本记录
	next, err := s.repo.NextVersion(ctx, articleID, tenantID)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.SnapshotVersion(ctx, &ArticleVersion{
		ArticleID:     articleID,
		Version:       next,
		Title:         snapshot.Title,
		Content:       snapshot.Content,
		Category:      snapshot.Category,
		Tags:          snapshot.Tags,
		AuthorID:      operatorID,
		ChangeSummary: fmt.Sprintf("回滚到版本 %d", version),
		TenantID:      tenantID,
	}); err != nil {
		s.logger.Warnw("failed to snapshot rollback version", "error", err, "article_id", articleID)
	}

	s.logger.Infow("Restored knowledge article version", "id", articleID, "from_version", version, "to_version", next)
	return updated, nil
}

// CompareVersions 对比两个版本的内容差异（简单行级 diff）
func (s *Service) CompareVersions(ctx context.Context, articleID int, from, to int, tenantID int) (map[string]interface{}, error) {
	fromV, err := s.repo.GetVersion(ctx, articleID, from, tenantID)
	if err != nil {
		return nil, err
	}
	toV, err := s.repo.GetVersion(ctx, articleID, to, tenantID)
	if err != nil {
		return nil, err
	}

	fromLines := strings.Split(fromV.Content, "\n")
	toLines := strings.Split(toV.Content, "\n")

	// 简单公共前后缀裁剪生成 diff 概览
	changes := []map[string]string{}
	commonPrefix := 0
	for commonPrefix < len(fromLines) && commonPrefix < len(toLines) && fromLines[commonPrefix] == toLines[commonPrefix] {
		commonPrefix++
	}
	fromSuffix, toSuffix := len(fromLines), len(toLines)
	for fromSuffix > commonPrefix && toSuffix > commonPrefix && fromLines[fromSuffix-1] == toLines[toSuffix-1] {
		fromSuffix--
		toSuffix--
	}
	removed := strings.Join(fromLines[commonPrefix:fromSuffix], "\n")
	added := strings.Join(toLines[commonPrefix:toSuffix], "\n")
	if removed != "" {
		changes = append(changes, map[string]string{"type": "removed", "content": removed})
	}
	if added != "" {
		changes = append(changes, map[string]string{"type": "added", "content": added})
	}

	return map[string]interface{}{
		"diff":    fmt.Sprintf("-版本 %d 特有内容:\n%s\n\n+版本 %d 特有内容:\n%s", from, removed, to, added),
		"changes": changes,
	}, nil
}

// NextVersion 返回下一版本号
func (s *Service) NextVersion(ctx context.Context, articleID int, tenantID int) (int, error) {
	return s.repo.NextVersion(ctx, articleID, tenantID)
}

// SnapshotVersion 保存版本快照
func (s *Service) SnapshotVersion(ctx context.Context, v *ArticleVersion) (*ArticleVersion, error) {
	return s.repo.SnapshotVersion(ctx, v)
}
