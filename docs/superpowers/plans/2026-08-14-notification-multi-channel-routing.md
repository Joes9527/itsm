# 通知多渠道路由 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把工单通知从「单渠道硬编码」改成「按用户 event_type 偏好路由到 email/in_app/sms/push 四渠道」，并增强前端偏好设置 UI。

**Architecture:** 复用已完备的 `NotificationPreferenceService`（`GetUserPreferenceByEventType` 已按事件类型查偏好并兜底默认值），`TicketNotificationService.SendNotification` 改为「查偏好 → 遍历启用渠道逐渠道发送」。push 走 `WebSocketService`，前端 `profile` 页改为按事件类型 × 4 渠道的开关矩阵。

**Tech Stack:** Go/Gin/Ent（后端）、Next.js/TypeScript/Ant Design（前端）。

## Global Constraints

- 渠道枚举：`email` / `in_app` / `sms` / `push`（push = WebSocket）
- event_type 值：`ticket_created` / `ticket_assigned` / `ticket_updated` / `ticket_resolved` / `ticket_closed` / `sla_warning` / `sla_violated` / `comment_added` / `approval_required` / `mention`
- DTO 字段 camelCase（符合 AGENTS.md）
- 默认偏好：email=true, in_app=true, sms=false, push=false
- 站内信（in_app）总是创建通知记录；email/sms/push 只实际发送不建记录
- 本次不做：Teams、digest、quiet_hours、SMS 接线

---

### Task 1: dto 改造 — `SendTicketNotificationRequest` Channel → EventType

**Files:**
- Modify: `itsm-backend/dto/ticket_notification_dto.go`

**Interfaces:**
- Produces: `SendTicketNotificationRequest{ UserIDs []int; EventType string; Content string }`（移除 `Channel`、`Type` 字段）

- [ ] **Step 1: 修改 dto 结构**

将 `SendTicketNotificationRequest`（约 31-36 行）改为：

```go
type SendTicketNotificationRequest struct {
	UserIDs   []int  `json:"userIds" binding:"required,min=1"` // 接收人ID列表
	EventType string `json:"eventType" binding:"required"`     // 事件类型（偏好查询键）：ticket_created / comment_added 等
	Content   string `json:"content" binding:"required"`       // 通知内容
}
```

（删除原 `Type` 和 `Channel` 字段。）

- [ ] **Step 2: 编译检查**

Run: `cd itsm-backend && go build ./dto/`
Expected: 成功（后续 Task 3 修复 `service` 包的引用后整体编译通过）。

- [ ] **Step 3: 暂不单独 commit（与 Task 2 一起提交）**

---

### Task 2: TicketNotificationService 注入偏好服务 + WebSocket

**Files:**
- Modify: `itsm-backend/service/ticket_notification_service.go`

**Interfaces:**
- Consumes: `*NotificationPreferenceService`（`GetUserPreferenceByEventType(ctx, userID, tenantID int, eventType string) (*dto.NotificationPreferenceResponse, error)`）、`*WebSocketService`（`GetHub().SendToUser(userID int, message WebSocketMessage)`）
- Produces: `SetNotificationPreferenceService(p *NotificationPreferenceService)`、`SetWebSocketService(w *WebSocketService)`

- [ ] **Step 1: 加字段**

在 `TicketNotificationService` struct（约 21-27 行）加两个字段：

```go
type TicketNotificationService struct {
	// ... 现有字段 ...
	emailService *EmailService
	smsService   *SMSService
	prefService  *NotificationPreferenceService // 按 event_type 查偏好
	wsService    *WebSocketService              // push 渠道（WebSocket）
}
```

- [ ] **Step 2: 加 setter**

```go
// SetNotificationPreferenceService 注入通知偏好服务
func (s *TicketNotificationService) SetNotificationPreferenceService(p *NotificationPreferenceService) {
	s.prefService = p
}

// SetWebSocketService 注入 WebSocket 服务（push 渠道）
func (s *TicketNotificationService) SetWebSocketService(w *WebSocketService) {
	s.wsService = w
}
```

- [ ] **Step 3: Commit**

```bash
git add itsm-backend/dto/ticket_notification_dto.go itsm-backend/service/ticket_notification_service.go
git commit -m "feat(notification): dto Channel→EventType + 注入偏好/WebSocket 服务"
```

---

### Task 3: SendNotification 多渠道路由核心重构

**Files:**
- Modify: `itsm-backend/service/ticket_notification_service.go`（`SendNotification`，约 46-198 行）

**Interfaces:**
- Consumes: `s.prefService.GetUserPreferenceByEventType`、`s.emailService.SendTicketNotification(ctx, to []string, ticketNumber, ticketTitle, action, content string) error`、`s.smsService.SendTicketNotification(ctx, to []string, ticketNumber, ticketTitle, action string) error`、`s.wsService.GetHub().SendToUser`
- Produces: 多渠道路由后的 `SendNotification`

- [ ] **Step 1: 重写 SendNotification 主体**

将 `SendNotification` 重写为按偏好多渠道路由。核心结构：

```go
func (s *TicketNotificationService) SendNotification(
	ctx context.Context,
	ticketID int,
	req *dto.SendTicketNotificationRequest,
	tenantID int,
) error {
	s.logger.Infow("Sending ticket notification", "ticket_id", ticketID, "event_type", req.EventType)

	ticketEntity, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("failed to get ticket: %w", err)
	}

	now := time.Now()
	for _, userID := range req.UserIDs {
		userEntity, err := s.client.User.Get(ctx, userID)
		if err != nil || userEntity == nil {
			s.logger.Warnw("User not found, skipping", "user_id", userID)
			continue
		}

		// 查偏好（带默认值兜底）
		prefs, err := s.prefService.GetUserPreferenceByEventType(ctx, userID, tenantID, req.EventType)
		if err != nil {
			s.logger.Warnw("Failed to get preference, using defaults", "user_id", userID, "error", err)
			prefs = &dto.NotificationPreferenceResponse{
				EmailEnabled: true, InAppEnabled: true, SmsEnabled: false, PushEnabled: false,
			}
		}

		// 1. 站内信：总是创建通知记录（现有语义）
		if prefs.InAppEnabled {
			s.createInAppNotification(ctx, ticketID, userID, req, tenantID, now)
		}

		// 2. 邮件
		if prefs.EmailEnabled && s.emailService != nil && userEntity.Email != "" {
			if err := s.emailService.SendTicketNotification(ctx, []string{userEntity.Email}, ticketEntity.TicketNumber, ticketEntity.Title, req.EventType, req.Content); err != nil {
				s.logger.Errorw("Failed to send email notification", "error", err, "user_id", userID)
			}
		}

		// 3. 短信（若 smsService 已接线）
		if prefs.SmsEnabled && s.smsService != nil && userEntity.Phone != "" {
			if err := s.smsService.SendTicketNotification(ctx, []string{userEntity.Phone}, ticketEntity.TicketNumber, ticketEntity.Title, req.EventType); err != nil {
				s.logger.Errorw("Failed to send SMS notification", "error", err, "user_id", userID)
			}
		}

		// 4. push（WebSocket）
		if prefs.PushEnabled && s.wsService != nil {
			s.wsService.GetHub().SendToUser(userID, WebSocketMessage{
				Type:    req.EventType,
				Payload: map[string]interface{}{"ticket_id": ticketID, "content": req.Content},
			})
		}
	}

	return nil
}
```

- [ ] **Step 2: 提取 createInAppNotification 辅助方法**

把原 `SendNotification` 里「创建 TicketNotification + Notification 记录 + 标记 sent」的逻辑提取为：

```go
// createInAppNotification 创建站内通知记录（TicketNotification + Notification），并标记已发送。
func (s *TicketNotificationService) createInAppNotification(
	ctx context.Context, ticketID, userID int,
	req *dto.SendTicketNotificationRequest, tenantID int, now time.Time,
) {
	notification, err := s.client.TicketNotification.Create().
		SetTicketID(ticketID).SetUserID(userID).
		SetType(req.EventType).
		SetChannel("in_app").
		SetContent(req.Content).
		SetTenantID(tenantID).
		SetStatus("pending").
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create notification", "error", err, "user_id", userID)
		return
	}
	_, _ = s.client.Notification.Create().
		SetTitle(req.EventType).
		SetMessage(req.Content).
		SetType(req.EventType).
		SetUserID(userID).
		SetTenantID(tenantID).
		SetNillableActionURL(ticketNotificationStringPtr(fmt.Sprintf("/tickets/%d", ticketID))).
		SetNillableActionText(ticketNotificationStringPtr("查看工单")).
		Save(ctx)

	_, err = s.client.TicketNotification.UpdateOneID(notification.ID).
		SetStatus("sent").
		SetNillableSentAt(&now).
		Save(ctx)
	if err != nil {
		s.logger.Warnw("Failed to update notification status", "error", err)
	}
}
```

（注意：`SetType` 现在用 `req.EventType`；`getUserNotificationPreferences` 和旧的 `GetUserNotificationPreferences` 空壳方法若不再被引用，一并删除。）

- [ ] **Step 3: 编译**

Run: `cd itsm-backend && go build ./service/`
Expected: 报 Task 3 未完成——所有 Notify 方法仍引用已删除的 `Type`/`Channel` 字段。这是预期，Task 4 修复。

---

### Task 4: 各 Notify 方法 — Type/Channel → EventType

**Files:**
- Modify: `itsm-backend/service/ticket_notification_service.go`（10 处硬编码点）

**Interfaces:**
- Produces: 所有 Notify/Send 方法统一传 `EventType`

- [ ] **Step 1: 逐方法替换 Type/Channel 为 EventType**

按映射替换以下 10 处（原 `Type: "x", Channel: "in_app"` → `EventType: "..."`）：

| 方法（行号） | 原 Type | 新 EventType |
|--------------|--------|--------------|
| NotifyTicketCreated (~255) | created | ticket_created |
| NotifyTicketAssigned (~271) | assigned | ticket_assigned |
| NotifyTicketStatusChanged (~297) | status_changed | ticket_updated |
| NotifyTicketCommented (~345) | commented | comment_added |
| NotifySLAWarning (~379) | sla_warning | sla_warning |
| NotifySLABreached (~416) | sla_breached | sla_violated |
| NotifySLAAlertLevelChanged (~492) | sla_alert | sla_violated |
| SendAssignmentNotification (~704) | assigned | ticket_assigned |
| SendEscalationNotification (~723) | escalated | ticket_updated |
| SendResolutionNotification (~742) | resolved | ticket_resolved |

示例（NotifyTicketCommented 内）：

```go
return s.SendNotification(ctx, ticketID, &dto.SendTicketNotificationRequest{
	UserIDs:   userIDs,
	EventType: "comment_added",
	Content:   content,
}, tenantID)
```

- [ ] **Step 2: 删除已失效的旧方法**

删除 `getUserNotificationPreferences`（约 675 行）和 `GetUserNotificationPreferences`（约 642 行）及 `UpdateUserNotificationPreferences`（约 658 行）——若它们已无调用方（偏好查询已由 `prefService` 承担）。

- [ ] **Step 3: 编译**

Run: `cd itsm-backend && go build ./service/`
Expected: 成功。

- [ ] **Step 4: Commit**

```bash
git add itsm-backend/service/ticket_notification_service.go
git commit -m "feat(notification): SendNotification 按 event_type 偏好多渠道路由"
```

---

### Task 5: app.go 接线

**Files:**
- Modify: `itsm-backend/internal/bootstrap/app.go`

**Interfaces:**
- Consumes: `notificationPreferenceService`（约 420 行已构造）、`webSocketService`

- [ ] **Step 1: 确认 webSocketService 变量名**

Run: `grep -n 'WebSocketService\|webSocketService\|NewWebSocketService' itsm-backend/internal/bootstrap/app.go`
Expected: 找到 webSocket 服务构造的变量名（如 `webSocketService`）。

- [ ] **Step 2: 注入**

在 `ticketNotificationService := service.NewTicketNotificationService(client, sugar)`（约 238 行）之后、以及 `notificationPreferenceService` 构造（约 420 行）之后，注入：

```go
ticketNotificationService.SetNotificationPreferenceService(notificationPreferenceService)
ticketNotificationService.SetWebSocketService(webSocketService)
```

（若 webSocketService 构造晚于 ticketNotificationService，则在 webSocket 构造后单独注入 `SetWebSocketService`。）

- [ ] **Step 3: 编译 + 运行相关测试**

Run: `cd itsm-backend && go build ./... && go test ./service/ -run TestTicketNotification -count=1`
Expected: 编译通过；若现有 `ticket_notification_service_test.go` 因 dto 字段变更失败，同步修正测试（见 Task 6）。

- [ ] **Step 4: Commit**

```bash
git add itsm-backend/internal/bootstrap/app.go
git commit -m "chore(notification): 接线偏好服务 + WebSocket 到通知服务"
```

---

### Task 6: 单元测试（后端多渠道路由）

**Files:**
- Modify: `itsm-backend/service/ticket_notification_service_test.go`（若存在）；否则新增 `itsm-backend/service/ticket_notification_multi_channel_test.go`

**Interfaces:**
- Consumes: `SendNotification(ctx, ticketID, req, tenantID)`、`SetNotificationPreferenceService`、`SetEmailService`、`SetWebSocketService`

- [ ] **Step 1: 写多渠道路由测试（mock 偏好 + 断言渠道调用）**

```go
func TestSendNotification_MultiChannelRouting(t *testing.T) {
	// 构造 TicketNotificationService，注入 mock 偏好服务（返回 email+in_app 启用）
	// 断言：emailService 被调用、in_app 记录创建、sms/push 不调用
}
```

要点：偏好 mock 返回 `email=true, in_app=true, sms=false, push=false` → 断言 email 调用、in_app 记录存在、sms/push 无调用。

- [ ] **Step 2: 跑测试**

Run: `cd itsm-backend && go test ./service/ -run TestSendNotification_MultiChannelRouting -count=1 -v`
Expected: PASS。

- [ ] **Step 3: 修正旧测试（若因 dto 变更失败）**

Run: `cd itsm-backend && go test ./service/ -count=1`
Expected: 所有 service 测试通过（0 failures）。若有旧测试引用 `Channel`/`Type`，改为 `EventType`。

- [ ] **Step 4: Commit**

```bash
git add itsm-backend/service/*_test.go
git commit -m "test(notification): 多渠道路由单元测试"
```

---

### Task 7: 前端偏好设置 UI 增强

**Files:**
- Modify: `itsm-frontend/src/app/(main)/profile/page.tsx`
- Modify（如需要）: `itsm-frontend/src/lib/api/notification-preference-api.ts`

**Interfaces:**
- Consumes: `GET /notification-preferences/event-types`（返回 `[{code,name,description}]`）、`bulkUpdate({preferences:[{eventType,emailEnabled,smsEnabled,inAppEnabled,pushEnabled,timezone,frequency}]})`

- [ ] **Step 1: 读当前 profile 页偏好设置 UI**

Run: `sed -n '260,360p' itsm-frontend/src/app/'(main)'/profile/page.tsx`
Expected: 确认现有 `emailNotify`/`desktopNotify` 开关、`handleSavePreferences`、加载逻辑。

- [ ] **Step 2: 改为按事件类型 × 4 渠道的开关矩阵**

将偏好设置 UI 改为表格：每行一个事件类型（从 `event-types` API 获取），4 列开关（邮件/站内/短信/推送），用 Ant Design `Table` + `Switch`。

保存 payload 带上 4 个渠道开关：

```ts
const preferences = eventTypes.map(et => ({
  eventType: et.code,
  emailEnabled: row[et.code].email,
  smsEnabled: row[et.code].sms,
  inAppEnabled: row[et.code].inApp,
  pushEnabled: row[et.code].push,
  timezone: values.timezone || 'Asia/Shanghai',
  frequency: 'immediate',
}));
await NotificationPreferenceApi.bulkUpdate({ preferences });
```

- [ ] **Step 3: 确认 bulkUpdate API payload 字段名**

Run: `grep -n 'bulkUpdate\|smsEnabled\|pushEnabled' itsm-frontend/src/lib/api/notification-preference-api.ts`
Expected: payload 字段用 camelCase（`smsEnabled`/`pushEnabled`），与后端 dto `NotificationPreferenceRequest` 对齐。

- [ ] **Step 4: 前端编译 + lint**

Run: `cd itsm-frontend && npm run build 2>&1 | tail -20`（或 `npx tsc --noEmit`）
Expected: 无类型错误。

- [ ] **Step 5: Commit**

```bash
git add itsm-frontend/src/app/'(main)'/profile/page.tsx itsm-frontend/src/lib/api/notification-preference-api.ts
git commit -m "feat(notification): 前端偏好设置改为按事件类型×4渠道矩阵"
```

---

### Task 8: 端到端验证

- [ ] **Step 1: 启动/重启后端 + provision 连接器**

Run: `cd itsm-backend && go build -o /tmp/itsm-backend . && nohup /tmp/itsm-backend > /tmp/itsm-backend.log 2>&1 &`
然后 provision `msgraph-email`（参考 `email-ticket-e2e-testing` skill）。

- [ ] **Step 2: 初始化用户默认偏好**

Run: `curl -X POST http://localhost:8090/api/v1/notification-preferences/init -b "$ITSM_COOKIE_JAR" -H "X-CSRF-Token: $ITSM_CSRF_TOKEN"`
Expected: 为当前用户创建默认偏好（comment_added 等事件 email+in_app 启用）。

- [ ] **Step 3: 触发评论并验证多渠道**

- 用 admin 对某工单新增评论（评论者为 helpdesk/L1）
- 检查日志 `grep -iE 'Email sent via Graph|Sending ticket notification' /tmp/itsm-backend.log`
- 验证 end user 收到邮件 + 站内通知

Expected: 日志显示 `event_type=comment_added`，且 end user（requester）按偏好收到 email + in_app。

- [ ] **Step 4: 回归**

Run: `cd itsm-backend && go build ./... && go test ./service/ -count=1`

---

## Self-Review 备注

- Spec 覆盖：改动点 1-7 均有对应 task（Task 1-7）；边界情况在 Task 3 代码中落实（默认偏好兜底、渠道跳过、in_app 总是建记录）。
- 类型一致：`EventType` 命名全链路统一（dto → service → 前端 `eventType`）。
- 风险点：删除旧 `GetUserNotificationPreferences`/`UpdateUserNotificationPreferences` 前，需确认无其他调用方（Task 4 Step 2 已标注）。
