# 通知多渠道路由设计文档

> **日期：** 2026-08-14
> **状态：** 待 review
> **目标：** 把工单通知从「单渠道硬编码」改成「按用户 event_type 偏好路由到多个渠道（email / in_app / sms / push）」

---

## 1. 背景与问题

### 1.1 用户诉求

helpdesk / L1 / L2 新增评论时，当前只发站内信（in_app），**不会邮件通知 end user**。用户希望通知支持多个渠道（in_app、email、teams 等），按用户偏好路由。

### 1.2 现状诊断（三层断层）

| 层 | 现状 |
|---|---|
| **NotificationPreferenceService**（`service/notification_preference_service.go`） | ✅ 完备：`GetUserPreferenceByEventType`（按事件类型查偏好）、默认偏好表、完整 CRUD |
| **TicketNotificationService**（`service/ticket_notification_service.go`） | ❌ 空壳：`GetUserNotificationPreferences`（642 行）硬编码返回默认值，**没有复用上层的偏好服务** |
| **发送逻辑** | ❌ 单渠道：`SendNotification` 的 `req.Channel` 是单值（`oneof=email in_app sms`），各 Notify 方法**硬编码 `Channel: "in_app"`** |

结论：偏好模型（数据层）已经多渠道，但发送逻辑是单渠道，两者之间断开。

### 1.3 现有偏好模型（已具备）

`NotificationPreference` 表按 `event_type` 维度，每事件类型 4 个渠道开关：

| 字段 | 语义 |
|------|------|
| `event_type` | ticket_created / ticket_assigned / ticket_updated / ticket_resolved / ticket_closed / sla_warning / sla_violated / comment_added / approval_required / mention |
| `email_enabled` | 邮件 |
| `in_app_enabled` | 站内信 |
| `sms_enabled` | 短信 |
| `push_enabled` | 推送（本次定义为 **WebSocket 实时推送**） |
| `frequency` | immediate / hourly_digest / daily_digest（本次仅 immediate） |
| `quiet_hours_start/end` | 免打扰（本次不做） |

---

## 2. 设计决策（已与用户确认）

| 决策 | 选择 |
|------|------|
| D1 路由策略 | 按用户 event_type 偏好路由（自动发到所有启用渠道） |
| D2 Teams | 本次不做（后续独立任务） |
| D3 push 语义 | WebSocket 实时推送（前端在线刷新） |
| D4 范围 | 所有通知类型都改 |
| D5 frequency | 本次只做 immediate（digest 后续独立） |

---

## 3. 目标架构

### 核心：`SendNotification` 从「单渠道」改为「按 event_type 偏好多渠道路由」

```
NotifyXxx(ctx, ...)
  → SendNotification(ctx, ticketID, { UserIDs, EventType, Content }, tenantID)
      → 对每个 userID：
          查偏好 GetUserPreferenceByEventType(userID, tenantID, EventType)
          → 得到启用渠道 { email?, in_app?, sms?, push? }
          → 逐渠道发送：
              email   → emailService.SendTicketNotification
              in_app  → 创建站内通知记录（现有逻辑）
              sms     → smsService.SendTicketNotification
              push    → webSocketService 推送
```

### 改动点 1：`dto.SendTicketNotificationRequest` — Channel → EventType

```go
type SendTicketNotificationRequest struct {
    UserIDs   []int  `json:"userIds" binding:"required,min=1"`
    EventType string `json:"eventType" binding:"required"` // 事件类型，用于查偏好
    Content   string `json:"content" binding:"required"`
}
```

- 移除 `Channel`（单渠道）和 `Type`（与 event_type 冗余）。
- `EventType` 取值为偏好模型的事件类型（如 `comment_added`）。

### 改动点 2：`TicketNotificationService` — 注入偏好服务 + WebSocket

```go
type TicketNotificationService struct {
    // ... 现有字段 ...
    emailService *EmailService
    smsService   *SMSService
    prefService  *NotificationPreferenceService // 新增：按 event_type 查偏好
    wsService    *WebSocketService              // 新增：push 渠道
}

func (s *TicketNotificationService) SetNotificationPreferenceService(p *NotificationPreferenceService) {
    s.prefService = p
}
func (s *TicketNotificationService) SetWebSocketService(w *WebSocketService) {
    s.wsService = w
}
```

### 改动点 3：`SendNotification` — 多渠道路由核心逻辑

替换现有「查偏好 → switch Channel」为「查偏好 → 遍历启用渠道」：

```go
// 伪代码
for _, userID := range req.UserIDs {
    // 查该用户对该事件类型的偏好
    prefs := s.prefService.GetUserPreferenceByEventType(ctx, userID, tenantID, req.EventType)
    // 若用户无偏好记录，用默认偏好（email=true, in_app=true, sms=false, push=false）

    // 站内信：总是创建通知记录（现有行为）
    if prefs.InAppEnabled { s.createInAppNotification(...) }

    // 邮件
    if prefs.EmailEnabled && user.Email != "" {
        s.emailService.SendTicketNotification(...)
    }
    // 短信
    if prefs.SmsEnabled && user.Phone != "" {
        s.smsService.SendTicketNotification(...)
    }
    // push（WebSocket）
    if prefs.PushEnabled && s.wsService != nil {
        s.wsService.SendToUser(userID, ...)
    }
}
```

### 改动点 4：各 Notify 方法 — Type → EventType 映射

| 现有方法 | 现有 Type | 新 EventType |
|---------|----------|--------------|
| NotifyTicketCreated | created | ticket_created |
| NotifyTicketAssigned | assigned | ticket_assigned |
| NotifyTicketStatusChanged | status_changed | ticket_updated |
| NotifyTicketCommented | commented | comment_added |
| NotifySLAWarning | sla_warning | sla_warning |
| NotifySLABreached | sla_breached | sla_violated |
| NotifySLAAlert | sla_alert | sla_violated |
| NotifyEscalated | escalated | ticket_updated（无独立事件，归入 ticket_updated） |
| NotifyResolved | resolved | ticket_resolved |
| SendAssignmentNotification | assigned | ticket_assigned |

> 「escalated」在偏好模型无独立事件类型，归入 `ticket_updated`（升级本质是状态变更）。

### 改动点 5：通知记录处理（TicketNotification 表）

- `TicketNotification.channel` 是单值 string，保留不变。
- 站内信（in_app）渠道**照旧创建一条 TicketNotification + Notification 记录**（前端查询用）。
- email/sms/push 渠道**只实际发送，不额外创建 TicketNotification 记录**（避免记录膨胀、保持前端展示不变）。
- 通知记录 `status` 仍按 in_app 发送结果标记 sent/failed；email/sms/push 发送失败只记日志。

### 改动点 6：`internal/bootstrap/app.go` — 接线

`notificationPreferenceService`（420 行已构造）和 `webSocketService` 注入 `ticketNotificationService`：

```go
ticketNotificationService.SetNotificationPreferenceService(notificationPreferenceService)
ticketNotificationService.SetWebSocketService(webSocketService)
```

### 改动点 7：前端偏好设置 UI 增强（`profile/page.tsx`）

现状：只有 `emailNotify` / `desktopNotify` 两个全局开关，应用到全部事件类型，缺 sms/push。

目标：改为「**按事件类型 × 4 渠道**」的开关矩阵：

| 事件类型 | 邮件 | 站内 | 短信 | 推送 |
|---------|------|------|------|------|
| ticket_created | ☑ | ☑ | ☐ | ☐ |
| ticket_assigned | ☑ | ☑ | ☐ | ☐ |
| comment_added | ☑ | ☑ | ☐ | ☐ |
| sla_warning | ☑ | ☑ | ☐ | ☐ |
| ... | | | | |

- 事件类型列表复用后端 `GET /notification-preferences/event-types`（而非前端硬编码 15 个）。
- 每行 4 个渠道开关（email / in_app / sms / push），对应偏好的 4 个 `*_enabled` 字段。
- 保存时调 `bulkUpdate`（已有 API），payload 带上 `smsEnabled` / `pushEnabled`。
- 短信/推送开关本次 UI 可展示、可保存，但后端 SMS 实际发送未接线（见范围取舍），push 走 WebSocket。

---

## 4. 边界情况

| 情况 | 处理 |
|------|------|
| 用户无偏好记录 | 用默认偏好（email=true, in_app=true, sms=false, push=false） |
| 用户无邮箱 / 无手机号 | 对应渠道跳过（email/sms 需目标地址） |
| emailService 为 nil | 邮件渠道跳过（当前 emailService 已接线，不会 nil） |
| smsService 为 nil | 短信渠道跳过（短信接线是后续任务，本次不接） |
| wsService 为 nil | push 渠道跳过 |
| 渠道全部禁用 | 至少保留 in_app 站内记录（现有「站内总是创建记录」语义） |

---

## 5. 测试计划

### 5.1 单元测试（`ticket_notification_service`）

- **多渠道路由**：mock 偏好（email+in_app 启用，sms+push 禁用）→ 断言 emailService 和站内记录被调用，sms/push 不调用。
- **默认偏好**：用户无偏好记录 → 按默认偏好（email+in_app）。
- **渠道地址缺失**：EmailEnabled=true 但用户无邮箱 → 邮件跳过。
- **push 渠道**：PushEnabled=true + wsService 非 nil → WebSocket 调用。
- **event_type 映射**：NotifyTicketCommented 传 `comment_added` → 按 comment_added 偏好路由。

### 5.2 集成验证

- 评论通知（helpdesk 评论）→ end user 按 `comment_added` 偏好收到 email + in_app。
- 重启后端 + provision 连接器 → 发邮件验证「Email sent via Graph」。

### 5.3 回归

- `go build ./...` + `go test ./service/...`。

---

## 6. 范围与取舍（明确排除）

| 排除项 | 理由 |
|--------|------|
| Teams 渠道 | 决策 D2，后续独立任务 |
| frequency = digest | 决策 D5，后续独立任务 |
| quiet_hours 免打扰 | 后续独立任务 |
| SMS 接线 | 现有 SMSService 未接线，本次仅保留调用点，不接 SMS 配置 |
| 移动 App 推送（FCM/APNs） | push 定义为 WebSocket（决策 D3） |

---

## 7. 与 AGENTS.md 对齐

| 原则 | 对齐 |
|------|------|
| 复用现有能力 | 复用已完备的 NotificationPreferenceService，不重复造偏好查询 |
| 现有机制优先 | 站内通知记录、emailService 发送逻辑保持不变 |
| 最小改动 | 只改发送路由层，不引入新表、新依赖 |
| 配置从 config 来 | 偏好走 DB（已有），无硬编码 |
