package knowledge

import (
	"context"
	"time"
)

// Repository interface for Knowledge domain
type Repository interface {
	Create(ctx context.Context, a *Article) (*Article, error)
	Get(ctx context.Context, id int, tenantID int) (*Article, error)
	List(ctx context.Context, tenantID int, page, size int, category, search, status string) ([]*Article, int, error)
	Update(ctx context.Context, a *Article) (*Article, error)
	Delete(ctx context.Context, id int, tenantID int) error
	GetCategories(ctx context.Context, tenantID int) ([]string, error)
	GetStats(ctx context.Context, tenantID int) (*Stats, error)
	// 版本控制
	NextVersion(ctx context.Context, articleID int, tenantID int) (int, error)
	SnapshotVersion(ctx context.Context, v *ArticleVersion) (*ArticleVersion, error)
	ListVersions(ctx context.Context, articleID int, tenantID int) ([]*ArticleVersion, error)
	GetVersion(ctx context.Context, articleID int, version int, tenantID int) (*ArticleVersion, error)
}

// ArticleVersion represents a knowledge article version snapshot
type ArticleVersion struct {
	ID            int
	ArticleID     int
	Version       int
	Title         string
	Content       string
	Category      string
	Tags          string
	AuthorID      int
	ChangeSummary string
	TenantID      int
	CreatedAt     time.Time
}

// Stats represents knowledge base statistics
type Stats struct {
	Total      int64
	Published  int64
	Draft      int64
	TotalViews int64
	TotalLikes int64
	Categories []CategoryStat
}

// CategoryStat represents category statistics
type CategoryStat struct {
	Name  string
	Count int64
}
