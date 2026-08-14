# 邮件附件处理 + MinIO 接线设计文档

> **日期：** 2026-08-13
> **状态：** 待 review
> **前置：** 邮件建单 + 回复线程追踪已落地并验收通过。本文档补齐「邮件附件/图片」的下载与关联工单，并**一并接入 MinIO 对象存储**（用户决策方案 A）。

---

## 1. 背景与问题

### 1.1 问题陈述

邮件建单目前**完全忽略附件**：`PollDelta` 只解析正文，不解析 `hasAttachments`，也不下载附件。因此用户邮件带 PDF/图片等附件时，建单后附件丢失；正文内嵌图片（inline）因正文纯文本化而彻底丢失。

### 1.2 期望行为

1. 附件下载并保存，关联到新建工单
2. inline 图片也作为附件保留
3. 附件下载失败不阻断建单（尽力而为）
4. 附件存储到 **MinIO**（对象存储，生产可用）

---

## 2. 现状核实（2026-08-13，读码 + Graph 实测）

### 2.1 邮件附件链路

| 项 | 现状 |
|---|---|
| `PollDelta` 解析附件 | ❌ 未解析 `hasAttachments` |
| Graph attachments 端点 | ✅ `GET /messages/{id}/attachments` → `{value: [...]}` |
| Graph `$value` 端点 | ✅ `GET /messages/{id}/attachments/{attachmentId}/$value` → 二进制 |
| `contentBytes` 限制 | <3MB 返回 base64；更大附件该字段为空，走 `$value` |

### 2.2 附件存储现状

| 项 | 现状 |
|---|---|
| `TicketAttachmentService` | ✅ bootstrap `app.go:369` 已构造 |
| 存储 | 本地 `uploads/tickets`（`os.Create` / `os.Open` / `os.Remove`） |
| `UploadAttachment` | 接受 `FileHeader{Filename, Size, ContentType, Reader io.Reader}`，Reader 抽象可复用 |
| 病毒扫描 | `AttachmentVirusScanner.Scan(ctx, filePath string)`，**依赖本地文件路径**，当前 noop |
| 大小/类型限制 | 10MB；图片/PDF/Office/文本/压缩白名单 |

### 2.3 MinIO 基础设施现状

| 项 | 现状 |
|---|---|
| docker-compose.dev.yml | 定义 `itsm-minio-dev`（`MINIO_ROOT_USER=minioadmin` / `MINIO_ROOT_PASSWORD=minioadmin123`，端口 `9010:9000`） |
| 后端环境变量 | `MINIO_ENDPOINT` / `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` / `MINIO_BUCKET=itsm-uploads` |
| **实际容器状态** | ⚠️ `itsm-minio-dev` 容器**无端口映射、无网络**（孤立），宿主机后端访问不到 |
| config 结构体 | ❌ 无 MinIO 字段 |
| go.mod | ❌ 无 minio-go 依赖 |

> **环境前置**：实施前需修复 `itsm-minio-dev` 启动（`docker compose up -d minio` 使其正确映射端口），否则接线后连不上。

---

## 3. 决策记录

### D1. 附件大小支持到 10MB（需 `$value` 端点）

- `<3MB` 用 `contentBytes`（base64 解码）；`>=3MB` 走 `$value` 下载二进制。对齐 ITSM `maxFileSize=10MB`。

### D2. inline 图片作为普通附件保留

- 正文已纯文本化，图片无法内嵌，统一作为附件下载。

### D3. 附件下载失败容忍，不阻断建单

- 先建单后尽力下载；单个附件失败记日志，批量失败追加系统评论汇总。

### D4. 复用 `TicketAttachmentService`（不新建存储机制）

- 附件下载通过 `TicketStore.SaveAttachment`，wiring 构造 `FileHeader{Reader: bytes.NewReader(data)}` 复用 `UploadAttachment`。

### D5. 附件下载同步执行（建单成功后、回信前）

- 独立 goroutine，不阻塞其他租户，受「容忍失败」约束。

### D6. MinIO 通过 `AttachmentStorage` 接口接入，Local/Minio 双实现

- 新增 `AttachmentStorage` 接口抽象存储：`Save` / `Open` / `Delete`。
- `LocalAttachmentStorage`（现有本地逻辑）+ `MinioAttachmentStorage`（minio-go）两个实现。
- `TicketAttachmentService` 注入该接口，根据 config 选择实现，**不破坏现有本地存储逻辑**。

### D7. `file_path` 字段语义统一为 object key

- 本地存储时 `file_path` 仍是本地路径；MinIO 时 `file_path` 存 object key（如 `tickets/23_xxx.pdf`）。
- 前端访问仍走 `file_url`（HTTP 下载 API），不受存储后端影响。

### D8. 病毒扫描接口从「文件路径」改为「内容 reader」

- 现状 `Scan(ctx, filePath string)` 依赖本地文件；MinIO 场景无本地路径。
- 改为 `Scan(ctx, reader io.Reader, size int64) error`，存储后端无关。当前 noop 实现，改动影响小。

---

## 4. 目标架构

### Part 1：邮件附件下载（复用 TicketAttachmentService）

#### 改动点 1：`client.go` — 解析 hasAttachments + 附件下载

- `deltaMessage` 加 `HasAttachments bool \`json:"hasAttachments"\``；`Message` 加 `HasAttachments bool`；`PollDelta` 映射。
- 新增：
  ```go
  type Attachment struct {
      ID          string
      Name        string
      ContentType string
      Size        int
      IsInline    bool
      Data        []byte // <3MB 附件已 base64 解码；大附件为 nil
  }
  func (c *Client) ListAttachments(ctx, mailbox, messageID) ([]Attachment, error)
  func (c *Client) DownloadAttachment(ctx, mailbox, messageID, attachmentID) ([]byte, error) // $value
  ```

#### 改动点 2：`coordinator.go` — 建单后下载附件

- 建单成功、AI 评论后、回信前插入 `if m.HasAttachments { c.saveAttachments(...) }`。
- `saveAttachments`：列附件 → 逐个取数据（小附件用 Data，大附件走 `DownloadAttachment`）→ `store.SaveAttachment(...)` → 失败计数 → 有失败则追加汇总评论。

#### 改动点 3：`TicketStore` 接口加 `SaveAttachment`

```go
SaveAttachment(ctx, tenantID, ticketID, uploaderID int, name, contentType string, data []byte) error
```

#### 改动点 4：`email_msgraph_wiring.go` — 实现 SaveAttachment

`ticketStoreAdapter` 注入 `attachmentService *service.TicketAttachmentService`，实现：
```go
func (a *ticketStoreAdapter) SaveAttachment(ctx, tenantID, ticketID, uploaderID int, name, contentType string, data []byte) error {
    _, err := a.attachmentService.UploadAttachment(ctx, ticketID, &service.FileHeader{
        Filename: name, Size: int64(len(data)), ContentType: contentType, Reader: bytes.NewReader(data),
    }, uploaderID, tenantID)
    return err
}
```

### Part 2：MinIO 接线

#### 改动点 5：引入依赖 + config

- `go.mod` 加 `github.com/minio/minio-go/v7`。
- `config` 加 `MinIOConfig{Endpoint, AccessKey, SecretKey, Bucket string, UseSSL bool}`，支持 `MINIO_ENDPOINT` / `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` / `MINIO_BUCKET` 环境变量。
- `config.yaml` 加 `minio:` 段。

#### 改动点 6：新增 `service/attachment_storage.go`

```go
type AttachmentStorage interface {
    Save(ctx context.Context, key string, reader io.Reader, size int64) error
    Open(ctx context.Context, key string) (io.ReadCloser, int64, error)
    Delete(ctx context.Context, key string) error
}
```

- `LocalAttachmentStorage{uploadDir}`：`os.Create`/`os.Open`/`os.Remove`。
- `MinioAttachmentStorage{client, bucket}`：`PutObject`/`GetObject`/`RemoveObject`；构造时 `MakeBucket` 确保 bucket 存在。

#### 改动点 7：`TicketAttachmentService` 改造

- 结构体加 `storage AttachmentStorage` 字段（构造时注入，默认 Local）。
- `UploadAttachment`：`saveFile(fileHeader, filePath)` → `storage.Save(ctx, key, reader, size)`。
- `GetAttachmentFile`：`os.Open(filePath)` → `storage.Open(ctx, key)`。
- `DeleteAttachment`：`os.Remove(filePath)` → `storage.Delete(ctx, key)`。
- `virusScanner.Scan(ctx, filePath)` → `Scan(ctx, reader, size)`（决策 D8）。
- `file_path` 字段：本地存路径、MinIO 存 object key（统一以 `key` 传入）。

#### 改动点 8：`bootstrap` 接线

- 根据 `cfg.MinIO.Endpoint` 是否为空，构造 `LocalAttachmentStorage` 或 `MinioAttachmentStorage`。
- `NewTicketAttachmentService` 增加 storage 参数（或 setter），注入。

#### 改动点 9：测试

- `client_test.go`：`ListAttachments`（contentBytes 解析 + 大附件 Data nil）、`DownloadAttachment`（$value 二进制）。
- `coordinator_test.go`：`fakeStore.SaveAttachment`；有附件保存/大附件 $value/失败不阻断。
- `service`：`MinioAttachmentStorage`（用 minio 的 mock 或 fake endpoint）、`LocalAttachmentStorage` 回归。
- `email_msgraph_wiring_test.go`：`SaveAttachment` 复用 `UploadAttachment`。

---

## 5. 测试计划

| 用例 | 期望 |
|---|---|
| 邮件带 <3MB 附件 | 建单后保存（MinIO object key 落库） |
| 邮件带 >3MB 附件 | $value 下载并保存 |
| inline 图片 | 作为普通附件保存 |
| 附件下载失败 | 建单成功 + 汇总评论 |
| 附件超 10MB / 类型不允许 | 跳过 + 汇总评论 |
| MinIO 存储 | Save/Open/Delete 走 PutObject/GetObject/RemoveObject |
| 本地存储回归 | 无 MinIO 配置时仍走本地 |

## 6. 范围与取舍（明确排除）

| 排除项 | 理由 |
|---|---|
| 附件正文内嵌显示 | 正文纯文本化，图片只能作附件（D2） |
| 附件异步下载 | 同步 + 容忍失败（D5） |
| 病毒扫描接真实引擎 | 现有 noop，真实扫描是独立安全任务（仅调整接口签名，不接引擎） |
| MinIO 多桶/生命周期策略 | 单 bucket `itsm-uploads` 够用 |

## 7. 与 AGENTS.md 对齐

| 原则 | 对齐 |
|---|---|
| 不重复造轮子 | 复用 TicketAttachmentService + 现有附件表 |
| 存储后端抽象 | `AttachmentStorage` 接口隔离 Local/Minio，不硬编码 |
| 边界兜底 | 附件失败不阻断建单，超限/类型不符跳过+评论 |
| 可观测 | 每个附件成功/失败结构化日志 + 失败汇总评论 |
