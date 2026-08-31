# BPMN 整改遗留项 E2E 测试计划

> **归档状态（2026-09-01）：已执行。** S1–S5、S7 以 HTTP + DB 证据完成，
> S6/S8/S9 以自动化测试替代浏览器路径；S10–S15 的跨进程 Dev 验收由
> `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md` 收口。
> 本文保留历史环境步骤，当前 Unified Intake/RLS 命令以
> `docs/e2e-testing-guide.md` 和 Unified Intake 实施报告为准。

> 对应工作分支的 4 个遗留项整改（租户隔离 fail-closed / Release·Change 域侧桥接 / service_request 白名单收敛 / 平台级操作恢复）+ 顺带修复的 4 个模板缺陷。
> 测试通过前**不提交**；每轮测试发现的问题记录在文末"结果记录表"，修复后重新执行对应场景。

> 2026-08-31 补充：S10-S15 覆盖 KAF 自主委派、Outbox、task-scoped API、
> action ledger 与崩溃恢复。`10.128.35.195` 是 KAF PROD，不是本计划的
> 测试目标；除非另行批准，不得使用 PROD 凭据或向 PROD 发送委派事件。

---

## 0. 前置条件

### 0.1 启动本地开发环境（本地启动优先，不用 docker 跑 backend/frontend）

backend / frontend **本地进程启动**（依赖的数据库/Redis/MinIO 仍用 docker 容器）：

```bash
# 1. 确认依赖容器在运行（postgres/redis/minio，保持容器方式）
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep itsm

# 2. 本地启动后端（端口 8090）
cd itsm-backend
go run main.go
# .env 里 DB_HOST/Redis/MinIO 指向 localhost 即可（依赖容器端口已映射到主机）

# 3. 本地启动前端（另开终端）
cd itsm-frontend
npm run dev -- -p 3010
# 注意：主机 3000 被 KAF 项目的 Langfuse 容器占用，本地 dev 服务器用 3010
# 前端访问 http://localhost:3010
```

> 模板无需任何手工操作：后端启动时会**自动检测模板漂移**——嵌入模板与库里最新版本不一致则自动发布新版本（事务化降级 is_latest、停用旧版本、激活新版本），存量数据保留。这也顺带覆盖"版本管理 is_latest 降级"整改项的验证。

### 0.2 确认模板已自动升级

```bash
docker exec itsm-postgres-dev psql -U itsm_user -d itsm -c \
  "SELECT key, version, is_latest, is_active FROM process_definitions WHERE key IN ('release_approval_flow','change_normal_flow','incident_emergency_flow') ORDER BY key, id;"
```

- 预期：修复后的模板有两个版本（1.0.0 旧 / 1.1.0 新），旧版本 `is_latest=f, is_active=f`，新版本 `is_latest=t, is_active=t`。

### 0.3 测试账号（已创建，密码统一 Test@12345678）

| 账号 | 角色 | 租户 | 用途 |
|---|---|---|---|
| admin | super_admin | 1 | 管理操作 / S5 API 步骤可复用 |
| agent_a | change_manager | 1 | 发布创建人、变更发起人（release:write + change:write） |
| approver_a | dept_manager | 1 | 发布审批人（release:approve）；**已是 ticket-approvers 组成员**（引擎审批兜底候选组） |
| agent_b | end_user | 2（隔离测试租户） | 跨租户隔离场景（S5） |

> 变更 CAB 审批人：change_normal_flow 的 Activity_CABApproval 配置 `assigneeRole="change_manager"`，用 agent_a（change_manager 角色）或管理员执行变更审批步骤即可。

### 0.4 健康检查

```bash
curl http://localhost:8090/api/v1/health
# 前端打开 http://localhost:3010 能正常登录
```

### 0.5 KAF 当前源码与 Dev 数据平面

`kaf-dev` Compose 只提供 PostgreSQL、Redis 和 Qdrant。旧的
`acp-backend` 容器镜像不是当前分支验收对象；必须从 KAF worktree 启动源码。

```bash
KAF=/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery

docker compose -f "$KAF/docker/docker-compose.dev.yml" -p kaf-dev up -d

cd "$KAF"
ENV_FILE=/dev/null DEBUG=true \
DATABASE_URL='postgresql+asyncpg://ai01:changeme@127.0.0.1:5434/control_plane' \
REDIS_URL='redis://:changeme@127.0.0.1:6380/1' \
PYTHONPATH=src .venv/bin/python -m alembic upgrade head

# 必须输出 036_kaf_completion_replay
docker exec kaf-dev-postgres psql -U ai01 -d control_plane -Atc \
  'select version_num from alembic_version'
```

启动 KAF 时使用新的 Go/BPMN ITSM 专用配置，不得复用旧 Gazellio
`ITSM_URL`/`ITSM_WEBHOOK_SECRET`：

```bash
ITSM_KAF_URL=http://127.0.0.1:8090 \
ITSM_KAF_AUTOMATION_TOKEN='<同租户 kaf_automation JWT>' \
ITSM_KAF_WEBHOOK_SECRET='<dev shared secret>' \
DATABASE_URL='postgresql+asyncpg://ai01:changeme@127.0.0.1:5434/control_plane' \
REDIS_URL='redis://:changeme@127.0.0.1:6380/1' \
PYTHONPATH=src .venv/bin/python -m uvicorn acp.main:app --host 127.0.0.1 --port 8001
```

ITSM 对应配置：

```bash
KAF_WEBHOOK_URL=http://127.0.0.1:8001/webhooks/itsm
KAF_WEBHOOK_SECRET='<dev shared secret>'
```

---

## 场景 S1：发布全流程（审批通过路径）【P1 核心】

**覆盖**：release_approval_flow 五节点全部可达；域侧桥接注入 business_id；网关变量 tech_review_pass / approval_pass；状态机白名单。

**前置**：agent_a（创建人）、approver_a（审批人，在审批候选组）。

**步骤**：

1. agent_a 登录 → 发布管理 → 新建发布（标题/编号随意，类型 minor）→ 状态应为 `draft`。创建成功后发布域会自动触发 `release_approval_flow`（按默认绑定）。
2. **工作流 → 实例**（菜单名"工作流"，不是"流程管理"）：确认已为该发布启动 `release_approval_flow` 实例，当前节点 = **技术评审**。
3. 发布详情页点 **技术评审** → 输入"架构评审通过" → 提交。
   - 预期：详情页 release_notes 出现 `[技术评审] 架构评审通过`；流程实例推进到 **发布审批**。
4. 用 approver_a 登录 → 发布详情页点 **批准**（填意见）→ 预期：
   - 发布状态 = `已计划(scheduled)`；流程实例推进到 **计划发布**；
   - approver_a 的待办列表里该审批任务消失。
5. 发布详情页点 **提交计划** → 预期：状态保持 scheduled；流程实例推进到 **执行发布**。
6. 点 **开始发布** → 预期：状态 = `进行中(in-progress)`；流程推进到 **验证确认**。
7. 点 **完成发布** → 预期：状态 = `已完成(completed)` 且 actual_release_date 有值；**流程实例结束**（状态 completed）。

**失败判定**：任一步流程节点不动 / 状态与步骤不符 / 流程实例悬挂 running。

---

## 场景 S2：发布审批拒绝路径【P1 + 模板修复】

**步骤**：重复 S1 步骤 1-3（新发布）→ approver_a 点 **拒绝**（必填意见）。
- 预期：发布状态 = `已取消(cancelled)`；**流程实例正常结束**（不再卡在审批结果网关）；approver 待办清空。

---

## 场景 S3：普通变更全流程（走 CAB 审批）【P1】

**覆盖**：change_normal_flow 评估→CAB→排期→实施→验证→关闭；TransitionStatus 域侧桥接；approvalAction 网关；verify_passed 网关；handler 状态对齐域状态机。

**步骤**：

1. 变更管理 → 新建普通变更（类型=normal）→ 提交审批。
2. 流程管理确认实例当前节点 = **变更评估**；在任务列表（我的待办/流程任务）完成"变更评估"任务（提交变量 `change_id` 为变更 ID，或由页面任务表单完成）。
   - 预期：流程推进到 **CAB 审批**（默认 binding 走 change_normal_flow）。
3. 审批人（change_manager 角色用户，或候选组内用户）登录 → 变更详情页 **批准**。
   - 预期：变更状态 = `approved`；流程推进到 **变更排期**；审批决策记录可见。
4. 变更详情页点 **开始实施**（POST /changes/:id/start）。
   - 预期：状态 = `in_progress`；流程推进到 **变更验证**（排期节点被桥接完成）。
5. 变更详情页点 **完成**（POST /changes/:id/complete）。
   - 预期：状态 = `completed` 且 actual_end_date 有值；**流程实例结束**。

**失败判定**：步骤 4/5 后流程节点不动、状态被 handler 改写成非法值（如 verification_passed/closed）、或流程悬挂。

---

## 场景 S4：标准变更免审批路径【P1 边界】

**步骤**：新建**标准变更**（type=standard，预授权）→ 直接点 **开始实施** → **完成**。
- 预期：流程若已启动则同样走通（桥接对不存在的节点安全跳过）；状态 in_progress→completed。

---

## 场景 S5：跨租户隔离（伪造业务 ID）【P0 安全，API 级】

**覆盖**：incident/change handler 的 `Where(TenantID)` 写保护；实例变量白名单。

**前置**：租户 A 有一条事件（incident，id 记为 A_ID）；租户 A 有一条进行中的 change 流程任务（或任意 user_task 任务 id 记为 TASK_ID）；租户 B 账号 agent_b。

**步骤**（curl，JWT 从登录接口获取）：

```bash
TOKEN_A=$(curl -s -X POST http://localhost:8090/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"agent_a","password":"<密码>"}' | jq -r '.data.token')

# 5.1 用租户 A 用户完成租户 A 的任务，伪造 change_id 指向不存在的行/越权行
curl -s -X PUT http://localhost:8090/api/v1/bpmn/tasks/<TASK_ID>/complete \
  -H "Authorization: Bearer $TOKEN_A" -H 'Content-Type: application/json' \
  -d '{"variables":{"change_id":999999,"action":"update_change"}}'
# 预期：接口不报 5xx（回调副作用失败只告警），目标变更状态未被改动（999999 不存在 → 零写入）

# 5.2 实例变量白名单（P0.3）：任何账号 PUT 保留键必须被拒绝
curl -s -X PUT http://localhost:8090/api/v1/bpmn/process-instances/<INSTANCE_ID>/variables \
  -H "Authorization: Bearer $TOKEN_A" -H 'Content-Type: application/json' \
  -d '{"business_id":1,"tenant_id":1}'
# 预期：报错"变量 \"business_id\" 由流程触发方管理，不允许经此端点覆盖"；实例变量未变
```

（若有平台级/租户 B 账号可用，再补一枪：用租户 B token 完成租户 A 的任务并伪造 incident_id=A_ID 的事件升级——预期事件数据零变化。这条路径在 UI 上不可达，纯 API 验证。）

**失败判定**：伪造 ID 后目标行状态被改写；白名单接口未拒绝。

---

## 场景 S6：流程设计器校验【前端整改项】

**步骤**：流程管理 → 设计器 → 新建/导入含 **并行网关 / 包容网关 / 子流程 / 边界事件** 的 BPMN → 点校验/保存。
- 预期：弹出 warning："该流程包含引擎暂不支持的元素：xxx"；保存仍允许但带警告标记。
- 反向：纯 排他网关+用户任务+服务任务 的流程校验无此类警告。

---

## 场景 S7：自定义字段类型校验【上一轮已合并项，顺带回归】

> **UI 覆盖面更正**：管理端字段构建器（`CustomFieldsEditor.tsx`）目前只能配出
> text/textarea/number/date/select 5 种类型，配不出 multiselect/boolean/file——这是
> 原始审计发现的独立缺口（未在这轮整改范围内），不是这次改动引入的回归。
> 截至 2026-09-01，工单创建页的字段渲染器已支持 `file`，但仍没有
> `'multiselect'`/`'boolean'`；未识别的类型会退化成单行文本框——
> 就算后端已经存了一个 multiselect 字段定义，这个页面也只会给你一个文本框，
> 提交的是字符串而不是数组，会稳定触发 `validateFieldValue` 的"需要数组类型的值"
> 报错，测的其实是"UI 传错类型"而不是 multiselect 校验本身。
> **所以 multiselect 分支只能走 7.2 的 API 直测，UI 路径测不到。**

**7.1 UI 路径（覆盖 number / select，走完整前端表单）**：
1. 工单模板管理 → 新建/编辑模板 → 自定义字段：number（整数，必填）、select（选项 a/b/c，必填）。
2. 工单创建页选该模板：
   - number 填 "abc" → 预期报"数值类型/格式错误"；
   - select 填 d（若 UI 选项本身封闭则跳过，直接走 7.2 用 API 提交越界值）；
   - 全部合法 → 创建成功且字段值正确回显。
3. 创建工单时留空必填自定义字段（number/select 任一）→ 预期：**创建失败且工单列表无孤儿行**（校验在落库前）。

**7.2 API 直测（覆盖 multiselect，以及 select 越界值，UI 走不到的路径）**：
```bash
# 通过真实模板端点创建/更新带 multiselect 字段的模板，记 TEMPLATE_ID
curl -s -X PUT http://localhost:8090/api/v1/tickets/templates/<TEMPLATE_ID> \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"fields":[{"name":"impacted_systems","label":"受影响系统","type":"multiselect","required":false,
       "options":[{"label":"x","value":"x"},{"label":"y","value":"y"},{"label":"z","value":"z"}]}]}'

# 提交越界值 → 预期 400，报"包含不在允许范围内的值: w"
curl -s -X POST http://localhost:8090/api/v1/tickets \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"title":"multiselect越界测试","priority":"medium","templateId":<TEMPLATE_ID>,
       "formFields":{"values":[{"name":"impacted_systems","value":["x","w"]}]}}'

# 合法值 → 预期创建成功
curl -s -X POST http://localhost:8090/api/v1/tickets \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"title":"multiselect合法测试","priority":"medium","templateId":<TEMPLATE_ID>,
       "formFields":{"values":[{"name":"impacted_systems","value":["x","y"]}]}}'
```

**失败判定**：7.1 UI 路径任一步校验未生效或留孤儿行；7.2 API 越界值未被拒绝，或合法值创建失败。

---

## 场景 S8（可选·进阶）：service_request 自定义模板 update_request 真写【P2.1】

**步骤**：设计器新建流程：开始 → 服务任务（`extensionElements` metaData：`service_task_type=service_request_task`、`action=update_request`）→ 结束 → 部署；启动实例（变量带 `request_id`=某服务请求 ID、`cost_center`="CC-E2E"）；流程走到该任务。
- 预期：任务完成后服务请求详情 `cost_center=CC-E2E`；对已完成/关闭态关联工单执行 approve/reject/complete 动作时明确报"非法的关联工单状态转换"。

---

## 场景 S9（可选·进阶）：平台级操作【P2.2】

**前置**：平台级账号（tenant_id=0，若 MSP 配置支持）。
**步骤**：平台账号启动一条 incident_emergency_flow 实例（API 或流程管理入口，变量带 incident_id/assignee_id）。
- 预期（修复后）：流程正常推进、自动分配动作以**实例所属租户**执行成功（事件被分配），不再整条 StartProcess 硬失败。
- 若环境无平台账号：跳过手动验证，此项已有自动化覆盖（`bpmn_platform_tenant_test.go` 三条用例）。

---

## 场景 S10：KAF 委派健康与迁移前置【P0】

1. 执行 0.5 的 Alembic 升级并启动当前 KAF 源码。
2. `GET http://127.0.0.1:8001/health` 必须返回 `{"status":"ok"}`。
3. PostgreSQL 必须存在 `kaf_delegation_deliveries`，revision 必须为
   `036_kaf_completion_replay`。
4. KAF 日志不得出现旧 `acp-backend` 的 `operation_policies.approval_chain`
   非空约束错误。

## 场景 S11：SSLVPN 委派主路径【P0】

1. 创建 SSLVPN Service Request，完成双级审批，使 BPMN 到达
   `taskType=kaf_delegate` 节点。
2. ITSM 必须在同一事务中产生一个 delegated `ProcessTask`、一条
   `kaf_delegate.created` 审计和一条 `kaf_delegate_requested` Outbox。
3. Outbox webhook 被 KAF 接收后，KAF 只能通过 `ITSM_KAF_URL` 获取
   `kaf-context`；自主选择 Procedure 后提交 `complete_bpmn_task`。
4. 最终只允许存在一条 action ledger、一个成功 completion receipt、一个
   KAF delivery；BPMN 只推进一次，WorkItem/Service Request 专业扩展仍存在。
5. 用同一 action payload 重放，ITSM 返回 `already_applied`，不得增加
   timeline、audit 或专业领域副作用。

## 场景 S12：投递重复与崩溃恢复【P0】

1. 同时发送相同 `(tenantId, taskId, correlationId)` 的 webhook，并触发一次
   delegated-list recovery；只能有一个 Procedure 获得有效 lease。
2. 在 KAF 已持久化 `completion_payload`、ITSM 已返回 `applied`、但 KAF
   尚未写 `completed` 时终止进程。
3. 重启 KAF 后必须重放原 payload，不得重新选择 Procedure 或再次执行 Tool；
   delivery 最终收敛为 `completed`。
4. 将 running lease 置为过期后复测，新的 owner 可以恢复，旧 owner 不得
   finalize、续租或覆盖结果。

## 场景 S13：认证、租户与配置隔离【P0 安全】

1. 只配置旧 `ITSM_WEBHOOK_SECRET`、不配置 `ITSM_KAF_WEBHOOK_SECRET`，发送
   正确签名的委派事件，预期 `503 kaf_webhook_secret_not_configured`。
2. 使用非 `kaf_automation`、错误租户或普通 user task 调用 context/actions，
   预期 403/404 fail-closed，且无 ledger、timeline、audit 或流程变化。
3. 使用同租户 `kaf_automation` 调用时，只能读取 delegated KAF task；列表
   翻页不得出现其他租户任务。

## 场景 S14：附件上下文最小披露【P1】

1. 给 SSLVPN WorkItem 上传一个文件名、路径和 URL 均含敏感信息的附件。
2. 调用 `GET .../kaf-context`，`attachments` 只允许包含同租户附件 ID。
3. 响应不得包含文件名、对象存储路径、直接 URL、签名 URL 或其他租户附件。

## 场景 S15：回调失败与部分推进【P0】

1. 注入 callback commit 后报错，首次 action 必须保持 retryable，不能伪装
   `applied`；重试通过 action scope 收敛且领域副作用只有一次。
2. 分别构造“task 已完成但 current activity 未推进”“activity 已变化但无活跃
   successor”“end activity 已写但 process 仍 running”，均不得 reconcile 为
   `applied`。
3. 只有精确 successor task 存在或 process 已 terminal completed 时，恢复才可
   写成功 receipt 并 finalize ledger。

---

## 已知问题（测试时如命中，属预存在缺陷，记录即可，不阻塞本轮通过）

1. `incident_emergency_flow` 的 `Gateway_Solved` 网关：outgoing 引用了入流 `Flow_5`（建模缺陷），且 `solved != true` 无出路分支——事件流程走"未解决"路径会卡网关。
2. 平台级（tenant_id=0）启动流程时，定义查询不筛租户——多租户同 key 时选哪条不确定（实例租户跟随所选定义，写入不越界）。
3. change 域 API 无"排期(scheduled)"动作——normal 变更无法经域 API 走 approved→scheduled，需直接完成 BPMN 排期任务。

---

## 结果记录表

| 场景 | 结果 | 失败现象 | 修复记录（commit/说明） | 复测 |
|---|---|---|---|---|
| 0.1 本地启动 | ✅ | `go run main.go` 默认不跑 seed/模板同步（`ITSM_AUTO_SEED`/`ITSM_AUTO_MIGRATE` 默认 false，属生产安全设计）；需先跑一次 `ITSM_BOOTSTRAP_ONLY=true ITSM_AUTO_SEED=true go run main.go` 补齐权限/模板 | 未改代码，操作步骤记录 | ✅ |
| 0.2 模板生效 | ✅ | 不带租户过滤的 SQL 会看到 3 行（tenant2 首次部署又插入一条 1.0.0），按租户过滤后 tenant1 两版本 is_latest 语义正确 | 未改代码 | ✅ |
| S1 发布全流程 | ✅ | 1) `/releases/:id/approve\|reject\|rollback` 被全局 ResourceActionMap 通配符误判为 release:write，只有 release:approve 的审批人被拒；2) dept_manager 缺 bpmn:read/task:read，审批人看不到"我的待办" | middleware/rbac.go 补充 3 条动作型子路由映射；pkg/seeder/seeder.go 给 dept_manager 补 bpmn:read/task:read | ✅ |
| S2 发布拒绝 | ✅ | 同 S1（复用同一批修复） | 同上 | ✅ |
| S3 变更全流程 | ✅ | 1) `handlers/change` 提交审批从未触发 change_normal_flow（只桥接"完成已有任务"方向，没接流程触发服务）；2) 触发后未按变更类型设置 approval_required，网关恒判"无需审批" | handlers/change/service.go 新增 SetProcessTriggerService 并在 SubmitChange 里触发，按 type==normal 设 approval_required；internal/bootstrap/app.go 装配 | ✅ |
| S4 标准变更 | ✅ | 无（复用 S3 的 approval_required 修复）；域状态机仍需显式 approved 步骤才能到 in_progress，测试时按此调用 | 无新增修复 | ✅ |
| S5 跨租户隔离 | ✅ | 无发现问题（含可选的租户 B 越权补测，403 fail-closed） | 无 | ✅ |
| S6 设计器校验 | ✅（代码验证，非浏览器） | 无浏览器自动化工具，改用现有前端单测 `BPMNDesigner.validate.test.tsx`（5/5 通过），未做真实浏览器点击验证 | 无 | — |
| S7 自定义字段 | ✅ | 1) 测试计划文档里的 `/field-definitions` 端点和扁平 `formFields` 形状在当前代码里都不存在（文档过期，非代码缺陷，已按 `PUT /tickets/templates/:id` + `formFields.values` 数组形状改写执行）；2) **真实缺陷**：`CreateTicket` 对字段值的格式/范围校验（number/select/multiselect）只在落库*之后*的 `CreateValues` 里跑，写入失败被当成"持久化失败"静默吞掉——越界值/错误类型既不报错也不落库，调用方拿到 `code:0` 成功响应但数据被悄悄丢弃 | `service/ticket_service.go`：新增 `validateFieldValueFormats`，在 `s.repo.Create` 之前校验，越界/类型错误直接 400 系列拒绝，不再落库 | ✅ |
| S8 service_request | ✅（单测覆盖，非真实设计器） | 无浏览器/设计器可用，改用现有 `service/bpmn/service_request_handler_test.go`（11/11 通过），未做真实设计器建流程+部署+启动实例的端到端验证 | 无 | — |
| S9 平台级操作 | ✅（自动化覆盖，未手工验证） | 环境无 tenant_id=0 平台账号，按测试计划的兜底条款改跑 `bpmn_platform_tenant_test.go`（3/3 通过） | 无 | — |
| S10 KAF 健康/迁移 | ✅ | 当前源码运行，Dev PostgreSQL 到 `036_kaf_completion_replay` | release closeout report | ✅ |
| S11 SSLVPN 委派主路径 | ✅ | SR 35 / WorkItem 18 / process 144 完成；Outbox、delivery、ledger、receipt、Graph action 基数均为 1 | Live Dev Closeout Addendum | ✅ |
| S12 重复/崩溃恢复 | ✅ | 精确 payload replay 返回 `already_applied`，未重复执行 Graph；lease/recovery breakers 通过 | ITSM 19 + KAF 7 deterministic breakers | ✅ |
| S13 认证/租户/配置 | ✅ | 普通主体 403、跨租户 404、专用 secret fail-closed | closeout breakers/audit | ✅ |
| S14 附件最小披露 | ✅ | 只返回一个同租户 opaque attachment ID，无文件名/路径/URL | closeout context evidence | ✅ |
| S15 回调失败/部分推进 | ✅ | callback、successor、terminal reconciliation breakers 全部收敛且无重复副作用 | closeout deterministic evidence | ✅ |

**顺带修复（测试过程中发现，非某一场景专属）**：
- `service/bpmn_process_binding_service.go` 的 `InitDefaultBindings` 每次进程启动都会重复插入一整套流程绑定（无去重，`CreateBinding` 从不返回代码里期望的 "already exists"），本轮测试过程中已从 800+ 条脏数据清理到 26 条并补上幂等检查。
- `pkg/seeder/seeder.go` 的权限目录 `seedPermissions` 里从未定义过 `bpmn:read/write/delete`——`rolePermissionMap` 里任何角色写 `bpmn:read`（包括这轮给 change_manager/dept_manager 补的）都因为查不到对应权限 ID 被静默丢弃，只是长期运行的开发库里这几行权限不知何时被别的途径手工插过，掩盖了这个问题；补齐目录项后 `go test ./...` 全绿（此前 `pkg/seeder`/`middleware` 各有几个用例断言的是修复前的错误行为，已随之更新为修复后的预期值）。

**验证保真度说明**：本轮全程无浏览器自动化工具，S1-S5、S7 均通过直接 HTTP 客户端（curl）+ DB 查询走查完成，覆盖不到前端 http-client 请求体转换、真实表单交互与客户端校验；S6/S8/S9 用现有单测替代，覆盖不到设计器/表单的真实浏览器路径。

**2026-09-01 文档校正**：S7.2 已改为真实
`PUT /api/v1/tickets/templates/:id`、字段键 `type` 和
`formFields.values` 数组形状。仍未完成的产品能力是共享字段编辑器与创建页对
`multiselect`/`boolean` 的一致支持，而不是继续使用不存在的端点测试。
