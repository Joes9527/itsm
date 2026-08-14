# 邮件通知（SMTP 发信）接线设计文档

> **日期：** 2026-08-13
> **状态：** 待 review
> **前置：** 邮件建单（MS Graph 收信建单）已完整落地（见 `2026-08-11-email-msgraph-ticket-creation-design.md`），本文档补齐「邮件集成」闭环缺失的最后一块——**发信（SMTP）通知的接线**。

---

## 1. 背景与问题

### 1.1 问题陈述

邮件相关的「收」和「发」两条链路现状严重不对称：

| 链路 | 状态 |
|---|---|
| 收信建单（MS Graph） | ✅ 已完整实现并接线 |
| 发信通知（SMTP） | ❌ 代码完整，但**从未接线** |

`EmailService`（`service/email_service.go`）已经实现了全部发信能力（工单通知、SLA 告警、密码重置、模板邮件、限流、重试），但 `internal/bootstrap/app.go` 里**从未构造它**、也从未调用任何 `SetEmailService`。结果是：工单状态变更、SLA 告警、密码重置这三类邮件**全部静默不发信**，用户完全感知不到通知功能的存在。

### 1.2 与「邮件建单」文档的关系

08-11 建单文档背景部分明确把「邮件通知」划为"另一个独立任务"：

> "'邮件通知'（工单状态变更/SLA 告警/密码重置邮件的发送）是另一个已单独定过范围、工作量小得多的独立任务（`internal/bootstrap/app.go` 里补上 `cfg.SMTP` → `service.NewEmailService` → `SetEmailService` 的接线），不在本次改动范围内"

本文档就是补齐这个被延后的独立任务。

---

## 2. 现状核实（2026-08-13，直接读码确认）

### 2.1 发信能力已完整（无需新增业务逻辑）

`service/email_service.go` 已实现：

- `EmailConfig{Host, Port, Username, Password, From, FromName}`
- `NewEmailService(config, logger)` — **当前全仓库零调用**
- `Send(ctx, msg)` — MIME 构建 + `smtp.PlainAuth` + 3 次重试 + 限流（20 封/分钟/收件人）
- `SendTemplate` / `SendTicketNotification` / `SendSLANotification` / `SendPasswordResetEmail`
- `ValidateConfig()` — 校验 Host/Port/Username/From

`service/sms_service.go` 也已完整（`Send` / `SendTicketNotification` / `SendSLANotification` / `SendVerificationCode`，支持 aliyun/tencent/mock）。

### 2.2 消费者已就绪（只差注入）

`TicketNotificationService` **已经构造并注入到 6 个消费者**：

```
app.go:238  ticketNotificationService := service.NewTicketNotificationService(client, sugar)
app.go:401  ticketService.SetNotificationService(ticketNotificationService)
app.go:402  ticketCommentService.SetNotificationService(ticketNotificationService)
app.go:403  ticketRatingService.SetNotificationService(ticketNotificationService)
app.go:625  slaMonitorService.SetNotificationService(ticketNotificationService)
app.go:626  slaAlertService.SetNotificationService(ticketNotificationService)
app.go:627  escalationService.SetNotificationService(ticketNotificationService)
```

`TicketNotificationService.SendNotification` 里发信分支（`email_service.go` 无关，在 `ticket_notification_service.go:152`）：

```go
case "email":
    if s.emailService != nil && userEntity.Email != "" {
        sendErr = s.emailService.SendTicketNotification(...)
```

**但 `emailService` 字段恒为 `nil`**，因为 `SetEmailService` 从未被调用 → 发信分支永远不执行。

`AuthService`（`app.go:572` 构造）同样有 `SetEmailService` 接口（密码重置邮件），但也从未调用；其 `baseURL` 字段硬编码为 `"http://localhost:3000"`，且无 config 来源。

### 2.3 三个次级缺口（接线时一并暴露）

**缺口 A：`EmailConfig` 缺 `Encryption`/`SkipVerify` 字段**

config.yaml 已定义完整 SMTP 配置：

```yaml
smtp:
  enabled: false
  host: "smtp.example.com"
  port: 587
  username: ""
  password: ""
  from_email: "noreply@example.com"
  from_name: "ITSM System"
  encryption: "tls"     # tls | ssl | none
  skip_verify: false    # dev only
```

但 `config.SMTPConfig`（`config/*.go:166`）有 `Encryption`/`SkipVerify`，而 `service.EmailConfig` **没有**这两个字段 → 接线时这两个配置会被丢弃。

**缺口 B：`EmailService.Send` 不支持 SSL(465) 和 skip_verify**

`Send` 用的是标准库 `smtp.SendMail`：
- 它内部会**自动尝试 STARTTLS**（相当于 `encryption=tls`，587 端口场景），但不支持：
  - `encryption=ssl`（465 端口隐式 TLS，需要 `tls.Dial` + `smtp.NewClient` 手动流程）
  - `skip_verify=true`（`smtp.SendMail` 内部用 `tls.Config{ServerName: host}`，无法跳过证书校验，自签名证书的测试环境会失败）

**缺口 C：`AuthService.baseURL` 无配置来源**

密码重置邮件的重置链接指向 `baseURL + "/reset-password?token=..."`，`baseURL` 硬编码 `http://localhost:3000`。生产环境重置链接会指向错误地址。config 里没有 frontend URL 字段。

### 2.4 一个重要附带发现：`internal/container/container.go` 是死代码

`internal/container/container.go`（`NewContainer`/`Initialize`）**全仓库零调用**。它里面也构造了 `ticketNotificationService`（第 110 行）但同样没接 `EmailService`。这是另一条未使用的 DI 路径。

**结论：本次接线只在 `internal/bootstrap/app.go` 做，不碰 `container.go`**（它本来就是死代码，单独清理是另一个范围更大的话题，不在本文档）。

---

## 3. 决策记录

### D1. 接线位置：只在 `app.go`

- 依据：`container.NewContainer` 零调用（死代码），实际运行路径只有 `app.go` 的 `NewApplication`。
- 不在本 spec 里清理 `container.go`，避免范围膨胀。

### D2. `EmailConfig` 补齐 `Encryption`/`SkipVerify` 字段，`Send` 实现三种加密模式

- 理由：config.yaml 已有 `encryption`/`skip_verify`，若接线时不透传，这两个配置就是死配置，且企业 SMTP 常见 465(SSL) 端口会发不出去。
- 三种模式语义：
  - `none`：明文 `smtp.Dial` + PlainAuth，不升级 TLS
  - `tls`：STARTTLS（587 端口），`smtp.Dial` + `c.StartTLS(tlsConfig)` + PlainAuth
  - `ssl`：隐式 TLS（465 端口），`tls.Dial` + `smtp.NewClient` + PlainAuth
  - `skip_verify=true` 时 `tls.Config{InsecureSkipVerify: true}`
- 实现方式：把 `Send` 里的 `smtp.SendMail` 替换为手动的 dial → (可选 TLS) → auth → 发信流程。这是标准库 `net/smtp` 的既有能力，不引入第三方依赖。

### D3. SMS 接线**本次不做**

- 理由：`service.SMSConfig` 结构（`Provider/AccessKey/SecretKey/SignName/Region/Endpoint`）与 `config.SMSConfig`（`Aliyun/Tencent` 嵌套结构）**不匹配**，需要额外的映射适配层；且用户当前聚焦「邮件集成」。SMS 作为独立后续任务单独立项。

### D4. `AuthService.baseURL` 从 config 注入

- 在 `config` 里新增一个可选的 `frontend_url` 配置项（`ServerConfig` 或顶层新字段），接线时 `authService.SetBaseURL(cfg.XXX)`。
- 若无配置则保留现有默认值（向后兼容）。

### D5. 发信失败不新增重试队列

- 沿用现有机制：`TicketNotificationService.SendNotification` 已经把通知记录状态置为 `sent`/`failed`（`ticket_notification_service.go:181-184`），发信失败只记日志 + 置 failed，不做异步重试队列。这符合现有架构风格，重试队列是独立话题。

### D6. `enabled=false` 时不构造 EmailService（或构造但置 nil 语义）

- `cfg.SMTP.Enabled` 为 false 时，跳过 `EmailService` 构造（不调用 `SetEmailService`），通知服务继续走站内信（in_app）路径。
- 这样 `enabled` 开关真正生效，且不引入"构造了但配置为空"的中间态。

---

## 4. 目标架构（改动点）

### 改动点 1：`service/email_service.go` — 扩展 `EmailConfig` + 加密模式

- `EmailConfig` 新增字段：`Encryption string`（`tls`/`ssl`/`none`，默认 `tls`）、`SkipVerify bool`。
- `Send` 方法：把 `smtp.SendMail` 替换为手动 dial 流程，按 `Encryption` 分支处理，`SkipVerify` 透传到 `tls.Config`。
- `ValidateConfig`：`Encryption` 非法值时报错（可选）。

### 改动点 2：`internal/bootstrap/app.go` — 构造 + 注入

在 `ticketNotificationService` 构造（第 238 行）之后、`ticketService` 构造（第 243 行）之前插入：

```go
// 邮件通知（SMTP 发信）—— enabled=false 时不接线，通知仅走站内信
if cfg.SMTP.Enabled {
    emailService := service.NewEmailService(service.EmailConfig{
        Host:       cfg.SMTP.Host,
        Port:       cfg.SMTP.Port,
        Username:   cfg.SMTP.Username,
        Password:   cfg.SMTP.Password,
        From:       cfg.SMTP.FromEmail,
        FromName:   cfg.SMTP.FromName,
        Encryption: cfg.SMTP.Encryption,
        SkipVerify: cfg.SMTP.SkipVerify,
    }, sugar)
    ticketNotificationService.SetEmailService(emailService)
    // authService 在稍后（第 572 行）才构造，这里需要把 emailService 暂存或在 authService 构造后注入
}
```

> 注：`authService` 在 `app.go:572` 才构造，晚于 `ticketNotificationService`。实现时需在 authService 构造后再调用 `authService.SetEmailService(emailService)`（需要把 `emailService` 变量作用域提到能覆盖 572 行的位置，或拆成两段注入）。

### 改动点 3：`internal/bootstrap/app.go` — `authService.SetEmailService` + `SetBaseURL`

在 `authService` 构造（第 572 行）后：

```go
if cfg.SMTP.Enabled {
    authService.SetEmailService(emailService)
}
if cfg.Server.FrontendURL != "" {
    authService.SetBaseURL(cfg.Server.FrontendURL)
}
```

### 改动点 4：`config` — 新增 `frontend_url` 配置项

- `ServerConfig` 新增 `FrontendURL string`（`mapstructure:"frontend_url"`），支持 env `FRONTEND_URL` 覆盖。
- `config.yaml` 的 `server:` 段新增 `frontend_url: "${FRONTEND_URL:http://localhost:3000}"`。

---

## 5. 测试计划

### 5.1 `service/email_service_test.go`（新增/扩展）

- **加密模式分支**（用 `httptest` 或 mock SMTP server）：
  - `none`：断言不触发 TLS 升级
  - `tls`（STARTTLS）：断言触发 STARTTLS
  - `ssl`（隐式 TLS）：断言走 `tls.Dial`
  - `skip_verify=true`：断言 `InsecureSkipVerify` 生效
- **`ValidateConfig`**：非法 `Encryption` 值报错。
- 现有 `EmailConfig` 字段新增后，确认旧构造方式（无 Encryption 字段）仍编译（默认值兜底）。

### 5.2 `internal/bootstrap` 接线验证

- 用 `SMTP.Enabled=false` 启动：确认不构造 `EmailService`、通知服务正常走站内信。
- 用 `SMTP.Enabled=true` + mock SMTP server：确认 `ticketNotificationService.emailService` 非 nil、`authService.emailService` 非 nil。
- 密码重置：确认 `authService.baseURL` 来自 `FRONTEND_URL`。

### 5.3 回归

- `go build ./...` 通过（注意：当前有 `handlers/known_error/handler.go:577` 缺 `service` import 的**既有编译错误**，与本 spec 无关，需先修或标注）。
- `go test ./service/... ./internal/bootstrap/...` 通过。

---

## 6. 范围与取舍（明确排除）

| 排除项 | 理由 |
|---|---|
| SMS 接线 | `SMSConfig` 结构不匹配，需额外适配；用户聚焦邮件（决策 D3） |
| `internal/container/container.go` 清理 | 死代码，单独话题，避免范围膨胀 |
| 发信异步重试队列 | 沿用现有 `sent`/`failed` 状态机制（决策 D5） |
| 邮件建单（MS Graph） | 已完整落地，不在本文档 |
| 邮件模板的可视化编辑 UI | 现有 `SendTemplate` 用 Go template，UI 是独立话题 |

---

## 7. 与 AGENTS.md 对齐

| 原则 | 对齐 |
|---|---|
| 不引入新的基础设施/依赖 | 用标准库 `net/smtp`，不引入第三方邮件库 |
| 配置从 config 来，不硬编码 | `frontend_url`、SMTP 全字段从 config/env 来（决策 D2/D4） |
| 现有机制优先 | 复用 `SetEmailService`/`SetBaseURL` 既有接口，不新增注入模式 |
| 开关真正生效 | `enabled=false` 跳过构造（决策 D6） |
| 企业级可观测 | 发信失败记日志 + 通知记录置 failed，可追踪 |
