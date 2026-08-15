package knowledge

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// setupVersionTest 创建测试环境：文章 + 版本仓储
func setupVersionTest(t *testing.T) (*EntRepository, *Article, context.Context) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:knowledge_version_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	article, err := client.KnowledgeArticle.Create().
		SetTitle("测试文章").
		SetContent("初始内容").
		SetCategory("测试分类").
		SetTags("tag1").
		SetAuthorID(1).
		SetTenantID(7).
		SetIsPublished(false).
		Save(ctx)
	require.NoError(t, err)

	domain := &Article{
		ID:          article.ID,
		Title:       article.Title,
		Content:     article.Content,
		Category:    article.Category,
		Tags:        []string{"tag1"},
		AuthorID:    article.AuthorID,
		TenantID:    article.TenantID,
		IsPublished: article.IsPublished,
	}

	return NewEntRepository(client), domain, ctx
}

func TestVersionFlow_SnapshotListGet(t *testing.T) {
	repo, article, ctx := setupVersionTest(t)

	// 首个版本号为 1
	next, err := repo.NextVersion(ctx, article.ID, article.TenantID)
	require.NoError(t, err)
	assert.Equal(t, 1, next)

	// 快照 v1
	_, err = repo.SnapshotVersion(ctx, &ArticleVersion{
		ArticleID:     article.ID,
		Version:       next,
		Title:         article.Title,
		Content:       article.Content,
		Category:      article.Category,
		Tags:          "tag1",
		AuthorID:      article.AuthorID,
		ChangeSummary: "初始版本",
		TenantID:      article.TenantID,
	})
	require.NoError(t, err)

	// 快照 v2
	next2, err := repo.NextVersion(ctx, article.ID, article.TenantID)
	require.NoError(t, err)
	assert.Equal(t, 2, next2)
	_, err = repo.SnapshotVersion(ctx, &ArticleVersion{
		ArticleID: article.ID, Version: next2, Title: "v2标题", Content: "v2内容",
		Category: "测试分类", Tags: "", AuthorID: article.AuthorID,
		ChangeSummary: "第二版", TenantID: article.TenantID,
	})
	require.NoError(t, err)

	// ListVersions 新→旧
	versions, err := repo.ListVersions(ctx, article.ID, article.TenantID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, 2, versions[0].Version)
	assert.Equal(t, 1, versions[1].Version)

	// GetVersion
	v1, err := repo.GetVersion(ctx, article.ID, 1, article.TenantID)
	require.NoError(t, err)
	assert.Equal(t, "初始内容", v1.Content)
}

func TestVersionFlow_CrossTenantIsolation(t *testing.T) {
	repo, article, ctx := setupVersionTest(t)

	// 其他租户访问同一文章版本 → 拒绝
	_, err := repo.ListVersions(ctx, article.ID, 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	_, err = repo.NextVersion(ctx, article.ID, 99)
	require.Error(t, err)

	_, err = repo.GetVersion(ctx, article.ID, 1, 99)
	require.Error(t, err)
}

func TestReviewFlow_SubmitAndApprove(t *testing.T) {
	repo, article, ctx := setupVersionTest(t)

	svc := &Service{repo: repo, logger: nil}
	_ = svc

	// 提交审核
	submitted, err := repo.Update(ctx, &Article{
		ID: article.ID, Title: article.Title, Content: article.Content,
		Category: article.Category, Tags: article.Tags, AuthorID: article.AuthorID,
		TenantID: article.TenantID, IsPublished: false, ReviewStatus: "under_review",
	})
	require.NoError(t, err)
	assert.Equal(t, "under_review", submitted.ReviewStatus)

	// 批准
	approved, err := repo.Update(ctx, &Article{
		ID: submitted.ID, Title: submitted.Title, Content: submitted.Content,
		Category: submitted.Category, Tags: submitted.Tags, AuthorID: submitted.AuthorID,
		TenantID: submitted.TenantID, IsPublished: true, ReviewStatus: "published",
		ReviewComment: "审核通过",
	})
	require.NoError(t, err)
	assert.True(t, approved.IsPublished)
	assert.Equal(t, "published", approved.ReviewStatus)
	assert.Equal(t, "审核通过", approved.ReviewComment)
}

func TestReviewFlow_RejectRequiresComment(t *testing.T) {
	repo, article, ctx := setupVersionTest(t)

	svc := NewService(repo, zaptest.NewLogger(t).Sugar())

	// 提交审核
	if _, err := svc.SubmitForReview(ctx, article.ID, article.TenantID); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// 拒绝无意见 → 错误
	_, err := svc.ReviewDecision(ctx, article.ID, article.TenantID, "reject", "")
	require.Error(t, err)

	// 拒绝有意见 → 回到 draft
	rejected, err := svc.ReviewDecision(ctx, article.ID, article.TenantID, "reject", "内容不足")
	require.NoError(t, err)
	assert.Equal(t, "draft", rejected.ReviewStatus)
	assert.False(t, rejected.IsPublished)
	assert.Equal(t, "内容不足", rejected.ReviewComment)
}
