# WorkItem 详情页能力对齐 · Phase 2：Incident 评论收口进 ticket_comments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move Incident comments off the standalone `incident_events` (`event_type="comment"`)
storage and onto the shared `ticket_comments` table (keyed by `incident.work_item_id`), then retire
the now-redundant Incident-specific comment backend/frontend code — closing the "Incident 评论走
独立 incident_events 表" gap called out in the design doc's capability matrix (§2.1).

**Architecture:** A one-off backfill CLI tool copies historical comment events into
`ticket_comments`, dedup-checked by `(ticket_id, user_id, content, created_at)` so it's safe to
re-run. The frontend's Incident comment UI (currently a page-level `CommentPanel` +
`incidentCommentAdapter`) switches to `ticketCommentAdapter` + `incident.workItemId`, matching how
Problem/Change will eventually consume the same shared storage. Only once the backfill has run
against the target environment and the frontend has switched over does the plan retire
`GetIncidentComments`/`CreateIncidentComment`, their routes, their DTOs, and the now-dead
`incident-comment-adapter.ts` — retiring any earlier would make the comment UI go blank mid-migration.

**Tech Stack:** Go, Ent ORM, `stretchr/testify`, SQLite via `ent/enttest` (backend); Next.js/React,
Jest (frontend, if adapter-level tests are added — none exist for
`ticket-comment-adapter.ts`/`incident-comment-adapter.ts` today, so no adapter unit test is
required here, only the page-level wiring change and its type-check).

**Spec:** `docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md` §4.2, §5.1

## Global Constraints

- Order matters and must not be reversed (spec §4.2 point 3): backfill code lands and is run
  against the target DB **before** the frontend switches adapters, and the frontend switch lands
  **before** the old backend endpoints/DTOs/adapter are deleted. Task numbering in this plan follows
  that order — do not skip ahead.
- `ticket_comments.user_id` is `Positive()` (required, must be > 0) and `.content` is `NotEmpty()`
  (`ent/schema/ticket_comment.go`) — legacy `incident_events` rows with `user_id <= 0` or an empty
  `description` cannot be mapped to a valid `ticket_comments` row and must be skipped, not
  defaulted or faked.
- `is_internal`/`mentions` are written as defaults (`false` / empty) for every backfilled row, never
  parsed out of `incident_events.data` — per spec §4.2 point 1, legacy data in that JSON blob isn't
  trustworthy enough to carry forward.
- Idempotency is achieved by checking `(ticket_id, user_id, content, created_at)` before insert, not
  by a schema change to either table (spec §4.2 point 1) — do not add new columns.
- Only comment events whose Incident already has a non-zero `work_item_id` are eligible — an
  Incident that hasn't been through `cmd/backfill_incident_work_item` yet is skipped, not errored.

---

## Task 1: `resolvePlan` — pure decision logic for one comment event

**Files:**
- Create: `itsm-backend/cmd/backfill_incident_comments/main.go`
- Test: `itsm-backend/cmd/backfill_incident_comments/main_test.go`

**Interfaces:**
- Produces: `resolvePlan(ctx context.Context, client *ent.Client, event *ent.IncidentEvent)
  (plan commentPlan, ok bool, reason string, err error)` and the `commentPlan` struct
  (`ticketID, userID int; content string; tenantID int; createdAt time.Time`) — consumed by Task 2's
  `backfillOne`/`previewBackfill`.
- Consumes: `client.Incident.Get(ctx, id) (*ent.Incident, error)` (existing ent client method).

- [ ] **Step 1: Write the failing test**

Create `itsm-backend/cmd/backfill_incident_comments/main_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./cmd/backfill_incident_comments/... -v`
Expected: FAIL (build error) — `resolvePlan`, `commentPlan` undefined, package `main` has no
non-test files yet.

- [ ] **Step 3: Write minimal implementation**

Create `itsm-backend/cmd/backfill_incident_comments/main.go`:

```go
// backfill_incident_comments 是 WorkItem 详情页能力对齐设计（docs/superpowers/specs/
// 2026-08-28-work-item-detail-page-parity-design.md §4.2）用的一次性迁移工具。
//
// 背景：Incident 评论历史上存在 incident_events 表里（event_type="comment"），Problem/Change/
// Ticket 的评论统一存在 ticket_comments 表里。本工具把 Incident 的存量评论事件按
// incident.work_item_id 写入 ticket_comments，为前端切到统一的 ticketCommentAdapter 做数据
// 准备。跑完、前端切换完成后，controller/incident_controller.go 的 GetIncidentComments/
// CreateIncidentComment 及对应路由才能安全退役——见同一设计文档 §4.2 第 3 步。
//
// 不处理的事：
//   - user_id 缺失（<=0）或 content 为空的历史评论事件会被跳过并计入 skipped，不会报错——
//     ticket_comments.user_id 是 Positive() 必填、content 是 NotEmpty() 必填，旧数据里这两种
//     不合规的行本来就无法映射成一条合法的 ticket_comments。
//   - incident.work_item_id 仍为空（还没跑过 cmd/backfill_incident_work_item）的 Incident 下的
//     评论事件会被跳过并计入 skipped——本工具不负责创建 WorkItem，只负责搬评论。
//   - 所属 Incident 已软删除的评论事件会被跳过。
//   - is_internal/mentions 一律用默认值（false / 空），不从 incident_events.data 里解析——
//     旧数据这两个字段不可靠，见设计文档 §4.2 第 1 步。
//
// 用法：
//
//	go run ./cmd/backfill_incident_comments -dry-run=true               # 预览，不写入
//	go run ./cmd/backfill_incident_comments -dry-run=false              # 全部租户实际回填
//	go run ./cmd/backfill_incident_comments -dry-run=false -tenant-id=3 # 只处理指定租户
package main

import (
	"context"
	"time"

	"itsm-backend/ent"
)

// commentPlan 是 resolvePlan 对一条 incident_events 评论事件算出的、待写入 ticket_comments
// 的字段集合。
type commentPlan struct {
	ticketID  int
	userID    int
	content   string
	tenantID  int
	createdAt time.Time
}

// resolvePlan 决定一条 incident_events 评论事件应该生成一条 ticket_comments 写入计划
// （ok=true），还是因为不满足写入条件而跳过（ok=false，reason 给出原因）。纯决策逻辑，
// 除了查一次所属 Incident 之外不做任何写入——backfillOne 和 previewBackfill 共用它，
// 保证"预览会跳过的行"和"实际会跳过的行"是同一套判断，不会各写一份分叉的逻辑。
func resolvePlan(ctx context.Context, client *ent.Client, event *ent.IncidentEvent) (plan commentPlan, ok bool, reason string, err error) {
	inc, err := client.Incident.Get(ctx, event.IncidentID)
	if err != nil {
		return commentPlan{}, false, "", err
	}
	if inc.DeletedAt != nil {
		return commentPlan{}, false, "所属 incident 已被软删除", nil
	}
	if inc.WorkItemID == 0 {
		return commentPlan{}, false, "incident 尚未回填 work_item_id（先跑 cmd/backfill_incident_work_item）", nil
	}
	if event.UserID <= 0 {
		return commentPlan{}, false, "评论事件缺少可归属的 user_id", nil
	}
	if event.Description == "" {
		return commentPlan{}, false, "评论事件 description 为空", nil
	}
	return commentPlan{
		ticketID:  inc.WorkItemID,
		userID:    event.UserID,
		content:   event.Description,
		tenantID:  event.TenantID,
		createdAt: event.CreatedAt,
	}, true, "", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./cmd/backfill_incident_comments/... -v`
Expected: PASS (5 tests: `TestResolvePlan_HappyPath`,
`TestResolvePlan_SkipsWhenWorkItemIDMissing`, `TestResolvePlan_SkipsWhenIncidentSoftDeleted`,
`TestResolvePlan_SkipsWhenUserIDMissing`, `TestResolvePlan_SkipsWhenContentEmpty`)

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add cmd/backfill_incident_comments/main.go cmd/backfill_incident_comments/main_test.go
git commit -m "feat(migration): add resolvePlan decision logic for incident comment backfill"
```

---

## Task 2: `findCandidates`, `alreadyMigrated`, `backfillOne`, `previewBackfill`, and `main()`

**Files:**
- Modify: `itsm-backend/cmd/backfill_incident_comments/main.go`
- Modify: `itsm-backend/cmd/backfill_incident_comments/main_test.go`

**Interfaces:**
- Consumes: `resolvePlan` (Task 1); ent predicates `incidentevent.EventType`, `incidentevent.TenantID`
  from `itsm-backend/ent/incidentevent`; `ticketcomment.TicketID/UserID/Content/CreatedAt` from
  `itsm-backend/ent/ticketcomment`.
- Produces: `findCandidates(ctx, client, tenantID int) ([]*ent.IncidentEvent, error)`,
  `alreadyMigrated(ctx, client, plan commentPlan) (bool, error)`,
  `backfillOne(ctx, client, event *ent.IncidentEvent) (outcome, string, error)` where `outcome` is
  `outcomeCreated` or `outcomeSkipped`, `previewBackfill(ctx, client, events []*ent.IncidentEvent)
  (wouldCreate, wouldSkip int, err error)`.

- [ ] **Step 1: Write the failing tests**

Append to `itsm-backend/cmd/backfill_incident_comments/main_test.go`:

```go
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

func TestBackfillOne_SkippedEventProducesNoRow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "skip")
	inc, err := client.Incident.Create().
		SetTitle("未回填WorkItem").SetIncidentNumber("INC-SKIP-1").
		SetReporterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	event := createCommentEvent(t, client, ctx, inc, user.ID, "评论内容", time.Now())

	result, reason, err := backfillOne(ctx, client, event)
	require.NoError(t, err)
	require.Equal(t, outcomeSkipped, result)
	require.NotEmpty(t, reason)

	count, err := client.TicketComment.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

// TestBackfillOne_Idempotent 验证重复运行不产生第二条 ticket_comments：跟
// backfill_incident_work_item 的并发写入冲突（返回 error）不同，这里的"已经回填过"
// 是预期中的正常重跑场景，应该静默 skip，不是 error。
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

	wouldCreate, wouldSkip, err := previewBackfill(ctx, client, []*ent.IncidentEvent{eligible, ineligible})
	require.NoError(t, err)
	require.Equal(t, 1, wouldCreate)
	require.Equal(t, 1, wouldSkip)

	count, err := client.TicketComment.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count, "dry-run 预览不能实际写入")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd itsm-backend && go test ./cmd/backfill_incident_comments/... -v`
Expected: FAIL (build error) — `findCandidates`, `backfillOne`, `outcomeCreated`, `outcomeSkipped`,
`previewBackfill` undefined.

- [ ] **Step 3: Write minimal implementation**

Append to `itsm-backend/cmd/backfill_incident_comments/main.go` (extend the import block with
`"flag"`, `"fmt"`, `"os"`, `"itsm-backend/common/tenantctx"`, `"itsm-backend/config"`,
`"itsm-backend/database"`, `"itsm-backend/ent/incidentevent"`, `"itsm-backend/ent/ticketcomment"`,
`"go.uber.org/zap"`):

```go
import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/ticketcomment"

	"go.uber.org/zap"
)

// outcome 是 backfillOne 处理一条评论事件的结果。
type outcome int

const (
	outcomeCreated outcome = iota
	outcomeSkipped
)

// findCandidates 返回所有 event_type="comment" 的 IncidentEvent（可选按租户收窄）。是否真的
// 写入由 resolvePlan/backfillOne 逐条判断——这里只做粗筛，不查所属 Incident。
func findCandidates(ctx context.Context, client *ent.Client, tenantID int) ([]*ent.IncidentEvent, error) {
	q := client.IncidentEvent.Query().Where(incidentevent.EventType("comment"))
	if tenantID > 0 {
		q = q.Where(incidentevent.TenantID(tenantID))
	}
	return q.All(ctx)
}

// alreadyMigrated 用 (ticket_id, user_id, content, created_at) 四元组查重——同一条评论事件
// 重复回填时命中同一行，不产生第二条 ticket_comments。
func alreadyMigrated(ctx context.Context, client *ent.Client, plan commentPlan) (bool, error) {
	return client.TicketComment.Query().
		Where(
			ticketcomment.TicketID(plan.ticketID),
			ticketcomment.UserID(plan.userID),
			ticketcomment.Content(plan.content),
			ticketcomment.CreatedAt(plan.createdAt),
		).
		Exist(ctx)
}

// backfillOne 处理一条评论事件：resolvePlan 判断是否该写入，alreadyMigrated 查重，
// 不存在则写入一条 ticket_comments。返回的 outcome 供调用方统计 created/skipped 数量，
// reason 在 outcomeSkipped 时说明原因（已迁移过 / 不满足写入条件），仅用于日志展示。
func backfillOne(ctx context.Context, client *ent.Client, event *ent.IncidentEvent) (outcome, string, error) {
	plan, ok, reason, err := resolvePlan(ctx, client, event)
	if err != nil {
		return outcomeSkipped, "", err
	}
	if !ok {
		return outcomeSkipped, reason, nil
	}

	exists, err := alreadyMigrated(ctx, client, plan)
	if err != nil {
		return outcomeSkipped, "", fmt.Errorf("查重失败: %w", err)
	}
	if exists {
		return outcomeSkipped, "已经回填过（命中查重）", nil
	}

	_, err = client.TicketComment.Create().
		SetTicketID(plan.ticketID).
		SetUserID(plan.userID).
		SetContent(plan.content).
		SetIsInternal(false).
		SetTenantID(plan.tenantID).
		SetCreatedAt(plan.createdAt).
		SetUpdatedAt(plan.createdAt).
		Save(ctx)
	if err != nil {
		return outcomeSkipped, "", fmt.Errorf("写入 ticket_comments 失败: %w", err)
	}
	return outcomeCreated, "", nil
}

// previewBackfill 是 -dry-run 用的只读版本：跑跟 backfillOne 完全相同的判断链路
// （resolvePlan + alreadyMigrated），但不调用 Create，只统计数量。
func previewBackfill(ctx context.Context, client *ent.Client, events []*ent.IncidentEvent) (wouldCreate, wouldSkip int, err error) {
	for _, event := range events {
		plan, ok, _, err := resolvePlan(ctx, client, event)
		if err != nil {
			return 0, 0, err
		}
		if !ok {
			wouldSkip++
			continue
		}
		exists, err := alreadyMigrated(ctx, client, plan)
		if err != nil {
			return 0, 0, err
		}
		if exists {
			wouldSkip++
			continue
		}
		wouldCreate++
	}
	return wouldCreate, wouldSkip, nil
}

func main() {
	tenantID := flag.Int("tenant-id", 0, "只处理指定租户（<=0 表示处理所有租户）")
	dryRun := flag.Bool("dry-run", true, "true 只打印候选统计，不实际写入；确认无误后用 -dry-run=false 真正回填")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	client, err := database.InitDatabaseWithRLS(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		sugar.Fatalw("connect database", "error", err)
	}
	defer client.Close()

	ctx := tenantctx.SystemContext(
		context.Background(),
		"ops:backfill_incident_comments",
		"WorkItem 详情页能力对齐：把 incident_events 里的存量评论事件搬到 ticket_comments",
	)

	candidates, err := findCandidates(ctx, client, *tenantID)
	if err != nil {
		sugar.Fatalw("查找待回填评论事件失败", "error", err)
	}
	if len(candidates) == 0 {
		sugar.Infow("没有找到需要回填的评论事件", "tenant_id", *tenantID)
		return
	}
	sugar.Infow("找到待回填评论事件", "count", len(candidates), "tenant_id", *tenantID, "dry_run", *dryRun)

	if *dryRun {
		wouldCreate, wouldSkip, err := previewBackfill(ctx, client, candidates)
		if err != nil {
			sugar.Fatalw("预览回填失败", "error", err)
		}
		sugar.Infow("dry-run 预览完成", "would_create", wouldCreate, "would_skip", wouldSkip)
		sugar.Infow("dry-run 模式，未实际写入——确认列表无误后加 -dry-run=false 重新运行")
		return
	}

	created, skipped, failed := 0, 0, 0
	for _, event := range candidates {
		result, reason, err := backfillOne(ctx, client, event)
		switch {
		case err != nil:
			sugar.Errorw("回填评论失败", "incident_event_id", event.ID, "error", err)
			failed++
		case result == outcomeSkipped:
			sugar.Infow("跳过评论事件", "incident_event_id", event.ID, "reason", reason)
			skipped++
		default:
			created++
		}
	}

	sugar.Infow("回填完成", "created", created, "skipped", skipped, "failed", failed, "total", len(candidates))
	if failed > 0 {
		os.Exit(1)
	}
}
```

Also add `"time"` to `commentPlan`'s original import block if not already present from Task 1 (it
is — `commentPlan.createdAt` is `time.Time`).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd itsm-backend && go test ./cmd/backfill_incident_comments/... -v`
Expected: PASS (all 10 tests across Task 1 and Task 2)

Run: `cd itsm-backend && go build ./...`
Expected: no errors (confirms `main()` compiles against real `config`/`database`/`tenantctx`
packages, not just the test-covered helper functions).

- [ ] **Step 5: Commit**

```bash
cd itsm-backend
git add cmd/backfill_incident_comments/
git commit -m "feat(migration): add backfill_incident_comments CLI tool"
```

- [ ] **Step 6: Run the tool against the target environment (deployment step, not code)**

Before proceeding to Task 3, an operator runs this against the environment being migrated:

```bash
cd itsm-backend
go run ./cmd/backfill_incident_comments -dry-run=true                # review the printed counts
go run ./cmd/backfill_incident_comments -dry-run=false                # actually backfill
go run ./cmd/backfill_incident_comments -dry-run=false                # re-run — should report created=0
```

This is an operational action against a real database, not something a unit test drives — it's
listed here because Task 3 assumes the target environment's Incident comments already exist in
`ticket_comments` before the frontend stops reading the old `incident_events`-backed endpoint.

---

## Task 3: Frontend — switch Incident's comment tab to `ticketCommentAdapter` + `workItemId`

**Files:**
- Modify: `itsm-frontend/src/app/(main)/incidents/[id]/page.tsx:1-13,99-106`

**Interfaces:**
- Consumes: `ticketCommentAdapter` (existing, `@/components/business/detail-tabs`,
  `itsm-frontend/src/components/business/detail-tabs/adapters/ticket-comment-adapter.ts`);
  `workItem: WorkItemCommon | null` (existing page state, `workItem.id` is `incident.workItemId`
  per `toWorkItemCommon` at the top of this same file).

- [ ] **Step 1: Change the import**

In `itsm-frontend/src/app/(main)/incidents/[id]/page.tsx`, replace:

```tsx
import {
  CommentPanel,
  HistoryTimeline,
  incidentCommentAdapter,
  fetchAuditLogHistory,
} from '@/components/business/detail-tabs';
```

with:

```tsx
import {
  CommentPanel,
  HistoryTimeline,
  ticketCommentAdapter,
  fetchAuditLogHistory,
} from '@/components/business/detail-tabs';
```

Also add `Empty` to the existing `import { App, Button, Card, Tabs } from 'antd';` line, making it
`import { App, Button, Card, Empty, Tabs } from 'antd';`.

- [ ] **Step 2: Switch the `CommentPanel` in the "评论" tab to use `workItem.id`**

Replace the `comments` tab's `children` (currently lines 99-106):

```tsx
                children: (
                  <CommentPanel
                    targetType="incident"
                    targetId={numericId}
                    adapter={incidentCommentAdapter}
                    currentUserId={user?.id}
                    formatDateTime={formatDateTime}
                    showInternalToggle={false}
                  />
                ),
```

with:

```tsx
                children: workItem ? (
                  <CommentPanel
                    targetType="incident"
                    targetId={workItem.id}
                    adapter={ticketCommentAdapter}
                    currentUserId={user?.id}
                    formatDateTime={formatDateTime}
                    showInternalToggle
                  />
                ) : (
                  <Empty description="该事件尚未关联 WorkItem，暂不支持评论" />
                ),
```

Note the switch from `targetId={numericId}` (the Incident's own PK) to `targetId={workItem.id}`
(`incident.workItemId`, i.e. `tickets.id`) — `ticket_comments.ticket_id` is a WorkItem id, not an
Incident id, so passing the wrong one would silently read/write an unrelated ticket's comments.
`showInternalToggle` flips from `false` to `true` (shorthand for `showInternalToggle={true}`)
because `ticket_comments.is_internal` is now a real, persisted field — the old
`incidentCommentAdapter` disabled this toggle specifically because `incident_events` had no
reliable equivalent (see its now-removed doc comment in Task 5).

- [ ] **Step 3: Type-check**

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors.

- [ ] **Step 4: Manual verification**

Start the frontend (`npm run dev`) and backend, open an Incident detail page for an Incident that
has a `workItemId` and at least one comment created after Task 2's backfill ran. Confirm:
- Existing (backfilled) comments render in the "评论" tab.
- Posting a new comment succeeds and appears immediately.
- The "仅内部可见" toggle is now visible and functional.

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add "src/app/(main)/incidents/[id]/page.tsx"
git commit -m "feat(incident): read/write incident comments through ticketCommentAdapter"
```

---

## Task 4: Retire the old backend Incident comment endpoints

**Files:**
- Modify: `itsm-backend/controller/incident_controller.go` (remove `GetIncidentComments` at
  lines 1322-1402, `CreateIncidentComment` at lines 1417-1502, and their now-unused imports)
- Delete: `itsm-backend/dto/incident_comment_dto.go`
- Modify: `itsm-backend/router/router.go:826-828`

**Interfaces:** none produced — this is a pure deletion task. No other code depends on the removed
symbols (verified: `dto.IncidentCommentResponse`/`CreateIncidentCommentRequest`/
`ToIncidentCommentResponse` are referenced only from `incident_controller.go` and the generated
`docs/docs.go` swagger file, which gets regenerated, not hand-edited).

- [ ] **Step 1: Remove the two handler methods from `incident_controller.go`**

Delete `GetIncidentComments` (the full function at lines 1322-1402, including its `@Summary`/
`@Success` swagger comment block immediately above it) and `CreateIncidentComment` (lines 1417-1502,
same — including its swagger comment block). Leave everything else in the file untouched.

- [ ] **Step 2: Remove the now-unused imports**

After deleting both handlers, `"itsm-backend/ent/incidentevent"` and `"itsm-backend/ent/user"` are
no longer referenced anywhere in `incident_controller.go` (confirmed: `incidentevent.` only
appeared inside `GetIncidentComments`; `user.` only appeared inside `GetIncidentComments` and
`CreateIncidentComment`). Remove both lines from the file's `import (...)` block.

- [ ] **Step 3: Delete the DTO file**

```bash
cd itsm-backend
git rm dto/incident_comment_dto.go
```

- [ ] **Step 4: Remove the route registration**

In `itsm-backend/router/router.go`, delete lines 826-828:

```go
				// 评论
				inc.GET("/:id/comments", middleware.RequirePermission("incident", "read"), config.IncidentController.GetIncidentComments)
				inc.POST("/:id/comments", middleware.RequirePermission("incident", "write"), config.IncidentController.CreateIncidentComment)
```

- [ ] **Step 5: Build and run backend tests**

Run: `cd itsm-backend && go build ./...`
Expected: no errors (confirms no other file referenced the deleted handlers/DTO).

Run: `cd itsm-backend && go test ./controller/... ./router/... ./dto/...`
Expected: PASS, no failures referencing the removed comment endpoints.

- [ ] **Step 6: Regenerate swagger docs**

Run whatever this repo's existing swagger regeneration command is (check `itsm-backend/Makefile` or
`package.json`-equivalent scripts for a `swag init` invocation — the project's own commit history
shows a precedent: `25797681 chore(docs): resync swagger after rebase onto main`). This removes the
stale `dto.IncidentCommentResponse`/`dto.CreateIncidentCommentRequest` definitions and the
`/incidents/{id}/comments` paths from `docs/docs.go` / `docs/swagger.json` / `docs/swagger.yaml`.

- [ ] **Step 7: Commit**

```bash
cd itsm-backend
git add controller/incident_controller.go router/router.go docs/
git commit -m "refactor(incident): retire standalone incident comment endpoints, superseded by ticket_comments"
```

---

## Task 5: Retire the old frontend Incident comment adapter and dead API methods

**Files:**
- Delete: `itsm-frontend/src/components/business/detail-tabs/adapters/incident-comment-adapter.ts`
- Modify: `itsm-frontend/src/components/business/detail-tabs/index.ts:22` (remove the barrel export)
- Modify: `itsm-frontend/src/lib/api/incident-api.ts` (remove `addComment`, `getIncidentComments`,
  `deleteIncidentComment`, and the `IncidentComment` interface)

**Interfaces:** none produced — pure deletion. Verified: after Task 3, nothing imports
`incidentCommentAdapter`; after this task, nothing imports `IncidentAPI.getIncidentComments` /
`.addComment` / `.deleteIncidentComment` / the `IncidentComment` type (all four were only consumed
by the adapter file being deleted here).

- [ ] **Step 1: Delete the adapter file and its barrel export**

```bash
cd itsm-frontend
git rm src/components/business/detail-tabs/adapters/incident-comment-adapter.ts
```

In `itsm-frontend/src/components/business/detail-tabs/index.ts`, remove line 22:

```ts
export { incidentCommentAdapter } from './adapters/incident-comment-adapter';
```

- [ ] **Step 2: Remove the dead API surface from `incident-api.ts`**

In `itsm-frontend/src/lib/api/incident-api.ts`, remove:
- The `addComment` method (lines 469-473, including its `// 添加评论...` comment line above it).
- The `getIncidentComments` method (lines 537-547, including its doc comment).
- The `deleteIncidentComment` method (lines 549-555, including its doc comment) — this one always
  threw `新功能开发中`, so nothing was relying on it actually working; deleting it doesn't remove
  any working capability.
- The `IncidentComment` interface (lines 318-327) — its only consumer was the deleted adapter.

- [ ] **Step 3: Type-check**

Run: `cd itsm-frontend && npm run type-check`
Expected: no errors.

- [ ] **Step 4: Run the frontend test suite**

Run: `cd itsm-frontend && npm test`
Expected: PASS, no failures referencing the removed adapter or API methods.

- [ ] **Step 5: Commit**

```bash
cd itsm-frontend
git add -A src/components/business/detail-tabs/ src/lib/api/incident-api.ts
git commit -m "refactor(incident): remove retired incidentCommentAdapter and dead comment API methods"
```

---

## Task 6: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Backend**

Run: `cd itsm-backend && go build ./... && go vet ./... && go test ./...`
Expected: build succeeds, vet clean, all tests pass.

- [ ] **Step 2: Frontend**

Run: `cd itsm-frontend && npm run type-check && npm run lint:check && npm test`
Expected: all pass.

- [ ] **Step 3: Manual end-to-end check**

With both backend and frontend running against a DB that has had the backfill tool run against it
(Task 2 Step 6): open an Incident that has historical comments, confirm they appear; post a new
comment; confirm `GET /api/v1/incidents/:id/comments` (the old route) now returns 404 (route
removed); confirm `GET /api/v1/tickets/:workItemId/comments` returns the same comments the UI shows.

- [ ] **Step 4: Commit (only if verification surfaced fixes)**

If any step above required a fix, commit it separately with a message describing what it addressed.
