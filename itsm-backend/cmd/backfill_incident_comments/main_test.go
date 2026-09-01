package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留。
var testDBCounter int64

func testDSN() string {
	return fmt.Sprintf("file:backfill_incident_comments_test_%d?mode=memory&cache=shared&_fk=1", atomic.AddInt64(&testDBCounter, 1))
}

func setupTenantAndUser(t *testing.T, client *ent.Client, ctx context.Context, code string) (*ent.Tenant, *ent.User) {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("T-" + code).SetCode(code).SetDomain(code + ".example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("reporter-" + code).SetEmail("reporter-" + code + "@example.com").
		SetName("Reporter").SetPasswordHash("hash").SetRole("end_user").
		SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant, user
}

// setupIncidentWithWorkItem 创建一条满足 WorkItem 创建不变量的 Incident。
func setupIncidentWithWorkItem(t *testing.T, client *ent.Client, ctx context.Context, tenantID, userID int, code string) *ent.Incident {
	t.Helper()
	wi, err := client.Ticket.Create().
		SetTitle("WI-" + code).SetTicketNumber("TKT-" + code).
		SetRecordClass("incident").SetRequesterID(userID).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	inc, err := client.Incident.Create().SetIncidentNumber("INC-" + code).
		SetWorkItemID(wi.ID).
		Save(ctx)
	require.NoError(t, err)
	return inc
}

func createCommentEvent(t *testing.T, client *ent.Client, ctx context.Context, inc *ent.Incident, userID int, content string, createdAt time.Time) *ent.IncidentEvent {
	t.Helper()
	workItem, err := client.Ticket.Get(ctx, inc.WorkItemID)
	require.NoError(t, err)
	event, err := client.IncidentEvent.Create().
		SetIncidentID(inc.ID).
		SetEventType("comment").
		SetEventName("用户评论").
		SetDescription(content).
		SetUserID(userID).
		SetSource("user").
		SetTenantID(workItem.TenantID).
		SetOccurredAt(createdAt).
		SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)
	return event
}

func TestResolvePlan_HappyPath(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "happy")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "happy")
	createdAt := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	event := createCommentEvent(t, client, ctx, inc, user.ID, "磁盘快满了", createdAt)

	plan, ok, reason, err := resolvePlan(ctx, client, event)
	require.NoError(t, err)
	require.True(t, ok, "reason: %s", reason)
	require.Equal(t, inc.WorkItemID, plan.ticketID)
	require.Equal(t, user.ID, plan.userID)
	require.Equal(t, "磁盘快满了", plan.content)
	require.Equal(t, tenant.ID, plan.tenantID)
	require.True(t, createdAt.Equal(plan.createdAt))
}

func TestResolvePlan_SkipsWhenIncidentSoftDeleted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "del")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "del")
	event := createCommentEvent(t, client, ctx, inc, user.ID, "评论内容", time.Now())
	_, err := client.Ticket.UpdateOneID(inc.WorkItemID).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)

	_, ok, reason, err := resolvePlan(ctx, client, event)
	require.NoError(t, err)
	require.False(t, ok)
	require.NotEmpty(t, reason)
}

func TestResolvePlan_SkipsWhenUserIDMissing(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "nouser")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "nouser")
	event := createCommentEvent(t, client, ctx, inc, 0, "系统生成的占位评论", time.Now())

	_, ok, reason, err := resolvePlan(ctx, client, event)
	require.NoError(t, err)
	require.False(t, ok)
	require.NotEmpty(t, reason)
}

func TestResolvePlan_SkipsWhenContentEmpty(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "nocontent")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "nocontent")
	event := createCommentEvent(t, client, ctx, inc, user.ID, "", time.Now())

	_, ok, reason, err := resolvePlan(ctx, client, event)
	require.NoError(t, err)
	require.False(t, ok)
	require.NotEmpty(t, reason)
}

func TestFindCandidates_OnlyCommentEvents(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "evtype")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "evtype")
	comment := createCommentEvent(t, client, ctx, inc, user.ID, "这是评论", time.Now())
	_, err := client.IncidentEvent.Create().
		SetIncidentID(inc.ID).SetEventType("status_change").SetEventName("状态变更").
		SetUserID(user.ID).SetTenantID(tenant.ID).SetOccurredAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	candidates, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, comment.ID, candidates[0].ID)
}

func TestFindCandidates_TenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenantA, userA := setupTenantAndUser(t, client, ctx, "ceta")
	tenantB, userB := setupTenantAndUser(t, client, ctx, "cetb")
	incA := setupIncidentWithWorkItem(t, client, ctx, tenantA.ID, userA.ID, "ceta")
	incB := setupIncidentWithWorkItem(t, client, ctx, tenantB.ID, userB.ID, "cetb")
	createCommentEvent(t, client, ctx, incA, userA.ID, "A租户评论", time.Now())
	createCommentEvent(t, client, ctx, incB, userB.ID, "B租户评论", time.Now())

	scoped, err := findCandidates(ctx, client, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, scoped, 1)

	all, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	require.Len(t, all, 2, "tenant-id<=0 时处理所有租户")
}

func TestBackfillOne_CreatesTicketComment(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "create")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "create")
	createdAt := time.Date(2026, 2, 1, 8, 30, 0, 0, time.UTC)
	event := createCommentEvent(t, client, ctx, inc, user.ID, "根因是磁盘满", createdAt)

	result, reason, err := backfillOne(ctx, client, event)
	require.NoError(t, err)
	require.Equal(t, outcomeCreated, result, "reason: %s", reason)

	rows, err := client.TicketComment.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, inc.WorkItemID, rows[0].TicketID)
	require.Equal(t, user.ID, rows[0].UserID)
	require.Equal(t, "根因是磁盘满", rows[0].Content)
	require.False(t, rows[0].IsInternal)
	require.Equal(t, tenant.ID, rows[0].TenantID)
	require.True(t, createdAt.Equal(rows[0].CreatedAt))
}

// TestBackfillOne_Idempotent 验证重复运行不产生第二条 ticket_comments；这里的
// "已经回填过"是预期中的正常重跑场景，应该静默 skip，不是 error。
func TestBackfillOne_Idempotent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "idem")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "idem")
	event := createCommentEvent(t, client, ctx, inc, user.ID, "重复运行测试", time.Now())

	first, _, err := backfillOne(ctx, client, event)
	require.NoError(t, err)
	require.Equal(t, outcomeCreated, first)

	second, reason, err := backfillOne(ctx, client, event)
	require.NoError(t, err, "重复运行必须是幂等的 skip，不是 error")
	require.Equal(t, outcomeSkipped, second, "reason: %s", reason)

	count, err := client.TicketComment.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count, "不应该产生第二条 ticket_comments")
}

func TestPreviewBackfill_CountsWithoutWriting(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "preview")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "preview")
	eligible := createCommentEvent(t, client, ctx, inc, user.ID, "会被回填", time.Now())
	ineligible := createCommentEvent(t, client, ctx, inc, 0, "user_id缺失会被跳过", time.Now())

	wouldCreate, skipReasons, failed, err := previewBackfill(ctx, client, []*ent.IncidentEvent{eligible, ineligible})
	require.NoError(t, err)
	require.Equal(t, 1, wouldCreate)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, len(skipReasons), "只有一种跳过原因")
	require.Equal(t, 1, skipReasons["评论事件缺少可归属的 user_id"])

	count, err := client.TicketComment.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count, "dry-run 预览不能实际写入")
}
