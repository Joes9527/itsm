# 邮件回复线程追踪设计文档（方案 B：Graph conversationId）

> **日期：** 2026-08-13
> **状态：** 待 review
> **前置：** 邮件建单（MS Graph）已完整落地并验收通过。本文档补齐「用户回复工单邮件」的处理逻辑，采用 **Graph conversationId 线程追踪**（方案 B）。

---

## 1. 背景与问题

### 1.1 问题陈述

邮件建单闭环中，系统建单成功后自动回一封确认信（主题 `Re: [TKT-xxx] ...`）。当**用户直接回复这封确认邮件**时，当前实现会**创建一张全新工单**（而非把回复追加到原工单）。

### 1.2 当前行为（bug）

`EmailPollingCoordinator.handleMessage` 流程：

```
收到邮件 → 自环防护 → 去重(按 internetMessageId) → 查用户 → AI 分派 → 创建新工单 → 回信
```

没有「回复识别」逻辑：回复邮件有新的 `internetMessageId`（去重不生效）、发件人非共享邮箱（自环防护不触发）→ 建新单。

### 1.3 期望行为

用户回复工单确认邮件时：

1. 识别这是对某张已有工单的回复
2. 把回复正文**追加为该工单的外部可见评论**
3. 不创建新工单

---

## 2. 现状核实（2026-08-13，含 Graph 行为验证）

| 项 | 现状 |
|---|---|
| delta 响应 message | **已含 `conversationId` 字段**（实测：`AAQkADM3...` 格式，每封首封邮件独立） |
| `sendMail` 指定 `conversationId` | **验证通过**（实测返回 202，可延续对话） |
| `internetMessageId` | 已解析，用于去重 |
| `ticket.external_message_id` | 已存在（去重用，`tenant_id + external_message_id` 复合索引） |
| `ticket_number` 唯一索引 | 已存在 |
| `TicketStore` 接口 | 4 个方法：`FindActiveUserByEmail` / `TicketExistsForExternalMessage` / `CreateTicket` / `PostSystemComment` |
| `PostSystemComment` | 硬编码 `[系统 AI 分派]` + `IsInternal(true)`，不适合用户回复 |

### 2.1 线程追踪机制（方案 B 核心）

Graph 的对话线程模型：

```
用户发邮件 M1 → conversationId = C1（首封邮件分配）
系统 sendMail 确认信（指定 conversationId=C1）→ 确认信归入 C1
用户回复确认信 M3 → conversationId = C1（延续对话）
```

**关键：** `sendMail` 必须**指定 `conversationId=C1`** 才能延续对话。若不指定，Graph 会为确认信开启新对话 C2，导致用户回复落在 C2，追踪断裂。

---

## 3. 决策记录

### D1. 采用 Graph `conversationId` 线程追踪（方案 B）

- 不采用主题工单号解析（方案 A）：依赖主题格式约定，用户改主题即失效，非邮件系统原生语义。
- 采用 conversationId：Graph 原生线程标识，不依赖主题，覆盖所有客户端回复行为。
- 已实测验证：delta 返回 conversationId ✅、sendMail 指定 conversationId 返回 202 ✅。

### D2. 新增 `ticket.conversation_id` 字段（schema 改动）

- 与 `external_message_id`（去重）对称，`conversation_id` 用于回复追踪。
- 加 `tenant_id + conversation_id` 复合索引（唯一），按 conversationId 快速查工单。

### D3. 回复评论为外部可见（`IsInternal=false`）

- 用户回复是可见对话，不是系统内部备注。
- 新增 `PostReplyComment` 方法，写 `IsInternal=false`，内容为回复正文。

### D4. 回复不触发 AI 分派

- 回复已关联已有工单，分类/优先级首次建单已定，无需重复分派。
- 跳过 triage，直接追加评论。

### D5. conversationId 缺失时的兜底

- 邮件无 `conversationId`（罕见）→ 走正常建单流程（不报错）。
- conversationId 查不到对应工单 → 走正常建单流程。

---

## 4. 目标架构（改动点）

### 改动点 1：`client.go` — 解析 conversationId + SendMail 支持延续对话

**`deltaMessage` 加字段：**

```go
type deltaMessage struct {
    ID                string    `json:"id"`
    ConversationID    string    `json:"conversationId"`
    InternetMessageID string    `json:"internetMessageId"`
    ...
}
```

**`Message` 加字段：** `ConversationID string`

**`PollDelta` 映射：** `ConversationID: v.ConversationID`

**`SendMail` 签名加 conversationId 参数：**

```go
func (c *Client) SendMail(ctx context.Context, mailbox, toAddress, subject, body, conversationID string) error {
    payload := map[string]interface{}{
        "message": map[string]interface{}{
            "subject": subject,
            "body":    ...,
            "toRecipients": ...,
            // 延续对话线程：指定原邮件的 conversationId，使用户回复能归入同一对话
            "conversationId": conversationID,  // 空值时省略
            "internetMessageHeaders": ...,
        },
        ...
    }
}
```

> 注意：`conversationId` 为空字符串时不应写入 payload（否则 Graph 可能报错）。

### 改动点 2：`connector.go` — `Send` 方法签名透传

`GraphConnector.Send(ctx, msg)` 内部调用 `SendMail`，需适配新签名（连接器通用 `Send` 接口的 `msg.Channel` 是收件人，`msg` 无 conversationId，此处传空即可，或保留旧签名另加 `SendReply`）。**实现时确认 connector.Send 是否被邮件建单链路使用**——当前建单回信走 `coordinator` 直接调 `GraphClient().SendMail`，不走 `connector.Send`，故 `connector.Send` 保持传空 conversationId。

### 改动点 3：ent schema — `ticket.conversation_id`

```go
field.String("conversation_id").
    Comment("邮件对话线程ID（Graph conversationId），用于识别用户回复并追加评论").
    Optional(),
// Indexes
index.Fields("tenant_id", "conversation_id").Unique(),
```

`go generate ./ent/...` 重新生成。

### 改动点 4：DTO / repository / service 透传 `ConversationID`

沿 `external_message_id` 同样的三层透传路径：

- `dto.CreateTicketRequest` 加 `ConversationID string`
- `repository/ticket/model.go` `CreateParams` 加 `ConversationID string`
- `repository/ticket/repository_impl.go` `Create()` 透传 `SetConversationID`
- `service/ticket_service.go` `CreateTicket()` 透传

### 改动点 5：`coordinator.go` — 回复识别 + InboundTicketRequest 加 ConversationID

**`InboundTicketRequest` 加字段：** `ConversationID string`

**`handleMessage` 插入回复识别分支**（去重之后、查用户之后、建单之前）：

```go
// 回复识别：conversationId 匹配已有工单 → 追加评论
if m.ConversationID != "" {
    ticketID, found, err := c.store.FindTicketByConversationID(ctx, tenantID, m.ConversationID)
    if err != nil {
        c.logger.Errorw("msgraph conversation lookup failed", "tenant_id", tenantID, "error", err)
        return
    }
    if found {
        body := cleanEmailBody(m.BodyContent)
        if body == "" { body = "(无正文)" }
        comment := fmt.Sprintf("[邮件回复] %s", body)
        if err := c.store.PostReplyComment(ctx, tenantID, ticketID, userID, comment); err != nil {
            c.logger.Warnw("msgraph failed to post reply comment", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
        }
        c.logger.Infow("msgraph reply appended to ticket", "tenant_id", tenantID, "ticket_id", ticketID, "from", m.FromAddress)
        return
    }
}
```

**建单时传 ConversationID + 回信延续对话：**

```go
ticketID, ticketNumber, err := c.store.CreateTicket(ctx, tenantID, InboundTicketRequest{
    ...
    ConversationID: m.ConversationID,
})

// 回信：指定 conversationId 延续对话
conn.GraphClient().SendMail(ctx, conn.Mailbox(), m.FromAddress, replySubject, replyBody, m.ConversationID)
```

### 改动点 6：`coordinator.go` — `TicketStore` 接口加两个方法

```go
type TicketStore interface {
    FindActiveUserByEmail(...)
    TicketExistsForExternalMessage(...)
    CreateTicket(...)
    PostSystemComment(...)
    // 新增：
    FindTicketByConversationID(ctx context.Context, tenantID int, conversationID string) (ticketID int, found bool, err error)
    PostReplyComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error
}
```

### 改动点 7：`email_msgraph_wiring.go` — 实现两个新方法 + CreateTicket 透传

```go
func (a *ticketStoreAdapter) FindTicketByConversationID(ctx, tenantID, conversationID) (int, bool, error) {
    t, err := a.client.Ticket.Query().
        Where(ticket.TenantIDEQ(tenantID), ticket.ConversationIDEQ(conversationID)).
        First(ctx)
    if ent.IsNotFound(err) { return 0, false, nil }
    if err != nil { return 0, false, err }
    return t.ID, true, nil
}

func (a *ticketStoreAdapter) PostReplyComment(ctx, tenantID, ticketID, authorUserID, content) error {
    _, err := a.client.TicketComment.Create().
        SetTicketID(ticketID).SetUserID(authorUserID).
        SetContent(content).SetIsInternal(false).SetTenantID(tenantID).
        Save(ctx)
    return err
}
```

`CreateTicket` 透传 `ConversationID` 到 `dto.CreateTicketRequest.ConversationID`。

### 改动点 8：测试

- `client_test.go`：`SendMail` 断言 conversationId 正确写入 payload（指定时）；空 conversationId 时 payload 不含该字段。
- `coordinator_test.go`：`fakeStore` 实现两个新方法；新增用例：conversationId 匹配 → 追加回复评论不建单；conversationId 不匹配 → 建单；无 conversationId → 建单。
- `email_msgraph_wiring_test.go`：`FindTicketByConversationID` 命中/未命中；`PostReplyComment` 写 `IsInternal=false`。

---

## 5. 测试计划

| 用例 | 期望 |
|---|---|
| conversationId 匹配已有工单 | 追加外部评论，不建新单 |
| conversationId 无匹配 | 走正常建单 |
| 邮件无 conversationId | 走正常建单 |
| 建单后回信 | sendMail 带原 conversationId（延续对话） |
| 回复评论 | `IsInternal=false`，内容为清洗后正文 |

## 6. 范围与取舍（明确排除）

| 排除项 | 理由 |
|---|---|
| 主题工单号解析（方案 A） | 已否决（决策 D1） |
| 回复通知处理人 | 独立话题，依赖通知系统 |
| 回复的 AI 分派 | 不触发（决策 D4） |
| 附件解析 | 独立话题 |
| `in-reply-to` / `references` 头 | conversationId 已覆盖线程追踪，头解析是冗余复杂度 |

## 7. 与 AGENTS.md 对齐

| 原则 | 对齐 |
|---|---|
| 用原生机制，不发明约定 | Graph conversationId 是邮件系统原生线程标识 |
| 数据有索引 | `tenant_id + conversation_id` 唯一索引 |
| 边界兜底 | conversationId 缺失/无匹配 → 静默落回正常建单 |
| 可观测 | 追加回复、建单、查询失败均有结构化日志 |
| schema 改动最小 | 仅新增一个 `conversation_id` 字段，沿现有 `external_message_id` 路径透传 |
