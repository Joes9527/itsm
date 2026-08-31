# Unified Intake 实施与验证报告

日期：2026-09-01

ITSM 分支：`feat/kaf-delegation-transactional-delivery`

KAF 分支：`feat/unified-intake-client`

## 1. 交付结论

Unified Intake 已成为 Incident 与 Service Request Item 的统一新建边界。ITSM Access JWT 与 KAF 短期交换令牌均调用 `POST /api/v1/intake/work-items`；租户、actor 和 requester 只来自认证身份。创建事务原子写入幂等 receipt、WorkItem、专业扩展、解析快照与 workflow-start Outbox，冻结的流程绑定由 worker 使用稳定 business identity 异步启动。

交付同时完成了旧入口收口、共享字段单一权威、外部身份映射、KAF HMAC 身份交换、短期令牌、低基数指标、RLS 探针、人工重试和运维配置。迁移主线当前 head 为 `022_external_identity_version`，相关顺序为 `019` → `020` → `021` → `022`。

## 2. 提交与范围

ITSM 实施提交（设计到跨仓切换）：

- `536e3959` — design unified work item creation
- `210db1bf` — plan unified intake implementation
- `bfbedc7c` — tenant-scoped persistence foundation
- `f8608cf5` — typed create command
- `54b12c17` — transactional create contributors
- `55a0cd8b` — authoritative create configuration
- `a68a54c9` — professional creator extraction
- `4390be76` — transactional WorkItem creation
- `47aabe68` — authenticated create endpoint
- `230ce912` — frozen workflow start through Outbox
- `a6f3e203` — professional create cutover
- `5342886a` — remove shared-field dual authority
- `b6276439` — verified channel identity exchange
- `535d4db7` — preserve authenticated provider

本报告、指标和最终验证修正在 `f28988ec docs(intake): record unified intake verification` 提交中。

KAF 切换提交：`16b1bae feat(itsm): create work items through unified intake`。KAF 删除了 Gazellio/Web Form/邮件成功兜底等创建旁路，保留互不相关的读取、密码与通知能力。KAF 不再读取 automation token 创建工单，也不接受权威 employee/tenant/requester 输入。

## 3. 自动化验证

### ITSM 后端

| 验证 | 结果 |
| --- | --- |
| `go test ./handlers/intake ./controller ./handlers/service_request ./service ./router ./middleware ./migration ./internal/bootstrap -count=1` | PASS，8/8 包 |
| `go test ./... -count=1` | PASS，52 个含测试包；161 个无测试文件包正常编译 |
| `go build ./...` | PASS |
| `go test ./handlers/intake -run 'Metrics|E2E' -count=1` | PASS |
| `go test -tags integration_postgres -v ./handlers/intake -run E2E -count=1` | PASS，1 个真实 HTTP + PostgreSQL E2E，0 skip，0.814s |
| `go test -tags integration_rls -v ./database/rls/... -count=1` | PASS，18 条 PASS 记录（含 3 个子测试），0 skip，0.163s |

RLS 使用一次性 PostgreSQL 数据库和实际 owner 连接执行；报告及日志未保存 DSN 密码。验证覆盖 tenant 1/999 隔离、KAF integrity 表、`intake_requests`、`intake_resolution_snapshots`、`external_identities`、缺少 tenant fail-closed，以及连接归还后 session tenant 不泄漏。

### ITSM 前端

计划指定的 Jest 命令执行了 3 个 suite、69 个断言，断言全部通过；命令本身退出码为 1，因为仓库全局 80% coverage 门槛会对仅选择 3 个文件的窄跑统计生效（本次覆盖率 2.37%）。使用相同文件并显式 `--coverage=false` 后为 3/3 suite、69/69 tests PASS，0.913s。`npm run build` PASS，并成功准备 standalone runtime。

这不是把原命令描述为绿色：原命令的非零退出已保留为测试基础设施限制；本次修改范围没有 Jest 断言失败。

### KAF

| 验证 | 结果 |
| --- | --- |
| 计划指定的 5 文件 focused suite | 24 passed |
| 扩大的修改范围 suite | 71 passed |
| Ruff check | PASS |
| Ruff format check | PASS |
| `pytest -q` 全仓 | exit 1：2455 passed、96 failed、13 skipped、1 xfailed、32 errors |

KAF 全仓非零结果不能描述为绿色。与原始 `main` 基线 `2450 passed、96 failed、13 skipped、1 xfailed、32 errors` 相比，新增 5 个通过，失败/error/skip 数量未增加；抽查原始工作树也复现了 PostgreSQL `ai01/changeme` 测试数据库认证失败。所有修改范围测试均通过，因此记录为既有环境/基线失败，而不是本次切换引入的失败。

## 4. 真实路径证据

一次性 PostgreSQL Intake E2E 通过真实 Gin HTTP 路由执行以下路径：

1. Access JWT 创建 Service Request Item 并原样重放；
2. Access JWT 创建 Incident 并原样重放；
3. 伪造 KAF assertion 被 401 拒绝；
4. 已签名 assertion 换取短期 Intake token，再创建 KAF channel Incident；
5. 模拟 BPMN 不可用，使三个 Outbox 事件进入 `dead`；
6. 通过正式 repository retry 路径恢复为 `pending`，再次派发后全部为 `published`。

最终数据库证据（仅记录非敏感 ID/计数）：

| WorkItem | recordClass | receipt | snapshot | Outbox | process | final Outbox |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| `1 / TKT-E2E-000001` | `service_request_item` | 1 | 1 | 1 | 1 | `published` |
| `2 / TKT-E2E-000003` | `incident` | 1 | 1 | 1 | 1 | `published` |
| `3 / TKT-E2E-000005` | `incident` | 1 | 1 | 1 | 1 | `published` |

专业扩展也分别断言为恰好一个：WorkItem 1 有一个 Service Request extension；WorkItem 2、3 各有一个 Incident extension。两次 Access JWT 重放返回相同 WorkItem ID，未新增 receipt、WorkItem、扩展、快照、Outbox 或流程。

本验证没有调用 Microsoft Graph、KAF PROD 或任何生产写入接口；assertion、JWT、HMAC secret、聊天原文和表单秘密均未写入本报告。

## 5. 可观测性与运维验收

Prometheus 指标已注册并由创建服务、身份交换和 workflow worker 实际观测：

- `itsm_intake_requests_total`
- `itsm_intake_request_duration_seconds`
- `itsm_intake_workflow_start_total`
- `itsm_intake_identity_exchange_total`

标签仅包含收敛后的 channel、record class 与结果枚举；无 tenant、user、WorkItem、idempotency key 或错误字符串等高基数字段。指标测试覆盖 created、replayed、conflict、error、latency、pending、published、retry、dead、issued 和 denied。

基础、dev、prod 三份 Compose 配置均通过 `docker compose ... config -q`（基础文件只有既有 obsolete `version` 提示）。`.env.example` 和开发指南已记录 exchange secret 隔离、Redis nonce fail-closed、Outbox worker 设置、迁移顺序、RLS 预检、指标、worker 健康与正式人工 retry 端点。

## 6. 已知限制与发布边界

- KAF `bastion_permission_apply` 与 `business_system_account_request` 尚无真实租户 `catalog_item_id` 配置；它们现在会明确 fail-closed，不能再退化为邮件或直接 CTI 创建。上线前须按租户配置真实 Catalog Item ID。
- 窄范围前端 Jest 命令会被全局 coverage 阈值判为非零；CI 应运行完整 coverage suite，或把 focused smoke 明确配置为不收集全局覆盖率。
- KAF 全仓测试需要可用且凭据匹配的 PostgreSQL 测试服务；在修复该既有环境前不能宣称 KAF repository-wide suite 绿色。
- 发布必须先运行迁移和 RLS 预检，再启用 KAF identity exchange；Redis 不可用时 exchange 按设计返回 503，不允许绕过 nonce 防重放。

## 7. 文档审计后的未完成工作

以下项目不否定首期 Unified Intake 的完成状态；它们是发布配置、测试基础设施或下一阶段产品能力。评分为客户/业务影响、安全/合规风险、ITIL 关键性、战略一致性、实施不确定性（均为 1–5，最后一项越高越难）。

| 优先级 | 结果与目标用户 | 负责边界 | 影响/安全/ITIL/战略/难度 | 状态 |
| --- | --- | --- | --- | --- |
| P0 | 为升级自旧数据模型的运维人员提供可执行的 Incident WorkItem 回填路径 | `cmd/backfill_*`、migration `021` | 5/4/5/5/3 | planned |
| P0 | 为两个 KAF Catalog procedure 配置真实租户 Catalog Item ID | KAF procedure/config registry | 5/3/5/5/2 | planned |
| P0 | 让 KAF repository-wide pytest 使用隔离且凭据可靠的 PostgreSQL fixture | KAF test bootstrap/settings | 4/2/3/4/3 | planned |
| P1 | Incident typed actions 支持 `expectedVersion` 并保留审计/租户边界 | Incident professional service/API | 4/4/5/5/3 | proposed |
| P1 | 移除 Problem/Change 对 WorkItem 公共字段的双重权威并使 `work_item_id` 必填 | Problem/Change schema/services/migration | 5/3/5/5/5 | planned |
| P1 | 收敛 KAF Procedure 权威版本、manifest 与执行模型 | KAF procedure registry/assembler | 4/4/4/5/4 | proposed |
| P1 | 修复 BPMN lower-camel query binding 与流程实例 ID 语义不一致 | BPMN controller/service contract | 3/3/4/4/2 | planned |
| P1 | 将 Intake create/exchange/mapping/retry 路由纳入生成的 Swagger/OpenAPI | Intake handlers + `itsm-backend/docs` | 3/2/3/3/2 | planned |
| P1 | 让 generic `CompleteTask` 获得与 KAF 专用路径等价的事务/恢复保证 | BPMN task completion | 4/4/5/5/5 | proposed |
| P1 | 修复默认 `incident_emergency_flow.bpmn` 的 solved 网关回路并补真实浏览器 BPMN E2E | BPMN template/browser suite | 4/3/5/4/3 | planned |
| P2 | 增加 Unified Intake UI、Intake Session、附件 staging、知识分流和多渠道状态反馈 | Intake frontend/application slice | 4/3/3/5/5 | proposed |
| P2 | 扩展 `ProfessionalCreator` 到 Problem、Change Request、Catalog Task | owning professional services + Intake registry | 5/3/5/5/5 | proposed |
| P2 | 将 `tickets.ticket_number` 唯一性改为 tenant-scoped 并完成存量冲突迁移 | Ticket schema/number allocator | 4/3/4/5/4 | proposed |

### 当前唯一的迁移发布阻塞

`021_work_item_authority` 会在任何 Incident 缺少权威 WorkItem 时 fail-closed；这是正确的安全门禁。但当前树已删除历史 `cmd/backfill_incident_work_item`，只剩只读的 `cmd/check_work_item_integrity`，因此从尚未完成 Incident 回填的旧版本直接升级时没有仓库内的修复命令。发布到这类环境前必须恢复/替代一个可审计、幂等、支持 dry-run 的 Incident 回填工具，并用生产副本演练 `integrity check → backfill → 021 → 022`。已经完成早期 WorkItem 回填且完整性检查为零异常的环境不受此阻塞影响。

### 验收边界

- P0 迁移工具：dry-run 给出候选基数；重复执行不新增 WorkItem；事务失败无部分状态；`check_work_item_integrity` 零异常后 `021`、`022` 连续通过。
- P0 Catalog 配置：两个 procedure 都从租户配置解析真实 Catalog Item ID；未知/停用项 fail-closed；真实 Dev 创建和精确重放各通过一次。
- P0 KAF 测试环境：全仓 `pytest -q` 在隔离数据库上退出 0；不得通过屏蔽 96 个失败或改成 skip 达成。
- Problem/Change 权威收口：所有公共读写迁到 WorkItem，扩展表删除 title/description/status/priority/assignee/tenant/公共时间戳副本，`work_item_id` 变为必填并由 ticket join 实施 RLS；Change 的关系 JSON 迁到结构化关系表。
- BPMN 合同：lower-camel query 有 HTTP contract test；变量端点只接受一种公开且文档化的实例标识；旧标识若移除需显式迁移，不保留含糊双语义。
- OpenAPI：四类 Intake 路由、认证方式、稳定错误和幂等重放响应出现在生成的 Swagger JSON/YAML/Go 中，并由契约检查防止再次漂移；本次只更新了手写 `docs/api/API_REFERENCE.md`。
- 后续产品增量继续遵守独立 spec → plan → implementation，不回写首期 Intake 的完成状态。
