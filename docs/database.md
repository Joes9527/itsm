# Database Guide

## Schema Overview

The ITSM system uses PostgreSQL 15+ with the following main entities:

```
users ────────┬───── user_roles ─────── roles
              │
tickets ──────┼───── ticket_comments ─── attachments
              │
incidents ────┼───── change_records
              │
problems ─────┤
              │
kb_articles ──┴───── knowledge_tags
```

## Ent ORM

The project uses [Ent](https://entgo.io/) as the ORM. Schemas are defined in `itsm-backend/ent/schema/`.

### Generate Code

```bash
cd itsm-backend
go generate ./ent/schema/...
```

### Create Migration

```bash
go generate ent new MigrationName
go generate ./ent
```

### Apply Post-schema Migrations

```bash
# This only applies the registered post-schema stream. The deployment
# bootstrap owns Ent Schema.Create and must have completed first.
go run -tags migrate ./cmd/migrate -up
```

For a disposable development or test database only, `-fresh` performs the
canonical order: create the Ent schema, apply the post-schema stream, then
seed. It requires both an allowed development mode and exact confirmation of
the configured database name:

```bash
ITSM_ALLOW_DESTRUCTIVE_FRESH=true ITSM_FRESH_DATABASE="$DB_NAME" \
  go run -tags migrate ./cmd/migrate -fresh
```

Do not use `-fresh` for a shared or production database.

### Recent Migrations

| 文件 | 说明 |
|------|------|
| `20260811_end_user_ticket_category_read.sql` | 为所有租户的 `end_user` 角色授予 `ticket_category:read` 权限，修复服务目录为空的问题 |
| `20260810_service_catalog_itsm_type.sql` | `service_catalogs` 表新增 `itsm_type` 列，支持 ITSM 类型审批路由 |
| `20260813_ticket_conversation_id.sql` | `tickets` 表新增 `conversation_id` 列 + 唯一索引，用于邮件回复线程追踪 |

## Tables

### users

| Column | Type | Description |
|--------|------|-------------|
| id | uuid | Primary key |
| username | varchar(50) | Unique username |
| email | varchar(255) | Email address |
| password_hash | varchar(255) | Bcrypt hash |
| tenant_id | uuid | Multi-tenant support |
| created_at | timestamp | Creation time |
| updated_at | timestamp | Last update |

### tickets

| Column | Type | Description |
|--------|------|-------------|
| id | uuid | Primary key |
| title | varchar(255) | Ticket title |
| description | text | Full description |
| priority | int | 1=Low, 2=Medium, 3=High, 4=Critical |
| status | int | 0=Open, 1=In Progress, 2=Resolved, 3=Closed |
| category | varchar(50) | Category type |
| source | varchar | 工单来源（manual / service_catalog / email） |
| creator_email | varchar(255) | 邮件建单时的原始发件人邮箱 |
| external_message_id | varchar(255) | 外部消息ID（邮件 internetMessageId，用于建单去重） |
| conversation_id | varchar(255) | 邮件对话线程ID（Graph conversationId，用于回复追踪） |
| requester_id | uuid | FK to users |
| assignee_id | uuid | FK to users (nullable) |
| sla_deadline | timestamp | SLA target time |

### roles

角色表（RBAC 业务角色，如 sysadmin/it_director/dept_manager/l1_support/end_user 等）。

| Column | Type | Description |
|--------|------|-------------|
| id | bigint | Primary key |
| name | varchar | 角色显示名（中文） |
| code | varchar | 角色 code（如 sysadmin/dept_manager/end_user） |
| tenant_id | bigint | 租户ID |
| created_at | timestamptz | 创建时间 |
| updated_at | timestamptz | 更新时间 |

### permissions

权限定义表（resource + action，如 `user:read`）。

| Column | Type | Description |
|--------|------|-------------|
| id | bigint | Primary key |
| code | varchar | 权限 code（如 `user:read`） |
| name | varchar | 权限显示名（中文） |
| resource | varchar | 资源（如 user/ticket） |
| action | varchar | 操作（如 read/write/delete） |
| tenant_id | bigint | 租户ID |

### role_permissions

角色-权限关联表（多对多）。

| Column | Type | Description |
|--------|------|-------------|
| id | bigint | Primary key |
| role_id | bigint | FK → roles.id |
| permission_id | bigint | FK → permissions.id |
| tenant_id | bigint | 租户ID |

> 权限运行时以数据库（roles + role_permissions + permissions）为唯一权威；硬编码 `RolePermissions` 仅作 super_admin 代码级放行与 end_user 防御性兜底（见 `docs/superpowers/specs/2026-08-14-permission-system-consolidation-design.md`）。

### connector_configs

连接器配置持久化表（ent schema 自动建表，无需 SQL migration）。provision 时落库，后端重启后 `LoadAll` 自动恢复已启用的连接器（如 `msgraph-email`）。

| Column | Type | Description |
|--------|------|-------------|
| id | bigint | Primary key |
| tenant_id | bigint | 租户ID |
| name | varchar | 连接器名称（如 msgraph-email / feishu） |
| provider | varchar | 连接器类型（如 microsoft / feishu / dingtalk） |
| enabled | boolean | 是否启用 |
| credentials | text | 凭据 JSON（含 Azure client_secret，**待加密**） |
| settings | text | 设置 JSON |
| labels | text | 标签 JSON |
| created_at | timestamptz | 创建时间 |
| updated_at | timestamptz | 更新时间 |

## pgvector (Vector Search)

Vector similarity search is used for the AI-powered knowledge base:

```sql
-- Enable extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Knowledge articles with embeddings
ALTER TABLE kb_articles ADD COLUMN embedding vector(1536);
```

Note: `pgvector` requires PostgreSQL 15+. If not available, vector features are disabled gracefully.

## Indexes

Key indexes for performance:

```sql
-- Ticket lookup by status
CREATE INDEX idx_tickets_status ON tickets(status);

-- User ticket assignment
CREATE INDEX idx_tickets_assignee ON tickets(assignee_id);

-- SLA monitoring
CREATE INDEX idx_tickets_sla_deadline ON tickets(sla_deadline) WHERE status < 2;

-- Full-text search
CREATE INDEX idx_tickets_search ON tickets USING gin(to_tsvector('english', title || ' ' || description));
```

## Connection Pool

Default pool settings (tune for production):

```yaml
# Ent connection pool config
max_open_conns: 25
max_idle_conns: 5
conn_max_lifetime: 5m
```

## Backup

```bash
# Full database dump
pg_dump -U postgres -d itsm > backup_$(date +%Y%m%d).sql

# Compressed backup
pg_dump -U postgres -d itsm | gzip > backup_$(date +%Y%m%d).sql.gz

# Restore
psql -U postgres -d itsm < backup_20260101.sql
```
