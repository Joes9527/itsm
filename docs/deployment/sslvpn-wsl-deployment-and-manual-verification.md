# SSLVPN：WSL 部署与手工端到端验收

状态：运行手册；命令与契约按 2026-09-08 两仓 SSLVPN 最终 feature 代码核对。本文提供操作步骤，**不表示新一轮部署、正常 Azure SSO 或真实授权已执行**。最终发布版本以两仓合并后的 `origin/main` SHA 为准。

配套交付 PR：[ITSM #10](https://github.com/Joes9527/itsm/pull/10)、[KAF #214](https://github.com/DawnproIN/kaf/pull/214)。部署前确认两者均已合并，再按第 3 节读取真实 main SHA；PR 已创建不等于已经合并。

适用主路径：在 `192.168.31.66` 的 WSL 建立两个新的部署 worktree，使用原 C4 隔离依赖和数据库，替换本次验收环境的应用进程，重新建立正常登录、身份映射、目录和审批配置，再由用户手工验收。保留原 feature、数据库历史和 ART 证据。

权威业务范围见[已确认设计](../superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)、[验证完成契约](../contracts/kaf-verified-access-completion.md)及[历史验收报告](../review/2026-09-05-sslvpn-end-to-end-verification-report.md)。成功含义是“指定 Graph 用户组授权经查询确认、ITSM 原子记录结果、KAF 展示同一申请结果”。VPN 登录、网段连通、到期自动回收、Teams/WeCom 不在本轮范围。申请有效期是记录的期限，不是自动撤权承诺。

## 1. 先确认要做什么

| 顺序 | 执行人 | 要做的事 | 放行证据 |
|---|---|---|---|
| 1 | 部署人员 | 确认两仓 main 已包含本次交付；建立干净部署 worktree | 两个 SHA、clean 状态、依赖版本 |
| 2 | 部署人员 / 数据库负责人 | 核对隔离资源、备份、停止原验收应用消费者；正式升级 | 目标库、备份、031 / 038、受限角色 |
| 3 | ITSM / KAF 管理员 | 配置凭据、正常身份、workspace、目录策略、BPMN 和审批人 | 本文配置清单全部有值且校验通过 |
| 4 | 请求者 / 主管 | 先跑拒绝申请 | 同编号拒绝、无委派、0 Graph add |
| 5 | 请求者 / 主管 / 网络运维 | 再跑一次两级批准申请 | 同编号、单次授权、查询确认和两端结果 |
| 6 | 验收操作员 / 权限恢复负责人 | 原始回执重放、证据收集、固定测试权限恢复 | 无第二次 add；原 remove 的 DELETE 响应及后续非成员 |

这里“普通使用”和“受控测试”不同：日常真实申请获批后保留权限；手工验收只使用[固定 Dev 对象](../testing/kaf-delegation-release-closeout-fixture.md)，由指定负责人恢复测试产生的权限。不得移除不属于本次测试的既有权限。

## 2. 环境事实与恢复前提

原 ITSM feature：`/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake`。
KAF 源码路径：`/home/administrator/.worktrees/kaf-sslvpn-unified-intake`。该路径在本次交付中恢复为持有自身 `.git` 目录的独立仓库，不再依赖 CI runner checkout 的 linked-worktree 元数据；已有源码与 ART 证据保留。部署前确认这项恢复完成，再从该独立仓库执行第 3 节的 `fetch` / `worktree add` 命令。

原 KAF 主 checkout 位于 CI runner 的 `/home/administrator/actions-runner/_work/kaf/kaf`。CI checkout 会清理工作目录，本次曾导致关联 worktree 的父级 Git 元数据丢失；因此 runner checkout **不得承载部署 worktree 的 Git 元数据**，也不能作为个人部署目录。本节只说明恢复后的目录关系，不要求用户重建、删除或搬动原仓库/证据。

原证据目录（以下称 ART）为 ITSM feature 下 `.superpowers/sdd/2026-09-05-sslvpn-end-to-end-implementation`；C4 运行证据位于 `ART/c4-live-runtime`。

| 资源 | 原 C4 地址 / 名称 | 本文用途 |
|---|---|---|
| PostgreSQL 17 + pgvector | `127.0.0.1:36446` | 复用，先备份；不是共享 5432 / 5434 |
| ITSM 数据库 | `sslvpn_a7_runtime_20260907` | 原历史、配置及专业数据 |
| ITSM app / system | `a7_runtime_app_20260907` / `a7_runtime_system_20260907` | 分开的受限连接 |
| KAF 数据库 / app | `sslvpn_b4_gateway_20260907` / `b4_gateway_app_20260907` | 原会话、checkpoint、委派账本 |
| Redis | `127.0.0.1:36445` | ITSM DB0；KAF gateway DB2；DB1 是测试用途 |
| Qdrant | `http://127.0.0.1:36483` | 原 `it-support` 内容，保留存储 |
| 真实 embedding | `http://localhost:11435/v1` | 原模型 `paraphrase-multilingual-MiniLM-L12-v2`，384 维；执行时重查 |
| 原 API / 前端 | API 36470、36475；前端 36474、36481 | 保留证据，复用数据库前精确停止原 API 消费者 |
| 新部署应用端口 | ITSM API 36570、前端 36574；KAF gateway 8000、前端 5173；Worker 36571 / 36572 | 本文命令使用这一组 |

2026-09-08 只读端口核对：原 36470/36475 API、36474/36481 前端及上述隔离依赖仍有监听；8000、5173 和 36570–36574 未见监听，3000 已被其他服务占用。**监听不是 readiness，也不是进程版本证明；执行时必须再核对。**

C4 已恢复：临时 users 14–17 inactive，审批角色权限和组成员清空，固定身份 mapping inactive，目录 9/10 内容与 KAF workspace 设置恢复，gateway 与两个 Worker 已停止。旧合成申请已通过 owning API 隔离。历史 000008 保持 unknown；000009 / WorkItem 13 保持原完成结果；累计真实 2 add / 2 remove。不要恢复旧 synthetic 任务或重授 000008。

**原 ART 的 Python、浏览器、初始化、cleanup 和 gateway launch 脚本都是历史证据，不是本文执行入口。**其中有创建临时身份、清理、调用真实 Graph 或替换运行 lifespan 的逻辑。不要批量执行；不要 source 原 kaf-dev 整份 `.env`；不要接触 KAF PROD `10.128.35.195`。

### 普通 WSL Dev 部署与本路径的差别

普通 Dev 可以采用自己的 PostgreSQL、Redis、Qdrant、workspace 和真实用户，但须独立配置全部目标和身份，并从正式初始化建立 schema。不能把本文 C4 的数据库名、用户 ID、目录 ID 复制到另一个环境。本文选择复用隔离库以保留真实迁移、角色和历史；**它不是全新环境一键安装，也不是生产发布步骤**。隔离依赖不存在或权限资料无法找回时，停止在此，按两仓数据库 provisioning 文档建立新的专用环境；不要 `docker compose up`、fresh/reset 或重建旧库猜测恢复。

## 3. 两仓同步与发布目录

以下命令在 WSL 的 Bash 执行。先确认两个 PR 已合并；未合并时不要部署当前旧 main。

```bash
export ITSM_SOURCE=/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake
export KAF_SOURCE=/home/administrator/.worktrees/kaf-sslvpn-unified-intake
export ITSM_DEPLOY=/home/administrator/.worktrees/itsm-sslvpn-deployment
export KAF_DEPLOY=/home/administrator/.worktrees/kaf-sslvpn-deployment
export SSLVPN_RUN=/home/administrator/.local/state/sslvpn-wsl
umask 077
git -C "$ITSM_SOURCE" status --short
git -C "$KAF_SOURCE" status --short
git -C "$ITSM_SOURCE" fetch origin main
git -C "$KAF_SOURCE" fetch origin main
git -C "$ITSM_SOURCE" log -5 --oneline origin/main
git -C "$KAF_SOURCE" log -5 --oneline origin/main
test ! -e "$ITSM_DEPLOY" && test ! -e "$KAF_DEPLOY"
git -C "$ITSM_SOURCE" worktree add --detach "$ITSM_DEPLOY" origin/main
git -C "$KAF_SOURCE" worktree add --detach "$KAF_DEPLOY" origin/main
mkdir -p "$SSLVPN_RUN"/{bin,config,logs,evidence,backup}
git -C "$ITSM_DEPLOY" rev-parse HEAD > "$SSLVPN_RUN/evidence/itsm-sha.txt"
git -C "$KAF_DEPLOY" rev-parse HEAD > "$SSLVPN_RUN/evidence/kaf-sha.txt"
git -C "$ITSM_DEPLOY" status --porcelain
git -C "$KAF_DEPLOY" status --porcelain
```

逐条执行，任一步失败即停止。若目标已存在，检查 `git status`、当前 HEAD、进程归属后再决定复用，不能删除、reset 或覆盖。仅在某仓已有主 checkout 清洁且没有其他任务提交时才可使用 `git switch main` / `git pull --ff-only`；本路径无需改动主 checkout。两仓必须成对部署：KAF schema 038 本身不能证明消费者兼容，旧代码可能误读保存的 failure payload。

依赖检查：Go 版本满足 ITSM `go.mod`（执行 `go version` 对照），Node ≥22 / npm ≥10，Python ≥3.12、uv、PostgreSQL 17 客户端。先检查现有版本，不修改全局工具链。构建到新目录：

```bash
cd "$ITSM_DEPLOY/itsm-backend"
go build -o "$SSLVPN_RUN/bin/itsm-api" .
go build -o "$SSLVPN_RUN/bin/itsm-kaf-worker" ./cmd/kaf_worker
cd "$KAF_DEPLOY"
uv sync --frozen
cd "$ITSM_DEPLOY/itsm-frontend"
npm ci
cd "$KAF_DEPLOY/frontend"
npm ci
```

## 4. 备份、数据库与配置

### 4.1 先备份并交接原消费者

先用 `ss -ltnp`、`ps -p <PID> -o pid,lstart,args` 和 `/proc/<PID>/cwd` / `exe`，对照 `ART/c4-live-runtime/processes.json` 的 PID、exe、cwd、startTicks；PID 已被复用时不得发信号。由运行负责人对**已核对的原 C4 API 两进程**逐个 `kill -TERM <PID>`，确认停止；gateway / Worker 若意外仍运行，同样核对后停止。不要 `pkill python`、`killall` 或杀 3000 等其他服务。原前端可以保持只读，但不要通过旧页面提交新申请。

用数据库负责人提供的 owner 凭据创建权限为 0600 的 `.pgpass`（不要放进命令参数或历史）。以下备份只针对固定隔离库：

```bash
pg_dump -h 127.0.0.1 -p 36446 -U postgres -Fc -d sslvpn_a7_runtime_20260907 -f "$SSLVPN_RUN/backup/itsm-before.dump"
pg_dump -h 127.0.0.1 -p 36446 -U postgres -Fc -d sslvpn_b4_gateway_20260907 -f "$SSLVPN_RUN/backup/kaf-before.dump"
pg_restore --list "$SSLVPN_RUN/backup/itsm-before.dump" > "$SSLVPN_RUN/evidence/itsm-backup-manifest.txt"
pg_restore --list "$SSLVPN_RUN/backup/kaf-before.dump" > "$SSLVPN_RUN/evidence/kaf-backup-manifest.txt"
```

`--list` 仅证明备份可读取，不能代替恢复抽检。数据库负责人还需保存角色/grant、Redis 持久化和 Qdrant 快照或停写备份，记录可恢复位置；备份含用户数据与凭据资料，不上传公开 PR。正式升级前完成一次隔离恢复抽检。只读核对旧队列：旧 synthetic 没有待执行任务；KAF `itsm_job` 无可执行旧任务；未知记录保持原状态。原 KAF gateway 的正常 lifespan 会启动其既有 job processor，旧 C4 wrapper 曾停止该循环，不能靠原脚本掩盖本次队列风险。

### 4.2 ITSM 配置文件

复制新版本 `itsm-backend/config.yaml` 至 `$SSLVPN_RUN/config/config.yaml`，只在这个未跟踪文件中修改。至少逐项填入：

| 配置项 | 本路径的值 / 来源 |
|---|---|
| `database.host/port/dbname` | `127.0.0.1` / **36446** / `sslvpn_a7_runtime_20260907` |
| `database.user/system_role_user` | `a7_runtime_app_20260907` / `a7_runtime_system_20260907` |
| `database.password/system_role_password` | 保持空，运行时由下面 `_FILE` 注入；不要保留示例密码 |
| `server.port/frontend_url` | **36570** / `http://localhost:36574` |
| `redis.host/port/db` | `127.0.0.1` / 36445 / 0；密码由负责人提供 |
| `deployment.auto_migrate/auto_seed` | `false` / `false` |
| `smtp.enabled` | 本次关闭；邮件不属于验收 |
| `log.path` | `$SSLVPN_RUN/logs/itsm` 的实际绝对路径；避免 debug 或原始 provider body |

注意：仓库 YAML 的 `database.port` 和 `server.port` 是数值；不能仅 export 一个未经代码支持的 `DB_PORT` / `SERVER_PORT`，就认为端口改变。配置 loader 从**进程当前目录**或其 `config/` 读取 `config.yaml`。

创建 `$SSLVPN_RUN/config/itsm.env`，按实际绝对路径填写（下例 `<...>` 必须替换，不能直接运行）：

```dotenv
ENV=development
SERVER_MODE=release
DEPLOYMENT_MODE=private
ITSM_AUTO_MIGRATE=false
ITSM_AUTO_SEED=false
RLS_MODE=enforce
DB_PASSWORD_FILE=<ITSM app 密码文件绝对路径>
DB_SYSTEM_ROLE_PASSWORD_FILE=<ITSM system 密码文件绝对路径>
JWT_SECRET_FILE=<ITSM JWT 独立签名秘密文件>
INTAKE_IDENTITY_CONFIG_FILE=<身份交换 providers.json 绝对路径>
KAF_WEBHOOK_URL=http://127.0.0.1:8000/webhooks/itsm
KAF_WEBHOOK_SECRET_FILE=<Worker 与 KAF 共享的 webhook HMAC 秘密文件>
FRONTEND_URL=http://localhost:36574
LOG_LEVEL=info
```

各秘密文件及 `.env` 均为 0600。直接变量和同名 `_FILE` 不可同时赋值。ITSM API 与 Worker 不能使用 owner、admin 回落或共享超级账号；`DB_APP_ROLE_*` / `DB_ADMIN_ROLE_*` 不应遗留在运行环境。

现有 runtime app 必须 nonowner、NOSUPERUSER、NOBYPASSRLS、无继承角色；system 必须独立的 NOSUPERUSER、BYPASSRLS、NOINHERIT、NOCREATEROLE、NOCREATEDB、NOREPLICATION、无角色成员关系，只拥有代码允许的目录/outbox 权限。**system 不是可任意访问业务表的超级账号**。完整 allowlist、列/序列和 PUBLIC 权限检查以 [`runtime_clients.go`](../../itsm-backend/database/runtime_clients.go) 为准。两连接须同库同 schema，不能给 system `GRANT ALL` 来绕过启动拒绝。

### 4.3 正式升级与启动隔离

ITSM：先将 `config.yaml` 复制为单独 `$SSLVPN_RUN/config/owner/config.yaml`，仅此副本使用数据库 owner；其环境文件不携带 app/system 凭据。保持原发布迁移 checksum，绝不篡改账本。现有 C4 已至 031；新 binary 的重复正式初始化仍应成功：

```bash
# 在独立子 shell 载入新建、已审查的 owner.env；它提供迁移用户和密码。
env -i PATH="$PATH" LANG=C.UTF-8 bash --noprofile --norc -c '
  set -a; . "$1"; set +a
  cd "$2"
  ITSM_BOOTSTRAP_ONLY=true ITSM_AUTO_MIGRATE=true ITSM_AUTO_SEED=false "$3"
' bash "$SSLVPN_RUN/config/owner.env" "$SSLVPN_RUN/config/owner" "$SSLVPN_RUN/bin/itsm-api"
```

这是 `main.go → RunInitialization → InitializeStorage` 的正式入口，包含 Ent schema、注册迁移、RLS/invariant 校验；不是手工执行一份 031 SQL。现有库不重新 seed。初始化成功后，用 owner **只读**查询迁移账本，确认含 `031_kaf_action_request_digest`，保留 checksum。新空环境才按[初始化运行手册](../runbooks/production-initialization.md)执行种子/租户开通和 `cmd/initialize -action plan|apply|status|verify`；它与 schema 初始化是不同职责，不能拿 `status` 冒充迁移。不要使用 fresh/reset/down。

KAF：新建 `$SSLVPN_RUN/config/kaf-owner.env`，明确 `DATABASE_URL=postgresql+asyncpg://<owner>:<encoded-password>@127.0.0.1:36446/sslvpn_b4_gateway_20260907`；所有 URL 密码需正确 URL 编码。运行正式入口：

```bash
cd "$KAF_DEPLOY"
env -i PATH="$PATH" LANG=C.UTF-8 ENV_FILE="$SSLVPN_RUN/config/kaf-owner.env" \
  "$KAF_DEPLOY/.venv/bin/python" -m acp.infrastructure.schema_management
```

预期输出 `Application and checkpoint schemas are current.`，`alembic_version` 为 `038_kaf_execution_phase`，checkpoint ledger 及真实查询通过。此命令无 `--upgrade`、`--revision` 等额外参数。权威说明是 KAF `docs/kaf2/operations/database-provisioning.md`。不能 `create_all`、stamp 或删除 ledger。

KAF app 仅获得业务/checkpoint DML、必要序列权限以及 schema / Alembic / checkpoint ledger 读取权限，无 DDL、owner 权限。常驻启动将 `ALLOW_STARTUP_SCHEMA_MANAGEMENT=false`、`ALLOW_STARTUP_CONFIG_SEED=false`，正常检查只读。迁移会分步提交，失败后记录实际已完成版本，不能假定全程回滚。

### 4.4 KAF gateway 必填配置

新建 `$SSLVPN_RUN/config/kaf.env`，用本次 Dev 凭据管理资料逐项填值。`ENV_FILE` 是正式支持的选择入口；不要导出旧环境全部变量。

```dotenv
RUNTIME_MODE=development
HOST=127.0.0.1
PORT=8000
FRONTEND_BASE_URL=http://localhost:5173
CORS_ORIGINS=["http://localhost:5173"]
DATABASE_URL=postgresql+asyncpg://b4_gateway_app_20260907:<encoded-password>@127.0.0.1:36446/sslvpn_b4_gateway_20260907
REDIS_URL=redis://:<encoded-password>@127.0.0.1:36445/2
QDRANT_URL=http://127.0.0.1:36483
QDRANT_API_KEY=<若启用认证则填写实际 Dev key，否则留空>
EMBEDDING_API_URL=http://localhost:11435/v1
EMBEDDING_API_KEY=<Dev embedding 服务要求的值>
EMBEDDING_MODEL=paraphrase-multilingual-MiniLM-L12-v2
EMBEDDING_DIMENSIONS=384
LLM_BASE_URL=<已批准的 Dev provider URL>
LLM_MODEL=<可用且支持本工作区工具调用的模型名>
LLM_API_KEY=<该 provider 的 Dev key>
ACP_JWT_SECRET=<KAF 专用强随机秘密>
AZURE_TENANT_ID=<Dev Entra tenant UUID>
AZURE_CLIENT_ID=<Dev app registration UUID>
AZURE_CLIENT_SECRET=<该 Dev app 的有效 secret>
AZURE_REDIRECT_URI=http://localhost:8000/auth/oidc/callback
ADMIN_EMAILS=<实际授权的 KAF 管理员邮箱>
IT_BACKEND=graph
VPN_USERS_GROUP_ID=<固定 Dev fixture 的 group Object ID；用于受控 remove>
ITSM_INTAKE_URL=http://127.0.0.1:36570
ITSM_INTAKE_PROVIDER=<与 ITSM providers.json 完全一致的 KAF provider key>
ITSM_INTAKE_CHANNEL=kaf_web
ITSM_INTAKE_EXCHANGE_SECRET_FILE=<独立 exchange secret 文件绝对路径>
ITSM_KAF_URL=http://127.0.0.1:36570
ITSM_KAF_AUTOMATION_TOKEN=<有效 ITSM kaf_automation 账号 token>
ITSM_KAF_WEBHOOK_SECRET=<与 ITSM KAF_WEBHOOK_SECRET_FILE 内容相同>
ITSM_KAF_RECOVERY_ALERT_RECIPIENTS=
ALLOW_STARTUP_SCHEMA_MANAGEMENT=false
ALLOW_STARTUP_CONFIG_SEED=false
ALLOW_STARTUP_IDEMPOTENT_SEED=false
ALLOW_MEMORY_CHECKPOINTER_FALLBACK=false
ALLOW_PLATFORM_TOOL_INIT_FAILURE=false
ALLOW_EXTERNAL_ACTION_OBSERVABILITY_FAILURE=false
ALLOW_ENTERPRISE_CONTEXT_DEGRADED=false
EXTERNAL_ACTION_RECOVERY_ENABLED=true
EHR_SYNC_ENABLED=0
HRIS_CRAWLER_ENABLED=0
ITSM_CRAWLER_ENABLED=0
ITSM_LIFECYCLE_POLL_ENABLED=false
EMPLOYEE_LIFECYCLE_ENABLED=false
O365_POLLER_ENABLED=false
SR_BATCH_ENABLED=false
SR_BATCH_AUTO_APPROVE=false
VPN_VERIFY_ENABLED=0
```

配置权威：KAF `src/acp/config.py`。Graph client 使用 `AZURE_*`，不能以一个未被该 client 读取的 `GRAPH_CLIENT_SECRET` 代替。`ITSM_KAF_URL` 和 `ITSM_INTAKE_URL` 都是 base URL，不加 `/api/v1`。禁用其他业务轮询不替代检查旧 `itsm_job` 队列。

**必须由用户 / 环境负责人补齐的值：**Dev app secret、Graph tenant/client、真实 Azure OID、真实 ITSM 请求者/审批人账号、KAF workspace UUID 与成员、有效 automation token、数据库密码、JWT/HMAC/exchange secrets、LLM provider/model/key、embedding 认证值、备份位置和恢复负责人。代码不会提供这些秘密；历史 fixture JWT 不等于正常 SSO。用 Dev Entra 管理页核对 app、Object ID、redirect URI 和已授予的组成员读写能力；用两端正常登录的 `/auth/me` 核对真实身份，不从邮箱字符串推断映射。

## 5. 正常身份、审批人和目录配置

### 5.1 先启动 API 与两个前端、gateway

API 需要数据库和新配置；gateway 可先启动供正常登录，但在旧任务检查、固定对象基线和配置核对完成前**不要启动 Worker、不要批准正向申请**。

在独立终端启动 API（前台便于看错误和 Ctrl-C 停止）：

```bash
env -i PATH="$PATH" LANG=C.UTF-8 bash --noprofile --norc -c '
  set -a; . "$1"; set +a; cd "$2"; exec "$3"
' bash "$SSLVPN_RUN/config/itsm.env" "$SSLVPN_RUN/config" "$SSLVPN_RUN/bin/itsm-api"
```

其他独立终端分别运行：

```bash
cd "$KAF_DEPLOY"
env -i PATH="$PATH" LANG=C.UTF-8 ENV_FILE="$SSLVPN_RUN/config/kaf.env" \
  "$KAF_DEPLOY/.venv/bin/python" -m uvicorn acp.main:app --host 127.0.0.1 --port 8000
```

```bash
cd "$ITSM_DEPLOY/itsm-frontend"
NEXT_PUBLIC_API_URL='' ITSM_BACKEND_URL=http://127.0.0.1:36570 npm run dev -- --hostname 127.0.0.1 --port 36574
```

```bash
cd "$KAF_DEPLOY/frontend"
npm run dev -- --host 127.0.0.1 --port 5173 --strictPort
```

每个新终端先重设第 3 节的目录变量。此处 Next/Vite 是手工 Dev 验收服务器；若采用长期服务托管，再由部署负责人配置服务单元，不能把终端存活当生产部署。

WSL 内检查：`curl -fsS http://127.0.0.1:36570/api/v1/readyz`、`curl -fsS http://127.0.0.1:8000/health`。KAF `/health` 仅返回进程存活，需同时检查严格 schema / checkpoint 启动未降级、数据库及 Redis 可用、正常登录、LLM 与 RAG 查询成功；不存在可替代全部这些检查的虚构 `/readyz`。

KAF Vite 的 `/api`、`/ws`、`/health` proxy 在 `frontend/vite.config.ts` 指向 `localhost:8000`，所以本文保留 gateway 8000。若该端口被占用，不要杀其他服务或只改 gateway 端口；另行在未跟踪的部署 Vite 配置中同步全部代理目标。

Windows 本机浏览器优先打开 `http://localhost:36574`（ITSM）及 `http://localhost:5173`（KAF），前提是 Windows→WSL localhost forwarding 可用且对应端口没有 Windows 进程占用。Azure app registration 的 redirect URI 必须精确登记 `http://localhost:8000/auth/oidc/callback`，浏览器也必须能访问该端口。远程 Mac/另一台 PC 的 localhost 指向它自己，不能直接用上述 URL；可先建立 SSH 本地隧道：

```bash
ssh -N -p 22222 -L 36574:127.0.0.1:36574 -L 5173:127.0.0.1:5173 -L 8000:127.0.0.1:8000 administrator@192.168.31.66
```

这要求 22222 确实进入该 WSL，且本机三个端口空闲。主机密钥按已有可信配置校验，不关闭校验。不要把 `127.0.0.1:5173` 随意改成 `0.0.0.0`。注意现有 ITSM API/Worker Go 监听实现为 `:<port>`，配置没有独立 bind-host 项；负责人须检查 Windows/WSL 防火墙，限定 API/health 的访问范围，不能宣称这些端口天然仅 loopback。

### 5.2 四类人类账号与机器账号

ITSM 通过 `/login` 正常用户名密码登录（需要时由管理员在 `/admin/users` 建立账号）；如选择 ITSM Azure 登录，另须配置 ITSM `AZURE_*`、`AZURE_ITSM_TENANT_CODE` 和 tenant provisioning 策略，本路径不以它替代 KAF 的 Azure SSO。

KAF 在登录页点击 Microsoft 登录，实际走 `/api/auth/oidc/start` → Azure → `/auth/oidc/callback` → 前端 `/auth/callback`。不要把旧 fixture JWT 填 localStorage 代替登录。管理员用 `ADMIN_EMAILS` 中经过批准的身份登录，再核对 `/auth/me` 的 scopes；申请者保持普通用户，不借管理员身份发起申请。

| 身份 | ITSM 配置入口与权限 | 验证方法 |
|---|---|---|
| 配置管理员 | `/admin/users`、`/admin/roles`、`/admin/groups`、`/admin/service-catalogs`、`/workflow/versions`；对应 user/role/group、service_catalog、workflow、intake_identity_mapping 的 read/write；受租户约束 | `/api/v1/auth/me` 与目标租户一致；需要跨租户时走正常 switch-tenant |
| 请求者 | 本租户 active 用户，`service_request:create`、`service_request:read`、`service_catalog:read`、所需 WorkItem 读取权限；KAF workspace 普通 member | 正常登录后的真实 Azure OID 映射到该 ITSM user；能读目录、自己的申请 |
| 主管 | active 用户；角色 code `dept_manager`，至少 `workflow:read`、`workflow:write`、`ticket:read`、`service_request:read`、`service_catalog:read`、`user:read`；加入同名审批 group | 独立浏览器能看到该编号一级待办，领取后操作；不能是请求者 |
| 网络运维 | 另一个 active 用户；角色 code `network_eng`，同上权限，加入同名 group | 一级通过前无该编号二级待办，通过后可领取和审批 |
| KAF automation | 独立 active `kaf_automation` ITSM 账号，属于目标租户；通过正常 `/api/v1/auth/login` 取得有效 token | `GET /api/v1/bpmn/process-tasks/kaf-delegated` 认证成功；不授管理员/申请者身份 |

实际权限键以当前服务端权限列表、角色编辑与 API 返回为准，不用菜单可见代替授权。审批候选以 group 关系验证，**仅创建同名角色不够**。原 C4 临时 actors 已停用，不直接沿用 16/17 或旧密码。若复用原组，读取成员后只增加本次明确批准的人；不清空其他成员。

无对应 UI 字段时，使用现有 authenticated API：`POST /roles`、`POST /users`、`POST /groups`、`POST /groups/{id}/members`（body `{"userId": <id>}`），上述路径均在 `/api/v1` 下。cookie 认证写操作先 `GET /csrf-token`，携带返回值的 `X-CSRF-Token`，并保留同一 cookie 会话；读取 HTTP status 之外还要确认 ITSM response `code=0`。不要把 curl 导出的 token、cookie 或登录 body 保存进公开证据。

automation token 有有效期；新 gateway 启动前核对，过期时用原账号正常刷新/登录更换配置再重启 gateway。`ITSM_KAF_AUTOMATION_TOKEN` 仅用于任务 context/actions；用户创建/读申请必须走下一节的专属 exchange。Webhook HMAC、automation bearer、ITSM JWT 签名、KAF JWT 签名、exchange HMAC 是五种职责，不能互相代用。

### 5.3 两条身份映射，不能混成一条

在 ITSM owner-only `providers.json` 登记 KAF exchange provider 以及 Graph provider。例如（秘密占位，不直接部署）：

```json
{
  "providers": {
    "kaf-wsl": {"secret": "<独立exchange秘密>", "channels": ["kaf_web"], "purposes": ["create", "read"]},
    "graph": {"secret": "<另一个独立随机provider秘密>", "channels": ["directory"], "purposes": ["read"]}
  },
  "maxAge": "1m", "futureSkew": "5s", "tokenTTL": "5m"
}
```

Graph 条目在此登记 mapping provider，不表示浏览器可拿它换取管理员身份。KAF `ITSM_INTAKE_PROVIDER=kaf-wsl`；`ITSM_INTAKE_EXCHANGE_SECRET_FILE` 的原始文本等于 KAF 条目的 secret。ITSM config 有严格文件权限/字段/用途检查；修改后重启 API/Worker 使其读到新配置。Redis 负责 nonce 防重放，WSL 时钟必须准确。

管理员在正确 ITSM tenant 调用 `POST /api/v1/intake/identity-mappings` 建立两条映射，body 分别为：

```json
{"provider":"kaf-wsl","workspace":"<KAF it-support workspace UUID>","subject":"<KAF /auth/me 的 sub，也是真实 Azure OID>","userId":123}
```

```json
{"provider":"graph","workspace":"<目录 accessPolicy.externalSystem>","subject":"<固定 Dev 用户 Graph Object ID>","userId":123}
```

`123` 仅为格式示例，必须换成刚核对的本租户真实请求者。第一条授权当前用户跨系统创建/读取；第二条供 ITSM 冻结审批目标 subject。Graph Object ID 从 Dev Entra/Graph 只读查询取得，不把 UPN 或邮箱填为 Object ID。创建返回 mapping ID/version，保存到受限部署清单。`GET /intake/identity-mappings` 是去敏列表，不回显原 subject/workspace；旧 mapping 来源需从受限部署清单或数据库负责人只读核对，不能凭用户 ID 猜测。复用已确认同一条 inactive mapping 时 `PATCH /intake/identity-mappings/{id}` body `{"version":<当前版本>,"active":true}`，冲突先重读；不能新建碰撞映射或直接改表。

### 5.4 KAF workspace 与 Procedure

KAF 管理员通过 `GET /workspaces` 找到 slug=`it-support` 的实际 UUID，`GET /workspaces/{uuid}/members` 确认请求者成员关系，必要时 `POST /workspaces/{uuid}/members`，body 为 `{"user_id":"<KAF用户UUID>","role":"member"}`（schema 见 KAF `src/acp/schemas/workspace.py`）。正常新用户首次登录可能自动加入已有 it-support，已有用户仍要核查，不能假设。

读取当前 workspace 全部 settings，保留现有 agent prompt、model、tool、tenant facts 等配置，用 `PATCH /workspaces/{uuid}` 的 `settings` 更新以下键（接口替换整个 settings 对象，必须先合并，不能仅发送两个键把其他配置抹掉）：

```json
{
  "itsm_tenant_id": "<ITSM tenant 数字ID的字符串>",
  "delegated_access_procedures": {
    "<与accessPolicy.externalSystem一致>": {
      "provider": "graph", "capability": "external_group_grant",
      "intent": "graph_vpn_access_grant", "tool": "ad_grant_vpn_access"
    }
  }
}
```

只能有一个 workspace 映射到该 ITSM tenant。这里 RAG workspace key 是 `it-support`（不是 UUID）。真实内容源为 KAF `scripts/procedures/graph_vpn_access_grant.md`，恰好包含一个 `itsm_approval` gate 和登记的 grant tool；不能加入静态用户/组参数、LDAP 路径或假向量。

内容负责人先核对 Qdrant 快照、现有 workspace 文档清单和批准的完整内容目录，再运行正式 CLI：

```bash
cd "$KAF_DEPLOY"
ENV_FILE="$SSLVPN_RUN/config/kaf.env" "$KAF_DEPLOY/.venv/bin/kaf-ingest" \
  --procedures-dir "$KAF_DEPLOY/scripts/procedures" --workspace-id it-support
```

**这个 CLI 会同步整个目录并删除该 workspace 在相关 collection 的孤立条目。**不能为省事把只含一个文件的临时目录作为完整 sync 目录；若现有内容还有未纳入仓库的文档，应先由内容负责人整理完整已批准源集合，或使用现有单文档 owner `acp.procedures.rag.ingest_document_text`（真实签名见源码）进行定点发布。不要让本文命令删除环境独有文档。

确认 exit 0、failed=0；再以 `get_by_intent("graph_vpn_access_grant", "it-support")` 读取精确 intent 并核对内容 hash，使用 workspace 的真实检索验证非零 384 维 embedding / Qdrant collection 维数一致。CLI 成功与“碰巧旧文档还在”不同，记录本次源码文件 SHA256 与已读取内容。LLM readiness 用正常 KAF 对话验证工具调用和确认卡，不用单独 `/health` 代替。

### 5.5 目录、有效期、固定绑定与 BPMN

ITSM 管理员在 `/admin/service-catalogs` 找到本租户 SSLVPN 目录。C4 曾用 tenant 3 / catalog 9，但已恢复，**必须重读当前目录**。保留修改前 `GET /api/v1/service-catalogs/{id}` 响应及 `catalogVersion` / `formSchemaVersion`。

配置满足下列条件：

1. `targetClass=service_request_item`，`serviceType=access`，需要审批；仅配置完成后 `status=enabled`。
2. `fields` 中有效期字段为 required 的 `select`；所有 `{value,label}` 与 `accessPolicy.durationOptions` 的 `{key,label,seconds}` 一一匹配，seconds 为正且有限。例如 `30d / 30天 / 2592000`。不要另设一个自由文本到期时间替代该字段。
3. `accessPolicy.provider=graph`，`externalSystem` 与上一节的 workspace binding、Graph mapping 完全相同；`groupId` 为固定 Dev security group Object ID；`durationField` 为该 select 的真实 name。目标来自管理员策略，申请者卡片不能覆盖 subject/group。
4. 保存后读取实际 policy ID/version；不要假设仍是历史 ID 5。更新目录使用 `PUT /service-catalogs/{id}`，body 必须有刚读取的 `expectedCatalogVersion`，只提交要改的字段；409 时重读确认，不自动覆盖。
5. BPMN 源使用 [`sslvpn_approval_flow.bpmn`](../../itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn)，发布副本将唯一 `CATALOG_ACCESS_POLICY_REQUIRED` 换成**本目录 policy ID 的十进制文本**。不改旧实例定义，不用 XML version 文本推断数据库 definition ID。
6. 通过 `/workflow/versions` 或 `POST /bpmn/versions` 发布新版本，字段为 `processDefinitionKey`、`name`、`bpmnXml`、`changeLog`；再 `PUT /bpmn/versions/{key}/{returned-version}/activate`。若尚无模板 key，用 `/admin/workflows` 的模板 owner 先建立模板；不要直接写 process_definitions 表。
7. 目录 `processDefinitionKey=sslvpn_approval_flow`，显式绑定已激活正式版本；读取落库 XML 核对配置后 SHA256。必须包含主管与网络两级、两个拒绝分支、`kaf_delegate / external_group_grant`、本 policy ref，以及 `complete_bpmn_task,record_execution_failure` 两种 allowed actions。内容不符不得继续。

目录存在配置相互依赖时，先保持 disabled 保存 policy 与字段，再发布绑定该 policy 的版本，最后带新 `expectedCatalogVersion` 启用目录；发布 owner 拒绝某一步时先查确切缺失项，不删校验或加 `no_process`。旧 C4 catalog 10 是其他租户合成目录，不能为本申请启用；原已批准/unknown 实例仍使用其冻结定义。

最终以请求者正常 KAF 会话读取目录契约：只看到本人可申请目录和有效期选项；更改目录后卡片需重新读取版本，不能继续提交过期确认快照。

## 6. Worker 与开始验收的最后门禁

先确认当前固定 Dev 用户身份和 group 完全匹配，通过 Graph 只读 GET 建立非成员基线。若已有权限，先查归属：已有真实业务权限不属于本测试，不移除；固定测试对象的历史测试权限由明确负责人按恢复流程处理后再开始。未经核对不以一次 404 认定目标正确或开始 grant。

管理员核对 KAF 旧 `itsm_job`、未完成 delivery、ITSM delegated/outbox 列表；仅允许本次新申请可被执行。unknown / legacy_unknown / write_pending / verification_pending 不准重置；未明来源任务先通过 owning 运维流程隔离。旧 000008 不复活。

在两个独立终端运行相同 Worker binary，仅 health 端口不同：

```bash
env -i PATH="$PATH" LANG=C.UTF-8 bash --noprofile --norc -c '
  set -a; . "$1"; set +a; cd "$2"
  export KAF_WORKER_HEALTH_PORT=36571
  exec "$3"
' bash "$SSLVPN_RUN/config/itsm.env" "$SSLVPN_RUN/config" "$SSLVPN_RUN/bin/itsm-kaf-worker"
```

第二个终端相同命令，将 `36571` 改为 `36572`。它们是独立进程和租约竞争者；不要把 API 内后台任务当这两个 Worker。

```bash
curl -fsS http://127.0.0.1:36571/readyz
curl -fsS http://127.0.0.1:36572/readyz
curl -fsS http://127.0.0.1:36570/api/v1/readyz
curl -fsS http://127.0.0.1:8000/health
```

保存所有进程 PID、exe、cwd、启动时间、发布 SHA；health 200、同一数据库、不同 Worker health 端口、正确 webhook HMAC 是必要条件，还需以下业务检查全部通过。

## 7. 手工端到端流程

使用三个**独立浏览器配置文件**：请求者（KAF 和 ITSM）、主管（ITSM）、网络运维（ITSM）。两个普通无痕窗口可能共享同一登录态，不足以证明身份隔离。配置管理员另用自己的窗口，只配置和查看，不代替请求者或审批人。

### 7.1 先做拒绝路径（没有 Graph 授权）

1. 请求者在 KAF `it-support` 新会话输入“我要申请 SSLVPN 访问权限，用于远程办公”。按对话补充原因、选择目录规定的有效期。
2. 在**原确认卡**核对服务、原因、有效期、当前身份，只确认一次。等待创建回执；记录 KAF session/card/action ID、ITSM WorkItem ID 和唯一编号。没有编号就先排错，不再新建一张申请绕过。
3. 点击原回执详情，在 ITSM 查看同编号专业 Service Request。应显示等待主管审批，不能显示“权限已开通”。
4. 主管在 `/workflow/ticket-approval` 按该唯一编号定位一级任务，核对请求者/原因/有效期，**先领取，再拒绝**，输入可识别的测试原因。
5. 请求者刷新 ITSM 详情，再在 KAF 原卡“刷新详情”，离开并重开同会话，确认都显示该编号拒绝。网络审批/委派任务没有产生，Graph 审计或受控 transport 证据证明本轮 0 add，不能只凭页面拒绝推断无外部写入。

### 7.2 再做一次批准路径

1. 再次只读确认固定对象为非成员；保留时间和目标匹配结果。开**新的会话/申请**，正常对话 → 原确认卡选择有效期 → 确认一次 → 取得新的唯一编号。新的编号与拒绝编号不能混用。
2. 主管在独立窗口按新编号找到一级任务，领取，核对申请有效期并批准。保留审批 actor、task ID、decision 与时间。
3. 网络运维刷新自己的待办，找到同编号二级任务，领取并批准。此时应有两个不同人类 actor 的审批证据。不可直接改审批表或后台脚本批准。
4. ITSM 创建委派/Outbox；两个 Worker 中一个领取并投递 KAF。KAF读取获批 context → workspace binding → 正式 Procedure → 当前授权范围验证 → 只读成员基线 → 持久化 write fence → **一次** Graph add → 有限 GET 验证 → 持久化原始动作回执 → ITSM原子完成。
5. 在 ITSM 同编号专业详情确认 `fulfillmentState=completed`、`accessResult` 存在、`verifiedAt` 为本次首次查询确认时间、`expiresAt` 等于该时间加获批 duration；界面“已完成 / 授权已验证”。不要只看 tickets 表的 base status，也不要手工点“完成交付”伪造结果。
6. KAF 原卡刷新、切换会话后重开，确认同一 WorkItem/编号显示“权限已开通”和同一验证时间/有效期。读取拒绝应清除 live view，旧创建回执只证明创建，不能证明目前可读或已成功。
7. 核对 Graph add 数量与查询确认，记录 provider 状态码、时刻、correlation/evidenceRef 的去敏摘要；授权路径无需 VPN 登录。若 baseline 已是成员，应显示 already_present，不能把它算新增授权，也不能生成本申请托管的到期时间。

### 7.3 原回执重放与可选 Worker 恢复

重放指 KAF 将**原已持久化完整 action payload**再次提交给 ITSM，保持 action、tenant/task/run/step、幂等键、expectedVersion、verifiedAt、evidenceRef 不变；不是用户再次点击新申请，不是再次运行 grant，也不是重新查当前成员后造一个完成回执。

优先观察原 delivery 的自动恢复是否在确认丢失后仅重送已保存 payload；如做受控人工重放，由运维人员使用 KAF 现有认证客户端/任务动作 owner，读取本次保存的 payload 原样提交，禁止编辑字段。原接口为 `POST /api/v1/bpmn/process-tasks/{taskId}/actions`；KAF `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py` 中 `HttpKafItsmContextClient.complete_kaf_task(task_id, payload)` 使用配置的 automation token。预期返回 `already_applied`、receipt 仍一份、专业结果与 verifiedAt/expiresAt 不变、Graph add 不增加。本手工基础流程不要求人为制造丢响应；未执行重放就记录“未验证”，不能借历史 C4 结果冒充本轮。

可执行重放示例：先由数据库负责人将**本次** `kaf_delegation_deliveries` 的 `task_id` 与 `completion_payload` 原样导出为权限 0600 的 JSON，格式 `{"taskId":"<本次TASK...>","payload":{...原始完整payload...}}`，保存到 `$SSLVPN_RUN/evidence/original-action.json`。用 WorkItem、task、tenant、event/action 核对本轮归属；不使用旧申请文件。以下只调用原动作接口，不执行 Procedure 或 Graph：

```bash
cd "$KAF_DEPLOY"
ENV_FILE="$SSLVPN_RUN/config/kaf.env" "$KAF_DEPLOY/.venv/bin/python" - "$SSLVPN_RUN/evidence/original-action.json" <<'PY'
import asyncio, json, sys
from pathlib import Path
from acp.orchestration.headless_tasks.kaf_delegation_pipeline import HttpKafItsmContextClient
record = json.loads(Path(sys.argv[1]).read_text())
assert isinstance(record["payload"], dict) and record["taskId"]
async def main():
    result = await HttpKafItsmContextClient().complete_kaf_task(record["taskId"], record["payload"])
    print(json.dumps({"status": result.get("status"), "action": result.get("action")}, ensure_ascii=False))
asyncio.run(main())
PY
```

随后用专业详情与账本只读比较原 receipt 和两个时间；不将原 payload 打印到终端或公开报告。

可选恢复实验仅在负责人能控制本次新 Worker 时执行：在新正向申请最终批准前，用 Ctrl-C 停 Worker1，确认它退出且 Worker2 ready；批准后观察 Worker2 实际取得 lease/claim token 并投递，再以同一 binary/配置重启 Worker1，确认 ready。不得停止数据库或在 Graph write_pending 窗口随意 kill gateway；不得因此创建额外申请或手工 requeue unknown。

### 7.4 unknown 与 Graph 可见性

Graph POST 成功后的 GET 可能短暂 404。生产实现只在**原成功写的执行上下文**做有限只读验证（默认 10 秒预算、0.5 秒起始、2 秒封顶退避，仅重试 404），不会重发 POST。超时、取消、401/403 或其他异常不能制造 verifiedAt。若进入 `execution_unknown`，后续成员 GET 200 只能证明“当前可见”，不能补造原 verifiedAt 或自动改成 completed。

先用 `GET /api/v1/delegated-executions?eventId=<本次event>` 和本次 KAF delivery/phase 做去敏只读核对；收集原 outbox、lease、action、provider 状态。**POST `/delegated-executions/{eventId}/reconcile` 会写审计结论，并非只读接口。**确需登记时由持 `delegated_execution:reconcile` 的操作员使用 body `{"conclusion":"delivery_unknown_manual_followup","reason":"<本次证据与人工跟进说明>"}`。它不重发授权，也不证明完成。`delivery_unknown` 禁止 requeue/force-resend；不改成 pending/received，不换新 run/step/key 来绕过。正常 not-started 可重试与已可能写入的 unknown 是不同情况。

## 8. 固定测试权限恢复

开始正向测试前就指定一名恢复负责人，保留本轮“非成员基线 → add 成功/可能成功”的归属证据；流程结束或失败均需检查外部状态。不能因为 ITSM unknown 就假定 Graph 未写。

1. 核对本次 action、固定用户 Object ID/UPN、固定组以及 `IT_BACKEND=graph` / `VPN_USERS_GROUP_ID`；不要用用户输入的任意组。
2. **本轮已知 add 成功，即使恢复前 GET 暂时 404，也必须调用原 native `remove_vpn_access(user_identifier=<固定UPN>)` 一次，不传 `target_group_dn`。**该参数非空会走 LDAP，禁止用于本验收。
3. 使用 KAF 已有管理工具调用入口执行上述 remove，或由操作员在显式 `ENV_FILE` 的 KAF Python 环境调用 `src/acp/mcp/tools/vpn.py` 中同一函数；它是外部写操作，不能混在普通只读诊断里。确认该次原 Graph `DELETE /groups/{groupId}/members/{subjectId}/$ref` 的响应（正常 204），记录本轮 remove 次数；工具只显示 `success` 不足以证明实际 DELETE。
4. 在删除后有限次只读 GET 同一成员路径，直至确认 404。若立即 GET 仍 200，保留该记录并等待复制可见性，不重复 DELETE。保存较晚两次非成员检查及时间。
5. 已成功 remove 且确认非成员后，不再 remove。无本轮 add 归属时不得删除他人已有权限。删除失败、响应丢失或迟迟仍成员，停止后续真实测试并交恢复负责人核对；不得以一次 404、工具返回 0 或页面 completed 宣布清理通过。

测试清理不回写或删除历史 WorkItem：原 completed 的 verifiedAt/expiresAt 和成功回执仍保留，原 unknown 也仍 unknown。后续正常使用若需要权限，必须由新的正常申请重新获批，不复用测试清理前的旧回执触发 grant。

为避免用户重新拼装 C4 脚本，可在本次 `$SSLVPN_RUN` 新建 `fixture.json`（0600），从固定 Dev 夹具、真实 Graph 查询和本轮 action 归属填写：`{"upn":"<固定UPN>","subjectId":"<固定Object ID>","groupId":"<固定Group ID>","knownAdd":true}`。以下片段默认**只读**，同时可以用于第 6 节基线核对；只有最后参数为 `remove-known-add` 才调用原 remove。第一次读基线时 `knownAdd=false`，仅在本轮确认 add 归属后改为 true。复制代码为 `$SSLVPN_RUN/check-or-cleanup.py`：

```python
import asyncio, json, sys, time
from pathlib import Path
from urllib.parse import quote
import httpx, structlog
from acp.config import settings
from acp.graph.client import get_graph_client
from acp.graph.users import graph_identity_lookup
from acp.mcp.tools.vpn import remove_vpn_access

# 只记录事件名，防止普通工具日志带出身份/provider原文。
structlog.configure(processors=[lambda _, __, event: {"event": event.get("event")},
                               structlog.processors.JSONRenderer()])
fixture_path = Path(sys.argv[1])
f = json.loads(fixture_path.read_text())
apply = len(sys.argv) == 3 and sys.argv[2] == "remove-known-add"
assert settings.it_backend == "graph" and settings.vpn_users_group_id == f["groupId"]
client = get_graph_client()
member = f'/groups/{quote(f["groupId"], safe="")}/members/{quote(f["subjectId"], safe="")}'
observed = []
async def guard(request):
    if request.method not in {"GET", "HEAD"}:
        assert apply and f["knownAdd"] is True
        assert request.method == "DELETE" and request.url.path == "/v1.0" + member + "/$ref"
async def observe(response):
    observed.append({"method": response.request.method, "status": response.status_code,
                     "at": time.time()})
# 仅给本次原client添加观察/限制，不替换其HTTP实现。
client._http.event_hooks = {"request": [guard], "response": [observe]}
async def read_member():
    try:
        data = await client.get(member)
        assert data["id"] == f["subjectId"]
        return True
    except httpx.HTTPStatusError as exc:
        if exc.response.status_code == 404:
            return False
        raise
async def main():
    try:
        identity = await graph_identity_lookup(client, f["upn"])
        assert identity and identity["_graph_id"] == f["subjectId"]
        before = await read_member()
        print(json.dumps({"beforeMember": before}))
        if not apply:
            return
        assert f["knownAdd"] is True
        # 独占标记阻止不经核对再次删除；失败也保留，不能盲删标记重试。
        fixture_path.with_suffix(".remove-attempt").touch(exist_ok=False)
        result = await remove_vpn_access(user_identifier=f["upn"])
        assert result.get("success")
        assert [x["status"] for x in observed if x["method"] == "DELETE"] == [204]
        nonmember_reads = 0
        for _ in range(15):
            is_member = await read_member()
            nonmember_reads = 0 if is_member else nonmember_reads + 1
            if nonmember_reads == 2:
                print(json.dumps({"restored": True, "transport": observed}))
                return
            await asyncio.sleep(2)
        raise RuntimeError("cleanup_requires_manual_followup")
    finally:
        await client.close()
try:
    asyncio.run(main())
except Exception as exc:
    print(json.dumps({"restored": False, "errorType": type(exc).__name__, "transport": observed}))
    raise SystemExit(1)
```

只读基线 / 恢复后复查：

```bash
ENV_FILE="$SSLVPN_RUN/config/kaf.env" "$KAF_DEPLOY/.venv/bin/python" "$SSLVPN_RUN/check-or-cleanup.py" "$SSLVPN_RUN/fixture.json"
```

**本轮已知 add 的受控移除**（先确认恢复负责人和 fixture 归属）：

```bash
ENV_FILE="$SSLVPN_RUN/config/kaf.env" "$KAF_DEPLOY/.venv/bin/python" "$SSLVPN_RUN/check-or-cleanup.py" "$SSLVPN_RUN/fixture.json" remove-known-add
```

不要在没有本轮 add 的拒绝路径执行移除。失败时保存 transport 摘要及 remove-attempt，先做只读核对，不删除标记来反复运行。POST 已确认但 GET 不可见的 unknown 也必须由恢复负责人核对归属后处理；上述片段不会改 ITSM/KAF 执行状态。

## 9. 故障定位与停止边界

| 现象 | 先核对 | 不要做 |
|---|---|---|
| API/Worker 启动失败 | config 当前目录、数值端口、独立 system 权限、031/checksum、auto flags、秘密文件 0600 | 给 app/ system 超管、关闭 RLS、改 ledger |
| KAF health 200 但不能工作 | schema/checkpoint 启动日志、DB app权限、Redis2、LLM工具调用、embedding维数、Qdrant内容、workspace原settings | 内存 fallback、mock RAG、伪造向量 |
| Azure 登录循环/回调失败 | browser localhost/tunnel、exact redirect URI、tenant/client/secret、真实管理员/普通用户、`/auth/me` | 注入 fixture JWT 冒充正常 SSO |
| 目录没有出现/确认失败 | 当前用户 mapping、provider/channel、workspace UUID、Redis nonce、时钟、权限、catalog/form versions | 浏览器指定 actor/tenant/source、用 automation token 创建 |
| 审批人无待办 | 正确 tenant 与唯一编号、组成员、workflow权限、前一节点已完成、领取状态 | 管理员代批、直接改表 |
| 最终批准后无委派 | 正式 XML policy ref、allowed actions、目录绑定、Outbox状态、两个 Worker与HMAC | 新审批引擎、未知 target 当成功 |
| unknown / 回调401 | automation有效期、原context授权、delivery phase/payload、Graph原始状态摘要 | force-resend、重grant、补造时间 |
| KAF卡与ITSM不同 | 同session/card/workItem、授权的当前详情、原卡刷新和历史hydration | 另建卡绕过、展示创建回执当成功 |

安全停止：先停止接收新申请/将本次目录通过版本条件禁用，结束正在进行的人工测试；按本次保存的 PID/exe/cwd/启动时间核对后 Ctrl-C 或 SIGTERM 两 Worker，再 gateway，再 API；不要删除队列、session、checkpoint 或未知执行证据。若有可能写入的动作，先由恢复负责人判断并处理外部权限。这里只停自己的验收应用，不删除原数据库、Redis/Qdrant、旧 ART 或其他开发服务。

回退应用前先确认新旧代码兼容持久化状态。KAF 037/038 的 downgrade 明确受阻；旧消费者不理解 execution phase / failure payload 时不能直接回退。优先 forward-fix；必要时恢复经验证的升级前备份到独立环境，按原数据和外部效果核对后再接流量。数据库恢复不自动撤销 Graph 权限；不能盲目 down/drop/清数据“回滚”。

## 10. 本轮证据与验收签收

将证据保存在新的 `$SSLVPN_RUN/evidence`，权限 0700/0600；不改写旧 C4 失败和恢复证据。记录每项实际时间、命令退出码、发布 SHA 与环境：

- [ ] 两仓配套 main SHA、clean、构建成功，运行 PID 与该版本对应。
- [ ] 备份恢复抽检、ITSM 正式031 / KAF正式038、受限启动与RLS/checkpoint通过；没有新应用使用 owner。
- [ ] 四类真实身份和独立浏览器、KAF正常AzureSSO、两条映射、it-support成员与tenant唯一绑定已核对。
- [ ] 目录/表单/策略版本、源BPMN与配置后XML hash、实际definition、Procedure hash与真实embedding/RAG已核对。
- [ ] 拒绝申请同编号两端拒绝；无委派，Graph add=0。
- [ ] 新批准申请同编号，两级不同审批人领取/批准，Worker实际投递、单次Graph add、GET确认、ITSM completed及不可变verifiedAt/expiresAt，KAF原卡恢复一致。
- [ ] 原回执重放：实际执行且already_applied/单receipt/时间不变/无第二次add；未做则明确未验证。
- [ ] 可选Worker停止恢复：实际另一进程claim证据；未做则明确未验证。
- [ ] 每次本轮add归属对应原remove的DELETE响应和后续非成员；0额外写入，异常已交负责人。
- [ ] 旧000008未知、旧000009结果、两次历史add/remove及旧synthetic隔离保持；未以新测试覆盖旧事实。

只导出去敏的状态、编号、内部关联 ID、版本/hash、时间、错误类别和计数。不要公开 `.env`、秘密文件、JWT/cookie、Authorization、登录请求、provider原文、完整数据库导出、浏览器网络trace。截图避开个人资料；原始受限材料保留本机，仅输出摘要。KAF的普通Graph错误日志可能包含provider body，不直接打包上传日志，应先去敏。

**部署通过**要求前四项和所有必要服务/身份/内容门禁通过；**本轮完整SSLVPN验收通过**还要求拒绝、正向、原始回执重放和测试恢复闭环全部有本轮证据。未执行的项目标“未验证”，不借历史C4结论填勾。旧 E2E 日期 parser 的非阻断 Minor 仅影响测试证据辅助解析，不是生产授权逻辑；手工核对时间须使用有效日历日期。

## 11. 代码权威入口

ITSM：[启动与schema owner](../../itsm-backend/internal/bootstrap/app.go)、[Worker入口](../../itsm-backend/cmd/kaf_worker/main.go)、[配置loader](../../itsm-backend/config/config.go)、[exchange配置](../../itsm-backend/config/intake_identity.go)、[mapping管理](../../itsm-backend/handlers/intake/identity_mapping_service.go)、[目录DTO](../../itsm-backend/dto/service_dto.go)、[策略校验](../../itsm-backend/handlers/service_catalog/access_policy.go)、[固定流程绑定](../../itsm-backend/service/bpmn_access_policy_publication.go)、[运维reconcile/requeue](../../itsm-backend/handlers/delegated_execution/service.go)。

KAF（相对 KAF checkout）：`src/acp/infrastructure/schema_management.py`、`src/acp/config.py`、`src/acp/routers/auth.py`、`src/acp/itsm_intake/{identity,client}.py`、`src/acp/routers/workspaces.py`、`src/acp/cli/ingest.py`、`src/acp/procedures/rag.py`、`src/acp/orchestration/headless_tasks/kaf_procedure_runner.py`、`src/acp/graph/groups.py`、`src/acp/mcp/tools/vpn.py`、`docs/kaf2/operations/{database-provisioning,delegated-access-verification}.md`。这些入口是部署时核对参数和错误的来源，不是要求用户运行原 ART 脚本。
