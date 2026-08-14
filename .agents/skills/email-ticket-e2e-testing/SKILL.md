---
name: email-ticket-e2e-testing
description: Use when testing the ITSM email integration end-to-end — email-to-ticket creation via MS Graph, reply threading via conversationId, attachment handling via MinIO, and email notifications via Graph sendMail. Covers environment prerequisites, connector provisioning, Graph API test commands, database/log/MinIO verification steps, and known pitfalls.
---

# 邮件建单端到端测试

## 概述

ITSM 邮件集成闭环的完整端到端测试流程，覆盖四条链路：

1. **邮件建单**：收信 → AI 分派（LLM）→ 建单 → 自动回信（`Re: [TKT-xxx]`）
2. **回复线程追踪**：用户回复确认信 → 按 `conversationId` 匹配 → 追加评论，**不重复建单**
3. **附件处理**：Graph 下载附件 → base64 解码 → MinIO 存储 → HTTP 下载
4. **邮件通知发信**：密码重置 / SLA 告警 → Graph sendMail

测试通过「Graph API 模拟发信 + 观察后端日志/数据库/MinIO」完成，无需真实用户在邮件客户端操作。

---

## 前置条件

| 组件 | 说明 | 端口/位置 |
|------|------|-----------|
| 后端 | 本地 `go run main.go` 或已编译二进制 | `localhost:8090` |
| PostgreSQL | Docker 容器 `itsm-postgres-dev` | `localhost:5432` |
| MinIO | Docker 容器 `itsm-minio-dev` | 宿主机 `localhost:9012`（API） |
| 连接器 | `msgraph-email`（需 provision，见下） | — |
| 测试邮箱 | 发件人（如 `Julian@...`）+ 共享邮箱（`ai-support@...`） | — |

**凭证来源**：从 `itsm-backend/.env`（MinIO/DB 等）和连接器配置（Azure 凭证）读取，不硬编码到本 skill。

> ⚠️ 后端每次重启后连接器配置会丢失，必须重新 provision（见下）。

---

## 连接器 provision

后端重启后，重新 provision `msgraph-email` 连接器：

```bash
TOKEN=$(curl -s -X POST http://localhost:8090/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<ADMIN_PASSWORD>"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('data',{}).get('accessToken',''))")

curl -s -X POST http://localhost:8090/api/v1/connectors/configs \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "msgraph-email",
    "provider": "microsoft",
    "enabled": true,
    "settings": {"azure_tenant_id": "<AZURE_TENANT_ID>", "mailbox": "<SHARED_MAILBOX>", "poll_interval_seconds": 60},
    "credentials": {"azure_client_id": "<AZURE_CLIENT_ID>", "azure_client_secret": "<AZURE_CLIENT_SECRET>"}
  }'
```

预期：`provision: 0 | healthy: True`。

---

## 获取 Graph token（app-only）

模拟用户发信、查询收件箱、发带附件邮件都需要 Graph token：

```bash
GTOKEN=$(curl -s -X POST "https://login.microsoftonline.com/<AZURE_TENANT_ID>/oauth2/v2.0/token" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'client_id=<AZURE_CLIENT_ID>' \
  -d 'client_secret=<AZURE_CLIENT_SECRET>' \
  -d 'scope=https://graph.microsoft.com/.default' \
  -d 'grant_type=client_credentials' \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('access_token',''))")
```

---

## 场景 1：邮件建单

**目的**：验证收信 → AI 分派 → 建单 → 回信全链路。

**步骤 1 — 从测试发件人发信到共享邮箱**：

```bash
curl -s -X POST "https://graph.microsoft.com/v1.0/users/<TEST_SENDER>/sendMail" \
  -H "Authorization: Bearer $GTOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "subject": "<测试主题，带明确分类特征，如 数据库连接超时>",
      "body": {"contentType": "Text", "content": "<测试正文>"},
      "toRecipients": [{"emailAddress": {"address": "<SHARED_MAILBOX>"}}]
    }
  }' -w "\nHTTP:%{http_code}\n"
```

预期 `202`。

**步骤 2 — 等待协调器轮询**（默认 `poll_interval_seconds=60`）：

```bash
sleep 70
```

**步骤 3 — 检查日志**（后端日志，`go run` 时在终端，`nohup` 时在 `/tmp/itsm-backend.log`）：

```bash
grep -iE 'Ticket created|AI 分派|msgraph ticket created' <LOG_FILE> | tail -5
```

**验证点**：
- `Ticket created` 出现，`ticket_id`、`ticket_number` 正确
- 工单 description 是**纯文本**（不是 HTML 代码）—— `Prefer: outlook.body-content-type="text"` 头生效
- AI 分派评论包含分类/优先级/置信度

**步骤 4 — 检查数据库**：

```bash
docker exec itsm-postgres-dev psql -U itsm_user -d itsm -c \
  "SELECT id, title, description, priority, source, external_message_id, conversation_id FROM tickets ORDER BY id DESC LIMIT 1;"
```

**验证点**：
- `source = email`
- `description` 是纯文本
- `external_message_id`（internetMessageId）已存（去重用）
- `conversation_id`（Graph conversationId）已存（回复追踪用）

**步骤 5 — 验证回信**：测试发件人邮箱收到 `Re: [TKT-xxx] <主题>` 确认信。

---

## 场景 2：回复线程追踪

**目的**：用户回复确认信 → 追加评论，**不新建工单**。

**步骤 1 — 回复确认信**（测试发件人回复共享邮箱，主题带 `Re: [TKT-xxx]`，或直接 reply）：

```bash
# 方式 A：用户直接回复（真实邮件客户端）后等待轮询
# 方式 B：用 Graph reply API 模拟回复原邮件（自动延续 conversation）
curl -s -X POST "https://graph.microsoft.com/v1.0/users/<SHARED_MAILBOX>/messages/<ORIGINAL_MESSAGE_ID>/reply" \
  -H "Authorization: Bearer $GTOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"comment": "<回复内容>"}' -w "\nHTTP:%{http_code}\n"
```

**步骤 2 — 等待轮询后检查日志**：

```bash
grep -iE 'reply appended|msgraph ticket created' <LOG_FILE> | tail -5
```

**验证点**：
- 出现 `reply appended to existing ticket`（**不是** `ticket created`）
- 数据库没有新建工单

**步骤 3 — 检查评论**：

```bash
docker exec itsm-postgres-dev psql -U itsm_user -d itsm -c \
  "SELECT content, is_internal FROM ticket_comments WHERE ticket_id=<TICKET_ID> ORDER BY id;"
```

**验证点**：新增 `[邮件回复] ...` 评论，`is_internal = false`（用户可见）。

> ⚠️ **关键陷阱**：`message.conversationId` 是 Graph **只读属性**，`sendMail` 指定它会被静默忽略。回信必须用 **`reply` API**（`POST /messages/{id}/reply`）才能延续对话线程，否则用户回复的 `conversationId` 会变化、匹配失败、重复建单。

---

## 场景 3：附件处理

**目的**：带附件邮件 → Graph 下载 → MinIO 存储 → HTTP 下载。

**步骤 1 — 发带附件邮件**（`fileAttachment`，`contentBytes` 为 base64）：

```bash
ATT_B64=$(python3 -c "import base64; print(base64.b64encode('<附件内容>'.encode()).decode())")

curl -s -X POST "https://graph.microsoft.com/v1.0/users/<TEST_SENDER>/sendMail" \
  -H "Authorization: Bearer $GTOKEN" \
  -H 'Content-Type: application/json' \
  -d "{
    \"message\": {
      \"subject\": \"<主题>附件测试\",
      \"body\": {\"contentType\": \"Text\", \"content\": \"<正文>\"},
      \"toRecipients\": [{\"emailAddress\": {\"address\": \"<SHARED_MAILBOX>\"}}],
      \"attachments\": [
        {\"@odata.type\": \"#microsoft.graph.fileAttachment\", \"name\": \"<文件名>.txt\", \"contentType\": \"text/plain\", \"contentBytes\": \"$ATT_B64\"}
      ]
    }
  }" -w "\nHTTP:%{http_code}\n"
```

**步骤 2 — 等待轮询后检查日志**：

```bash
grep -iE 'Uploading attachment|save attachment failed' <LOG_FILE> | tail -5
```

**验证点**：出现 `Uploading attachment`，**无** `save attachment failed`。

**步骤 3 — 检查数据库附件记录**：

```bash
docker exec itsm-postgres-dev psql -U itsm_user -d itsm -c \
  "SELECT id, ticket_id, file_name, file_path, file_size, file_type FROM ticket_attachments WHERE ticket_id=<TICKET_ID>;"
```

**验证点**：`file_path` 是 object key（形如 `tickets/<ticketID>_<nano>_<name>`）。

**步骤 4 — 验证 MinIO 对象可读回**（用 minio-go 脚本）：

```bash
cat > /tmp/verify_minio.go << 'EOF'
package main
import ("context";"fmt";"io";"log"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials")
func main(){
	c,_:=minio.New("localhost:9012",&minio.Options{Creds:credentials.NewStaticV4("minioadmin","minioadmin123",""),Secure:false})
	obj,_:=c.GetObject(context.Background(),"itsm-uploads","<FILE_PATH>",minio.GetObjectOptions{})
	d,_:=io.ReadAll(obj); fmt.Println(string(d))
}
EOF
cd itsm-backend && go run /tmp/verify_minio.go
```

**验证点**：读回内容与原附件一致。

**步骤 5 — 验证 HTTP 下载**（前端用 `GET /api/v1/tickets/{id}/attachments/{attachment_id}`）：

```bash
curl -s "http://localhost:8090/api/v1/tickets/<TICKET_ID>/attachments/<ATTACHMENT_ID>" \
  -H "Authorization: Bearer $TOKEN"
```

**验证点**：返回附件内容，`200`。

> ⚠️ **关键陷阱**：`http.DetectContentType` 对 UTF-8 文本返回 `text/plain; charset=utf-8`（带 charset），会因不在白名单而误拒。代码已用 `mime.ParseMediaType` 规范化，但若遇到「detected file type not allowed」需检查此处。

---

## 场景 4：邮件通知发信

**目的**：密码重置 / SLA 告警走 Graph sendMail 发信。

**步骤 1 — 触发密码重置**：

```bash
curl -s -X POST "http://localhost:8090/api/v1/auth/forgot-password" \
  -H 'Content-Type: application/json' \
  -d '{"email": "<已注册用户邮箱>"}'
```

**步骤 2 — 检查日志**：

```bash
grep -iE 'Email sent via Graph|Failed to send email via Graph' <LOG_FILE> | tail -5
```

**验证点**：出现 `Email sent via Graph`（走 Graph sendMail），**无** `Failed to send email via Graph`。

> 邮件通知默认走 Graph sendMail（`EmailService` 的 `graphProvider` 延迟绑定 `msgraph-email` 连接器）；连接器未 provision 时才 fallback 到 SMTP。

---

## 验证命令速查

| 目的 | 命令 |
|------|------|
| 后端健康 | `curl -s http://localhost:8090/api/v1/health` |
| 日志建单/回复/附件/通知 | `grep -iE 'ticket created\|reply appended\|Uploading attachment\|Email sent via Graph' <LOG_FILE>` |
| 查最新 email 工单 | `docker exec itsm-postgres-dev psql -U itsm_user -d itsm -c "SELECT id,title,conversation_id FROM tickets WHERE source='email' ORDER BY id DESC LIMIT 3;"` |
| 查附件记录 | `... -c "SELECT * FROM ticket_attachments WHERE ticket_id=<ID>;"` |
| 查评论 | `... -c "SELECT content,is_internal FROM ticket_comments WHERE ticket_id=<ID> ORDER BY id;"` |
| MinIO 对象读回 | minio-go 脚本（见场景 3 步骤 4） |

---

## 常见问题（已知坑）

| 问题 | 现象 | 原因 / 解决 |
|------|------|-------------|
| `conversationId` 只读 | 回复被识别为新工单（重复建单） | `sendMail` 指定 `conversationId` 无效，回信改用 `reply` API |
| MIME charset 误拒 | `save attachment failed: detected file type not allowed: text/plain; charset=utf-8` | 白名单匹配前需 `mime.ParseMediaType` 规范化 |
| 附件下载 404 | `GET /api/v1/tickets/{id}/attachments/{id}` 404 | 下载路由需注册（`router.go` 补 `DownloadAttachment`/`PreviewAttachment`） |
| MinIO 端口冲突 | `failed to bind host port 9010` | 9010 被 WebSocket 服务占用，改映射 `9012:9000` |
| 连接器重启丢失 | 邮件不再建单 | 后端重启后重新 provision `msgraph-email` |
| 邮件正文 HTML 污染 | description 显示 HTML 代码 | 需 `Prefer: outlook.body-content-type="text"` 头请求纯文本 |
| 回信 400 | `Auto-Submitted should start with x-` | Graph 只接受 `x-` 前缀自定义头，用 `X-Auto-Submitted` |
