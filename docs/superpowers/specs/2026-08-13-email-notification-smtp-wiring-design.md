# 邮件通知发信接线设计文档（Graph sendMail 版）

> **日期：** 2026-08-14
> **状态：** 待 review（修订版）
> **前置：** 邮件建单（MS Graph 收信）、回复线程追踪、附件处理均已落地。本文档补齐邮件集成闭环的最后一块——**发信通知**，采用 **Graph sendMail**（Exchange Online 已禁用 SMTP Basic Auth，不再用 SMTP 发信）。

---

## 1. 背景与问题

### 1.1 问题陈述

邮件「收」和「发」两条链路严重不对称：

| 链路 | 状态 |
|---|---|
| 收信建单（MS Graph） | ✅ 已完整实现 |
| **发信通知** | ❌ `EmailService` 代码完整但**从未接线**，且基于 **SMTP**（`smtp.SendMail`） |

`EmailService`（`service/email_service.go`）已实现全部发信能力（工单通知、SLA 告警、密码重置、模板、限流、重试），但：
1. `internal/bootstrap/app.go` 从未构造它、从未调用 `SetEmailService`
2. 它基于 SMTP，而 **Exchange Online（Microsoft 365）已禁用 SMTP Basic Auth**，即使接线也发不出去

### 1.2 结论

发信后端必须从 **SMTP 改为 Graph sendMail**，复用已接好的 msgraph 连接器。

---

## 2. 现状核实（2026-08-14，直接读码确认）

| 项 | 现状 |
|---|---|
| `EmailService` | ✅ 完整（`Send`/`SendTicketNotification`/`SendSLANotification`/`SendPasswordResetEmail`/`SendTemplate`） |
| `EmailService.Send` 核心 | 构建 MIME + `smtp.SendMail`，3 次重试 + 限流（20 封/分钟/收件人） |
| 模板方法 | `SendTicketNotification` 等构造 `EmailMessage{To, Subject, Body(HTML), BodyText(纯文本)}` 后调 `Send` |
| 消费者 | `TicketNotificationService` 已注入 6 个消费者，但 `emailService` 字段恒为 nil（`SetEmailService` 从未调用） |
| `AuthService` | 有 `SetEmailService`（密码重置），从未调用；`baseURL` 硬编码 |
| **msgraph 连接器** | ✅ 已 provision（`ai-support@dawnpro.onmicrosoft.com`），`GraphConnector.GraphClient().SendMail(ctx, mailbox, to, subject, body)` 已实现 |
| connector 时机 | ⚠️ 运行时 provision（per-tenant，`connectorManager.Get(1, "msgraph-email")`），非启动时可用 |
| service→connector 依赖 | ✅ 已存在（`service/ticket_service.go` import `itsm-backend/connector`） |

### 2.3 三个既有缺口（原 SMTP spec 遗留，本次一并处理）

- **缺口 A**：`EmailConfig` 缺 `Encryption`/`SkipVerify` —— **Graph 方案下不再需要**（不经过 SMTP），删除该需求。
- **缺口 B**：`Send` 不支持 SSL(465)/skip_verify —— **Graph 方案下不再需要**。
- **缺口 C**：`AuthService.baseURL` 无配置来源 —— **保留**（密码重置链接仍需要 frontend_url）。

---

## 3. 决策记录

### D1. 发信后端：Graph sendMail 为主，SMTP 保留为 fallback

- Exchange Online 禁用 SMTP Basic Auth，主路径走 Graph sendMail。
- 保留 `EmailService` 现有 SMTP 能力作为 fallback（其他环境或 Graph 不可用时）。

### D2. `EmailService` 通过 `GraphMailSender` 接口 + 延迟 provider 注入

- 定义 `GraphMailSender` 接口（service 包，不依赖 msgraph 具体类型）：
  ```go
  type GraphMailSender interface {
      SendMail(ctx context.Context, mailbox, to, subject, body string) error
  }
  ```
- `EmailService` 加 `graphProvider func() (GraphMailSender, string, bool)` 字段（返回 sender + mailbox + 是否可用）。
- **延迟绑定**：connector 运行时 provision，不能启动时注入，发信时动态查。

### D3. Graph 发信用纯文本 body

- `EmailMessage.BodyText`（纯文本）作为 Graph `body.content`（`contentType=Text`），忽略 HTML。
- 通知类邮件纯文本够用，且现有 msgraph `SendMail` 是 Text contentType。

### D4. SMS 接线本次不做

- `SMSConfig` 结构不匹配，需额外适配；用户聚焦邮件。

### D5. 发信失败不新增重试队列

- 沿用现有 `sent`/`failed` 状态机制，发信失败只记日志 + 置 failed。

### D6. `AuthService.baseURL` 从 config 注入

- `ServerConfig` 新增 `FrontendURL`（`frontend_url`），接线时 `authService.SetBaseURL(...)`。

---

## 4. 目标架构（改动点）

### 改动点 1：`service/email_service.go` — Graph 后端 + 延迟 provider

**新增接口：**
```go
type GraphMailSender interface {
    SendMail(ctx context.Context, mailbox, to, subject, body string) error
}
```

**`EmailService` 加字段 + setter：**
```go
type EmailService struct {
    // ... 现有字段 ...
    graphProvider func() (GraphMailSender, string, bool)
}

// SetGraphProvider 注入 Graph 发信后端（延迟绑定：发信时动态查 connector）。
func (s *EmailService) SetGraphProvider(provider func() (GraphMailSender, string, bool)) {
    s.graphProvider = provider
}
```

**`Send` 加 Graph 分支（在 MIME 构建前）：**
```go
func (s *EmailService) Send(ctx context.Context, msg *EmailMessage) error {
    if err := s.validateMessage(msg); err != nil { return err }
    if err := s.checkRateLimit(msg.To, 20, time.Minute); err != nil { return err }

    // Graph 优先：provider 可用则走 Graph sendMail
    if s.graphProvider != nil {
        if sender, mailbox, ok := s.graphProvider(); ok && sender != nil {
            return s.sendViaGraph(ctx, sender, mailbox, msg)
        }
    }
    // SMTP fallback（现有 MIME + smtp.SendMail 逻辑）
    return s.sendViaSMTP(ctx, msg)
}

func (s *EmailService) sendViaGraph(ctx context.Context, sender GraphMailSender, mailbox string, msg *EmailMessage) error {
    body := msg.BodyText
    if body == "" { body = msg.Body }
    for _, to := range msg.To {
        if err := sender.SendMail(ctx, mailbox, to, msg.Subject, body); err != nil {
            return fmt.Errorf("graph send email: %w", err)
        }
    }
    s.logger.Infow("Email sent via Graph", "to", msg.To, "subject", msg.Subject)
    return nil
}
```

（现有 SMTP 发送逻辑提取为 `sendViaSMTP`，不改动业务行为。）

### 改动点 2：`internal/bootstrap/app.go` — 构造 + 注入 graphProvider

在 `connectorManager` 构造（233 行）之后：

```go
// 邮件通知（Graph sendMail 为主，SMTP fallback）
emailService := service.NewEmailService(service.EmailConfig{
    Host: cfg.SMTP.Host, Port: cfg.SMTP.Port,
    Username: cfg.SMTP.Username, Password: cfg.SMTP.Password,
    From: cfg.SMTP.FromEmail, FromName: cfg.SMTP.FromName,
}, sugar)
// 延迟绑定 Graph 发信：发信时动态查 msgraph 连接器（单租户 tenantID=1）
emailService.SetGraphProvider(func() (service.GraphMailSender, string, bool) {
    c, ok := connectorManager.Get(1, "msgraph-email")
    if !ok { return nil, "", false }
    gc, ok := c.(*msgraph.GraphConnector)
    if !ok { return nil, "", false }
    return gc.GraphClient(), gc.Mailbox(), true
})
ticketNotificationService.SetEmailService(emailService)
```

> `authService` 在稍后构造，其 `SetEmailService(emailService)` 需在 authService 构造后注入（变量作用域提到能覆盖该位置）。

### 改动点 3：`internal/bootstrap/app.go` — `authService.SetEmailService` + `SetBaseURL`

authService 构造后：
```go
authService.SetEmailService(emailService)
if cfg.Server.FrontendURL != "" {
    authService.SetBaseURL(cfg.Server.FrontendURL)
}
```

### 改动点 4：`config` — 新增 `frontend_url`

- `ServerConfig` 加 `FrontendURL string`（`mapstructure:"frontend_url"`），env `FRONTEND_URL`。
- `config.yaml` 的 `server:` 段加 `frontend_url: "${FRONTEND_URL:http://localhost:3000}"`。

---

## 5. 测试计划

| 用例 | 期望 |
|---|---|
| `GraphMailSender` mock 可用 | `Send` 走 Graph，调用 mock `SendMail` 每个收件人一次 |
| `graphProvider` 返回不可用 | `Send` 走 SMTP fallback（现有逻辑） |
| `BodyText` 为空 | Graph 用 `Body` 作为纯文本内容 |
| `SendTicketNotification` | 模板方法不变，最终落到 Graph/SMTP 后端 |
| `authService.SetBaseURL` | baseURL 来自 `FRONTEND_URL` |
| 回归 | `go build ./...` + `go test ./service/... ./internal/bootstrap/...` |

## 6. 范围与取舍（明确排除）

| 排除项 | 理由 |
|---|---|
| SMTP Encryption/SSL/skip_verify | Graph 方案不需要（决策 D1），SMTP 仅 fallback |
| SMS 接线 | 结构不匹配（决策 D4） |
| `container.go` 清理 | 死代码，单独话题 |
| 发信异步重试队列 | 沿用现有机制（决策 D5） |
| 邮件模板可视化 UI | 独立话题 |

## 7. 与 AGENTS.md 对齐

| 原则 | 对齐 |
|---|---|
| 复用现有能力 | 复用 msgraph 连接器的 `SendMail`，不新写 Graph 发信 |
| 接口抽象 | `GraphMailSender` 接口隔离 msgraph 具体类型，service 不依赖 connector 实现 |
| 延迟绑定 | connector 运行时 provision，用 provider 延迟查，不硬编码启动时状态 |
| 现有机制优先 | 复用 `SetEmailService` 既有接口 |
| 可观测 | 发信成功/失败记日志 |
