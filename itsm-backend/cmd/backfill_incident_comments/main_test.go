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

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留
// （与 cmd/backfill_incident_work_item/main_test.go 同一做法）。
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

// setupIncidentWithWorkItem 创建一条已经跑过 backfill_incident_work_item 的 Incident
// （即 work_item_id 非空），是本工具大多数测试用例的正常前置状态。
func setupIncidentWithWorkItem(t *testing.T, client *ent.Client, ctx context.Context, tenantID, userID int, code string) *ent.Incident {
	t.Helper()
	wi, err := client.Ticket.Create().
		SetTitle("WI-" + code).SetTicketNumber("TKT-" + code).
		SetRecordClass("incident").SetRequesterID(userID).SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	inc, err := client.Incident.Create().
		SetTitle("INC-" + code).SetIncidentNumber("INC-" + code).
		SetReporterID(userID).SetTenantID(tenantID).SetWorkItemID(wi.ID).
		Save(ctx)
	require.NoError(t, err)
	return inc
}

func createCommentEvent(t *testing.T, client *ent.Client, ctx context.Context, inc *ent.Incident, userID int, content string, createdAt time.Time) *ent.IncidentEvent {
	t.Helper()
	event, err := client.IncidentEvent.Create().
		SetIncidentID(inc.ID).
		SetEventType("comment").
		SetEventName("用户评论").
		SetDescription(content).
		SetUserID(userID).
		SetSource("user").
		SetTenantID(inc.TenantID).
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

func TestResolvePlan_SkipsWhenWorkItemIDMissing(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "nowi")
	inc, err := client.Incident.Create().
		SetTitle("未回填WorkItem的事件").SetIncidentNumber("INC-NOWI-1").
		SetReporterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	event := createCommentEvent(t, client, ctx, inc, user.ID, "评论内容", time.Now())

	_, ok, reason, err := resolvePlan(ctx, client, event)
	require.NoError(t, err)
	require.False(t, ok)
	require.NotEmpty(t, reason)
}

func TestResolvePlan_SkipsWhenIncidentSoftDeleted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "del")
	inc := setupIncidentWithWorkItem(t, client, ctx, tenant.ID, user.ID, "del")
	event := createCommentEvent(t, client, ctx, inc, user.ID, "评论内容", time.Now())
	_, err := client.Incident.UpdateOneID(inc.ID).SetDeletedAt(time.Now()).Save(ctx)
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
