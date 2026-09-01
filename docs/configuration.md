# Configuration Reference

## Environment Variables

All configuration is done via environment variables. See `.env.prod.example` for production settings.

### Backend

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `DB_HOST` | PostgreSQL host | Yes | localhost |
| `DB_PORT` | PostgreSQL port | No | 5432 |
| `DB_USER` | Database user | Yes | postgres |
| `DB_PASSWORD` | Database password | Yes | - |
| `DB_NAME` | Database name | No | itsm |
| `REDIS_HOST` | Redis host | Yes | localhost |
| `REDIS_PORT` | Redis port | No | 6379 |
| `REDIS_PASSWORD` | Redis password | No | - |
| `JWT_SECRET` | JWT signing key (min 32 chars) | Yes | - |
| `LOG_LEVEL` | Log level: debug/info/warn/error | No | info |
| `PORT` | Backend HTTP port | No | 8090 |
| `ENABLE_SWAGGER` | Enable Swagger UI | No | true |
| `CORS_ALLOWED_ORIGINS` | CORS allowed origins (comma-separated) | No | * |
| `ITSM_ALLOW_ALL_ORIGINS` | Allow all CORS origins | No | false |

Redis is the authoritative one-time-consumption store for refresh tokens. If
Redis is missing or unavailable, login and already-issued access tokens keep
their normal behavior, but refresh requests fail closed with HTTP `503` and
code `5003`; the backend never falls back to reusable refresh tokens.

### AI / LLM

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `OPENAI_API_KEY` | OpenAI API key | No | - |
| `OPENAI_BASE_URL` | OpenAI API base URL | No | https://api.openai.com/v1 |
| `MINIMAX_API_KEY` | MiniMax API key | No | - |
| `MINIMAX_BASE_URL` | MiniMax API base URL | No | https://api.minimax.chat/v1 |
| `NEXT_PUBLIC_ENABLE_AI` | Enable AI features in frontend | No | true |

### Object Storage (MinIO)

附件存储后端（邮件附件等）。

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `MINIO_ENDPOINT` | MinIO server endpoint | No | minio:9000 |
| `MINIO_ROOT_USER` | MinIO root user (access key) | Yes | minioadmin |
| `MINIO_ROOT_PASSWORD` | MinIO root password (secret key) | Yes | minioadmin123 |
| `MINIO_BUCKET` | MinIO bucket name | No | itsm-uploads |

### Email Notification

邮件通知（工单通知、SLA 告警、密码重置）优先通过 **Microsoft Graph sendMail** 发送（复用 `msgraph-email` 连接器，适用于 Exchange Online），SMTP 仅作为 fallback（Exchange Online 已禁用 SMTP Basic Auth）。

`msgraph-email` 连接器配置已持久化到数据库（`connector_configs` 表），后端重启后自动恢复，无需手动重新 provision。连接器配置通过 `POST /api/v1/connectors/configs` 提交（含 `azure_tenant_id`、`azure_client_id`、`azure_client_secret`、`mailbox` 等）。

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `SMTP_HOST` | SMTP server host（fallback） | No | - |
| `SMTP_PORT` | SMTP port（fallback） | No | 587 |
| `SMTP_USERNAME` | SMTP username（fallback） | No | - |
| `SMTP_PASSWORD` | SMTP password（fallback） | No | - |
| `SMTP_FROM` | From email address（fallback） | No | noreply@itsm.local |
| `FRONTEND_URL` | 前端地址（密码重置链接等） | No | http://localhost:3000 |

Incident alert email delivery is accepted transactionally through `outbox_events`; API success means durable acceptance, not external delivery completion. The shared worker owns lease recovery, retry, blocked, and dead-letter states.

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `OUTBOX_DELIVERY_BATCH_SIZE` | Maximum deliveries claimed per registered event type and poll | No | 20 |
| `OUTBOX_DELIVERY_POLL_INTERVAL` | Shared delivery worker polling interval | No | 5s |
| `OUTBOX_DELIVERY_HANDLER_TIMEOUT` | Timeout for one external delivery call | No | 5s |
| `OUTBOX_DELIVERY_MAX_ATTEMPTS` | Retry attempts before dead-letter | No | 5 |

### Notifications

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `DINGTALK_WEBHOOK` | DingTalk robot webhook URL | No | - |
| `WECOM_WEBHOOK` | WeCom robot webhook URL | No | - |

## Next.js (Frontend)

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `NEXT_PUBLIC_API_URL` | Backend API URL | Yes | http://localhost:8090 |
| `NEXT_PUBLIC_ENABLE_AI` | Enable AI features | No | true |

## Configuration Files

### Backend (config.yaml)

The backend also supports `config.yaml` for static configuration:

```yaml
server:
  port: 8090
  log_level: info

database:
  host: localhost
  port: 5432
  user: postgres
  password: your-password
  name: itsm

redis:
  host: localhost
  port: 6379
```

Environment variables take precedence over `config.yaml`.

### Docker Compose Override

For Docker deployments, create `docker-compose.override.yml`:

```yaml
version: '3.8'
services:
  itsm-backend:
    environment:
      DB_HOST: postgres
      REDIS_HOST: redis
```

## Feature Flags

| Flag | Description |
|------|-------------|
| `ENABLE_SWAGGER` | Swagger API docs at /swagger |
| `ENABLE_AI_TRIAGE` | AI-powered ticket triage |
| `ENABLE_AI_SUMMARY` | AI-generated ticket summaries |
| `ENABLE_RAG` | RAG-based knowledge base search |

## 权限模型（RBAC）

权限运行时以**数据库**（`roles` + `role_permissions` + `permissions` 表）为唯一权威，统一使用 `DBOnly` 模式（开发/生产一致），权限由 seeder 初始化。

- `super_admin`（平台超管）：代码级放行，跨租户运维，不查数据库。
- `sysadmin`（租户系统管理员）：数据库角色，拥有租户内所有权限。
- 其他业务角色（`it_director`/`dept_manager`/`l1_support`/`end_user` 等）：权限由数据库角色-权限关联决定。

硬编码 `RolePermissions`（`middleware/rbac.go`）已收敛为最小兜底集（super_admin + end_user + msp_*），仅在数据库未初始化时防御性兜底。详见 `docs/superpowers/specs/2026-08-14-permission-system-consolidation-design.md`。
