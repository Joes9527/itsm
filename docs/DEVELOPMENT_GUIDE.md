# 开发与运维手册 (Development & Operations Guide)

本文档汇集 ITSM 项目的日常开发命令、Docker 部署配置、历史分支复盘教训与通用规范。

---

## 1. 常用开发命令

### 前端 (itsm-frontend)

```bash
cd itsm-frontend
npm install              # 安装依赖
npm run dev              # 启动本地开发服务 (http://localhost:3010)
npm run build            # 生产环境构建
npm run lint             # Lint 检查并自动修复
npm run lint:check       # 仅 Lint 检查
npm run type-check       # TypeScript 类型检查
npm test                 # 运行全部测试
npm run test:unit        # 仅单元测试
npm run test:integration # 仅集成测试
npm run test:e2e         # 运行 Playwright E2E 测试
```

### 后端 (itsm-backend)

```bash
cd itsm-backend
go run main.go           # 启动本地服务 (http://localhost:8090)
go build -o itsm-backend main.go # 编译二进制
./itsm-backend           # 运行二进制
go test ./...            # 运行全量单元与集成测试
# 已有 Ent schema 的部署：只运行已注册的 post-schema migrations。
go run -tags migrate ./cmd/migrate -up
# 仅 disposable development/test 数据库：完整且破坏性的 fresh bootstrap。
ITSM_ALLOW_DESTRUCTIVE_FRESH=true ITSM_FRESH_HOST="$DB_HOST" \
  ITSM_FRESH_PORT="$DB_PORT" ITSM_FRESH_DATABASE="$DB_NAME" \
  go run -tags migrate ./cmd/migrate -fresh
go run -tags create_user main.go
```

### BPMN instance authorization

Trusted BPMN scope is built only from authenticated `tenant_id`, `user_id`, role, and RBAC state. Elevated permissions are `process_instance:read`, `process_instance:update`, `task:read`, and `task:update`; request parameters never grant scope.

```bash
go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate' -count=1
```

### BPMN callback outbox

The callback outbox provides **at-least-once delivery**, not exactly-once delivery. Every delivery carries the stable outbox `execution_key` as `Idempotency-Key`; callback receivers must deduplicate that key and make the deduplication record and business effect atomic. A retry or lease recovery reuses the same key.

Rows move through `pending` (eligible at `next_attempt_at`), `processing` (owned by a worker lease), and `completed` (durably delivered). A processing lease lasts 60 seconds; after it expires, another worker can reclaim the row. The application starts an immediate sweep, then sweeps every 2 seconds in batches of 50. Failures return to pending with exponential backoff capped at 5 minutes.

Diagnostics must always include both the trusted tenant and exact execution key. Do not run unscoped outbox dumps on shared or production databases. Configure non-secret connection metadata in a PostgreSQL service entry (for example, `itsm-diagnostics` in `~/.pg_service.conf`) and keep the password in a mode-`0600` `.pgpass` or `PGPASSFILE`. Disable shell tracing before selecting those protected files. Invoke `psql` without a DSN argument so credentials never appear in the process argv. This example returns only operational metadata and deliberately excludes callback variables:

```bash
set +x
export PGSERVICE=itsm-diagnostics
export PGPASSFILE=/run/secrets/itsm-pgpass

psql --no-psqlrc -v tenant_id=42 -v execution_key='callback-key' -c "
SELECT execution_key, status, attempt_count, next_attempt_at,
       lease_owner, lease_expires_at, last_error_class, updated_at
FROM process_callback_outboxes
WHERE tenant_id = :'tenant_id'::bigint
  AND execution_key = :'execution_key';"
```

Logs may include `tenant_id`, `execution_key`, callback kind, attempt count, status, and an allowlisted error class. Never log callback variables or bodies, raw handler errors, credentials, tokens, DSNs, or other secrets.

Load the Go integration DSN once from a secret manager or a protected local environment file while shell tracing is disabled. The file example below must be mode `0600`; replace the command substitution with the local secret-manager command where applicable. Never echo the value or expand it in the `go test` argv. Then run the callback and authorization release gate from `itsm-backend`:

```bash
set +x
export ITSM_TEST_DB="$(< /run/secrets/itsm-test-db.dsn)"

go test -tags integration ./service \
  -run 'TestClaimTaskConcurrentCASPostgres|TestBPMNCallbackOutboxLeaseRecoveryPostgres' -count=1 -v

go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate|Callback|CounterSign' -count=1
go test -race ./service ./internal/bootstrap -run 'TestBPMNAuthorization|TestTaskMutation|TestProcessInstanceMutation|TestCounterSign|TestCallback|TestClaimTask' -count=1
go test ./middleware ./router ./internal/bootstrap -count=1
go test ./... -count=1
go build ./...
git diff c15af6eda1febd47a75fb1e621907b16bbaac336..HEAD --check
```

Provision `ITSM_TEST_DB` through the protected local secret environment before running the integration gate. Do not paste its password into documentation, shell history, CI output, process arguments, or test logs, and do not substitute SQLite or skip the integration tests when PostgreSQL is unavailable.

The KAF work remains on a separate branch. After rebasing that branch onto callback-outbox or authorization changes, rerun the authorization/KAF-focused tests and the complete gate above before merge; a previously green KAF result is not evidence for the rebased result.

### 环境配置

```bash
# itsm-backend/.env 配置示例
LOG_LEVEL=info
DB_PASSWORD=your_password
JWT_SECRET=your-jwt-secret
ADMIN_PASSWORD=admin123

# 前端环境变量
NEXT_PUBLIC_API_URL=http://localhost:8090
```

### 开发环境拓扑（多机协作）

当前 ITSM 项目在两台机器上并行开发，不是单机自包含环境：

- **本机（Mac）**：日常编码环境，运行前端 `npm run dev`、后端 `go run main.go` 等本地进程。
- **192.168.31.66**：承载共享的 Postgres / Redis 基础设施——本机 `itsm-backend/.env` 的 `DB_HOST`/`REDIS_HOST` 均指向这台机器，本地后端连的不是私有 Docker 实例，而是这台机器上的共享数据库。同一台机器上还运行着另一路代码检出/worktree（供并行 agent 任务使用）以及 GitHub Actions self-hosted CI runner。

由此带来的实际约束：

1. **DB/Redis 是共享基础设施，不是本机沙箱。** 本机执行的迁移、`-fresh` 重置、RLS 模式切换（`RLS_MODE=shadow/enforce`）、批量数据操作，影响的是所有连到 `192.168.31.66` 的开发者和 agent，不只是当前会话。执行破坏性操作前先确认没有其他人正在使用（参见 [CLAUDE.md](../CLAUDE.md) 中关于 `-fresh` migrate 的警告）。
2. **派发并行/后台任务前，先确认远端是否已有同名 worktree 在跑。** 已有过因未核对而重复排期的教训（KAF 委派链路），核对方法与纪律见 [architecture-assessment-remediation-execution-plan-design.md](superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md) 的"五、执行纪律"一节。
3. 连接 `192.168.31.66` 所需的跳板、端口、鉴权等个人 SSH 配置不进入本文档；需要登录该机器做运维/调试时，向持有该配置的开发者确认。

---

## 2. Docker 部署与运维排查

### 生产环境启动（必须显式传入 env-file）

```bash
# 正确：显式传入 --env-file
docker-compose -f docker-compose.prod.yml --env-file .env.prod up -d

# 错误：缺少环境变量文件直接启动
docker-compose -f docker-compose.prod.yml up -d
```

### 常用排查命令

```bash
# 检查容器状态
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# 检查容器日志
docker logs <container> --tail 30

# 检查容器网络
docker inspect <container> --format '{{json .NetworkSettings.Networks}}' | jq -r 'keys[]'

# 从容器内测试后端健康检查 API
docker exec <container> wget -qO- http://localhost:8090/api/v1/health
```

### 网络隔离排查

生产容器与开发容器可能运行在不同网络：
- `itsm_itsm-network` - 开发网络
- `itsm_itsm-prod-network` - 生产网络
如遇 DNS 解析失败或容器间互通异常，先检查容器所在网络归属。

---

## 3. 复杂功能开发复盘教训（源自 dynamic-custom-fields 分支实践）

多任务拆解、逐任务评审的开发流程本身不会自动保证质量；以下几条是历史重构中实际踩过的坑，作为今后工作的检查项：

1. **"已完成"的历史结论必须重新验证，不能直接继承。**
   设计阶段得出的"这段代码零路由/零调用方"之类结论，到实际执行删除/重构前必须重新跑一遍验证命令，不能假设结论仍然成立——历史调查可能漏看了一条独立的调用链。发现结论与现状矛盾时，停下来重新调查、提给相关方确认范围，而不是凭旧结论盲目继续。
2. **任务级别的代码评审不能替代整分支的最终评审。**
   功能拆成多个子任务、每个任务分别评审通过，不代表功能作为整体是对的——跨任务的集成点（比如某任务的后端解析逻辑要跟另一任务的前端提交格式严格对齐）只有在看到完整分支 diff 的最终评审阶段才可能被发现。多任务功能必须有一次覆盖全分支 diff 的最终评审。
3. **修同一类缺陷时，主动搜索代码库里是否有其它地方犯过同样的错误。**
   修复某个结构性缺陷类别后，应该主动 grep 代码库里结构相似的其它调用点，而不是只改报告里点名的那一处。
4. **"清理型"修复本身要经过独立复核，不能拿到 implementer 自报的 DONE 就结束。**
   一次修复（比如清理孤儿数据）可能引入新的回归（如把软删除配成硬删除）。这类修复必须再过一轮独立于原实现者的复核，不能只信任自我报告。
5. **验证方法的"保真度"要讲清楚，不能用低保真度验证悄悄替代高保真度验证。**
   用 API 直接调用（如 curl）走查是合理的替代方案，但必须明确说明它覆盖不到哪些层（如前端 http-client 的请求体转换逻辑）。API 走查通过不代表真实浏览器路径没问题；报告里要老实写清楚哪部分测到、哪部分没测到。
6. **功能"看起来已完成"不等于真的可用，必须走一遍真实使用路径再下结论。**
   代码交付、单测/集成测通过，都不能替代一次端到端的真实操作验证；至少要用真实 HTTP 客户端路径走一遍主链路。
7. **复用旧代码时，要重新审视它当初依赖的假设是否还成立。**
   当一个此前半成品的功能第一次被真正打通、有真实数据流过时，依赖它的旧代码需要重新审视，不能假设其历史设计假设仍然合理。
