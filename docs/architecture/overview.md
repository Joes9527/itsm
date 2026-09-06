# Architecture Overview

> **状态**：当前
> **更新日期**：2026-06-28

## 系统视图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Clients                                    │
│  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────────┐        │
│  │  Web (Next.js)  │  │  Mobile (PWA)   │  │  itsm-cli (CLI)  │        │
│  └────────┬────────┘  └────────┬────────┘  └────────┬─────────┘        │
│           │ HTTPS / WSS         │                    │                  │
└───────────┼──────────────────────┼────────────────────┼──────────────────┘
            │                      │                    │
            ▼                      ▼                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        Nginx (Reverse Proxy)                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                   │
│  │   /api/v1/*  │  │   /ws/*      │  │   /metrics   │                   │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘                   │
└─────────┼──────────────────┼──────────────────┼──────────────────────────┘
          │                  │                  │
          ▼                  ▼                  ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                  itsm-backend (Go 1.25 + Gin)                           │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  middleware/                                                     │   │
│  │    ├─ CORS / Security Headers / SQL Injection / XSS Protection  │   │
│  │    ├─ Rate Limiter (Redis / in-memory fallback)                 │   │
│  │    ├─ JWT Auth                                                   │   │
│  │    ├─ RBAC (role-based) + ABAC (expr-lang)                      │   │
│  │    ├─ Tenant Isolation                                           │   │
│  │    └─ MSP (multi-tenant service provider)                        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  controller/  (HTTP handlers — DTO only, never Ent models)     │   │
│  │    ├─ ticket / incident / problem / change                      │   │
│  │    ├─ cmdb / sla / knowledge / approval                        │   │
│  │    ├─ bpmn / workflow / dashboard / connector                   │   │
│  │    ├─ auth / user / role / permission / menu / tenant          │   │
│  │    └─ ai / analytics / prediction / search                      │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  service/  (business logic)                                     │   │
│  │    ├─ ticket_service / incident_service / change_service        │   │
│  │    ├─ cmdb_service / sla_service / knowledge_service           │   │
│  │    ├─ bpmn_* (engine / process / trigger / monitoring)          │   │
│  │    ├─ llm_gateway / rag_service / triage_service                │   │
│  │    ├─ escalation_service / sla_monitor_service                 │   │
│  │    └─ asset / vendor / survey / global_search / etc.            │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  cache/  (Redis abstraction — watermill pub/sub + key-value)   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  ent/  (ORM — schema → codegen → migrate)                       │   │
│  │    115+ entities: ticket / incident / problem / change / cmdb   │   │
│  │    / user / tenant / role / sla / bpmn / knowledge / etc.       │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────┬───────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                  Storage & External Services                            │
│  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌─────────────────┐    │
│  │ PostgreSQL │  │   Redis    │  │  pgvector   │  │  LLM Providers  │    │
│  │  (主存储)   │  │ (缓存/队列) │  │ (向量检索)  │  │  DeepSeek/OAI   │    │
│  └────────────┘  └────────────┘  └────────────┘  └─────────────────┘    │
│  ┌────────────┐  ┌────────────┐  ┌────────────────────────────────────┐  │
│  │ Prometheus │  │  Grafana   │  │  Connectors (飞书/Slack/钉钉/Teams) │  │
│  └────────────┘  └────────────┘  └────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

## 模块划分

### 后端 (itsm-backend)

- **`controller/`**：HTTP 入口，**只调用 service**，**不直接访问 DB**。返回 DTO，**禁止返回 Ent 模型**
- **`service/`**：业务编排，跨 controller / 跨 ent 操作
- **`ent/schema/`**：数据模型，**改 schema 必须 `go generate`**
- **`middleware/`**：横切关注点（auth / rbac / tenant / logging / metrics）
- **`dto/`**：请求/响应结构，使用 **camelCase 字段名**
- **`cache/`**：Redis 封装 + Watermill 消息流
- **`router/`**：路由注册（**仅 controller 层路由，业务分组路由在 controller 内 `RegisterRoutes` 方法**）

### 前端 (itsm-frontend)

- **`src/app/(main)/`**：受保护页面，基于角色视图分流（PortalLayout vs ConsoleLayout）：
  - **`/portal`**：员工自服务门户（宽屏极简无侧栏、AI 自愈拦截、服务目录、我的请求、经理审批）
  - **`/workspace/tickets`**：工程师协同工作台（三栏分屏、SLA 倒计时钟、360° 用户画像、AI 相似方案排障）
  - **`/manager/live-board`**：运营监控中心（实时进单监控墙、团队负载调度与 AI 突发故障聚类预警）
  - **`/executive/dashboard`**：战略决策大屏（MTTR 趋势分析、成本分布与 ChatBI 对话式数据洞察）
  - **`/admin/overview`**：系统管理后台（BPMN 引擎、连接器市场、组织架构与权限管理）
- **`src/config/persona/`**：角色与工作台视图映射配置（`persona-config.ts`）
- **`src/lib/store/`**：全局 Zustand 状态（`auth-store.ts`、`persona-store.ts`、`layout-store.ts`）
- **`src/components/`**：按业务领域与视图划分的组件（`portal/`、`workspace/`、`manager/`、`executive/`、`ai/`、`layout/`）
- **`src/lib/api/`**：API 客户端模块

### 工具链

- **`itsm-cli`** (Node.js)：命令行工具，供运维和开发使用
- **`itsm-agent`** (Go)：Worker 进程（执行异步任务、定时作业）
- **`itsm-skill`** (Node.js)：MCP 技能市场插件
- **`itsm-ai-service`** (Python)：可选 — 自托管 LLM 推理
- **`itsm-rag`** (Python)：可选 — 自托管 embedding 模型

飞书公开回调仅通过高熵连接器实例 ID 定位租户；原始请求验签、URL token 校验及严格 JSON 边界由 `FeishuController.Webhook` 负责。旧管理员 parse-only 回调与未使用的连接器 HTTP wrapper 已移除；未支持的执行事件显式拒绝。

## 数据流：典型工单生命周期

```
1. 客户提交工单
   ├─ Web Form → POST /api/v1/tickets
   └─ 飞书任务 (Connector) → POST /api/v1/feishu/webhook/:instance_id

2. Controller 接收
   ├─ middleware: Auth → RBAC → Tenant
   └─ TicketController.CreateTicket → dto 校验

3. Service 处理
   ├─ TicketService.Create
   │   ├─ Ent: INSERT INTO tickets ...
   │   ├─ 触发 AI Triage（异步）→ 更新 priority / assignee
   │   ├─ 触发 SLA 计时器 → Redis sorted set
   │   └─ 发布事件 TicketCreated → Watermill pub/sub
   │
   └─ 后台订阅
       ├─ NotificationService → 邮件 / 飞书 / 钉钉
       ├─ SLAMonitorService → 检查 SLA 阈值
       ├─ KnowledgeService → RAG 推荐相关文章
       └─ AnalyticsService → 实时统计

4. 工程师处理
   ├─ GET /api/v1/tickets/:id
   ├─ PUT /api/v1/tickets/:id/status  (状态流转)
   ├─ POST /api/v1/tickets/:id/comments
   └─ POST /api/v1/tickets/:id/resolve

5. 关闭 + 复盘
   ├─ POST /api/v1/tickets/:id/close
   ├─ 触发 SLA 合规检查 → 写入 sla_violations 表
   └─ 异步：生成 RAG 训练样本（提升未来分派准确率）
```

## 关键设计决策

| 决策 | 原因 | 备选 |
|:---|:---|:---|
| **Ent ORM** | Schema 优先 + 类型安全 + 自动迁移 | GORM / sqlc / raw SQL |
| **Watermill** | 跨进程消息流 + Redis pub/sub | 直接 Redis / Kafka |
| **expr-lang** | ABAC 表达式引擎，支持运行时编译 | Casbin / OPA |
| **BPMN 引擎外置** | 复用成熟 lib（nitram509），避免重写 | 自研引擎 |
| **DTO camelCase** | 与前端 TypeScript 类型对齐 | Ent 原生 snake_case |
| **WebSocket 票据** | 避免 JWT 在 URL 中泄露 | 长连接 + refresh token |
| **多包结构** | 子项目可独立 CI / 独立部署 | monorepo 单一包 |

详见：

- [多租户设计](tenancy.md)
- [BPMN 工作流架构](bpmn.md)
- [模块边界清单](modules.md)
