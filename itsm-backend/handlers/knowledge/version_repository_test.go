package knowledge

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
