# KAF / ITSM 本机 WSL 开发环境

状态：数据库状态补充于 2026-09-14（目录快照 17:57 CST）；其余仓库整理、启动来源和端口说明保留 2026-09-08 历史记录，不代表当前进程存活或版本。本文只适用于维护者本机，操作前仍须实时核对。

## Agent 必读：当前数据库状态（2026-09-14）

完整实例／库名、端口、用途、任务归属、大小及核实状态统一维护在[本地 PostgreSQL 数据库总清单](review/2026-09-14-postgresql-database-register.md)。该清单覆盖本次可见的 WSL Docker 环境；停止容器未启动取证，内部库名未知的项不能当作空库或可删除资源。下表仅标明本轮交接入口，不复制全量清单。

| 角色 | 实例 / 数据库 / schema | Agent 使用边界 |
| --- | --- | --- |
| ITSM 新目标 | `ga-itsm-20260914 / itsm_ga_ready / public` | 任务二配置迁移唯一目标，由任务二 Agent 协调写入 |
| ITSM 保留源 | `itsm-postgres-dev / itsm_config_baseline_20260908 / public` | 已确认配置与 Phase 1 身份数据来源；其他任务只读核对，不清理 |
| KAF 新结构目标 | `ga-kaf-20260914 / kaf_ga / public` | 039 结构验证目标；尚不是业务运行库 |
| KAF 保留基线 | `kaf-dev-postgres / kaf_config_baseline_20260908 / public` | 038 基线保全；历史身份时间戳来源未决，阻塞对应数据升级／搬迁 |

**交接版本与证据：**G-A 固定为 `d91b587fe3ab40cc863321346d217d258a3a96d8`，详见[数据库对账交接](review/2026-09-14-database-reconciliation-handoff.md)。目标 ITSM 源码为 `0788a9bb196ab37a8389b3f366bed9877b2f72c3`，KAF 为 `23f01476b8ea7293c423d608329241477a5336a5`。文档分支 HEAD 不等于应用源码，也不自动改变 GARevision；任务二后续批次与验收由其 GBRevision 记录。其他 worktree 若尚未包含这些文档，应按固定提交读取交接，不能用旧 main 文档推定当前目标。

**准入范围：**G-A 通过的是隔离结构、角色边界和配置迁移准入，不是 G-B/G-C 或应用上线。ITSM 普通迁移对齐至 046，P037 有真实证据，R(038) 未执行。新目标已保全原新 ITSM 的 13 张组织／用户／权限基础表（含 7,862 用户）；这是固定快照，不代表覆盖源侧后续变化，也不是再次迁移旧系统用户。历史 ticket、审批／评论／附件、旧 BPMN／实例和知识库未导入。配置、目录、SLA 与流程绑定由任务二继续验证；PostgreSQL 鉴权 A3/A4 仍按原 R4 跟踪，不能因 046 存在而关闭。

**写入与启动边界：**GA 两个实例使用独立卷、内部网络，没有发布宿主 PG 端口，目前仍是两个实例，任务三尚未合实例。操作前从受保护配置解析连接并核对实例、库、schema、账号、代码版本及负责 Agent；不得套用 5432/5433 或默认 `.env`。`acp-postgres:5433 / control_plane` 与 `kaf-dev-postgres:5434 / control_plane` 是不同库。`itsm_migration_20260914` 是旧 019 对照克隆，不能作为 046 目标。

- 任务二是 `itsm_ga_ready` 的单一协调写入任务；其他 Agent 不并行迁移、初始化、授予权限或跑会写库的测试。
- 运行角色尚未配置完整业务／配置 DML；KAF 当前仅验证 SELECT 结构检查权限。应用验收前须审查所需最小权限并复验上游约束，不能用 owner 账号启动应用。P037 固定的四张核心表 ACL、控制证据和账本不得随意修改。
- 禁止依据通用 quickstart、`-fresh`、bootstrap 或 seed 示例重置这些现有库。迁移 `-status` 的连接解析也须按固定制品核实；不能假定 `DB_DSN` 能选择目标。
- 不迁历史 ticket，不执行 ITSM R(038)，不清历史任务／队列，不改历史 SQL/checksum，不做真实企业写入或源停写。KAF 038 与 ITSM R(038) 是两条不同迁移链。

**备份和保全状态：**本轮已有新隔离目标迁移前备份及恢复验证，以及上述 13 张基础表的只读快照；没有因此完成原 ITSM/KAF 全库备份。原库未清理、删除、覆盖或改名。目前没有任何库被确认可删除；停止、零连接、名称含 test/dev 或旧日期都不构成退役许可。退役须另核依赖、完整可恢复备份、数据范围和切换验收。

**应用状态限制：**17:07 左右 WSL 意外重启，G-A 收尾检查时原候选 API/worker/web/ingress/PG 等停止；本轮只恢复任务自有 GA PG。下面 2026-09-08 的“已运行”和健康结果均是历史记录，不能据此自动重启旧应用或重复启动消费者。原候选恢复和新目标启动分别需要核对当前配置及准入。

凭据／COPY 数据／原始证据留在受保护路径，引用见 G-A 交接，不进入 Git。新增数据库、改变用途或发布新的 GA/GB/GC 交接时，应更新清单及本节日期／版本；历史验收快照不直接覆写为新结论。


## 主机与职责

- `192.168.31.66` 是 Windows 主机，Ubuntu WSL 运行在其中。
- 主路由 `192.168.31.1` 部署 WireGuard。MacBook 通过 WireGuard 和公钥 SSH 访问 WSL（用户 `administrator`，端口 `22222`）；也会在 Mac 本地运行前后端，连接 WSL 数据基础设施。Mac 项目路径尚未确认，不使用文档中的历史个人目录作为默认值。
- KAF 与 ITSM 各有独立入口，共用一个 Azure AD 租户，尚未区分开发/生产租户；Azure 登录仍在调试。
- ITSM 持有审批与业务状态，KAF 受控执行 SR。KAF 生产位于公司内网 `10.128.35.0/24`，本机四个应用入口是开发环境。

## 唯一开发入口与固定运行副本

| 角色 | 路径 | 本次整理后的状态 |
|---|---|---|
| ITSM 开发主仓库 | `/home/administrator/project/itsm` | `main` / `2fa93c1b` |
| KAF 开发主仓库 | `/home/administrator/project/kaf` | 原功能分支 / `a936411b`，没有切换版本 |
| KAF 功能 worktree | `/home/administrator/project/kaf-worktrees/<task>` | 后续开发任务单独创建 |
| ITSM 功能 worktree | `/home/administrator/project/itsm/.worktrees/<task>` | 后续开发任务单独创建 |
| KAF 运行副本 | `/home/administrator/apps/itsm-kaf/kaf` | detached HEAD / `3daa5520` |
| ITSM 运行源码副本 | `/home/administrator/apps/itsm-kaf/itsm` | detached HEAD / `2fa93c1b`；API 使用下面的专用二进制 |

KAF 主仓库的 `a936411b` 与运行副本的 `3daa5520` 提交编号不同，但已提交文件树相同。不要为统一编号擅自切换版本。远端为 `github.com/DawnproIN/kaf` 和 `github.com/Joes9527/itsm`。

KAF 运行副本的 `.git` 文件指向 `/home/administrator/project/kaf/.git/worktrees/kaf`，common dir 为 `/home/administrator/project/kaf/.git`。ITSM 运行副本继续共用 `/home/administrator/project/itsm/.git`。工作目录不是分支；detached HEAD 对固定版本运行副本是预期状态。应用不执行 `.git`，但版本管理依赖该关联，不得删除其 Git 数据目录。

以下目录已退出开发入口使用，原文件保留用于回滚，目录同级有 `.ARCHIVED.md` 标记：

- Windows 原仓库 `D:\SynologyDrive\kerry\KAF_Migration_Pack\kaf-main`；其 WSL 映射 `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main` 是同一份文件。
- 旧 WSL 仓库 `/home/administrator/.worktrees/kaf-sslvpn-unified-intake`。

新主仓库保存完整 WSL Git 元数据，并在 `refs/archive/windows/*` 保留 Windows 的 132 条引用。Windows 已修改/未跟踪文件单独备份，未自动应用到新主仓库。不要从归档入口继续开发，也不要让 CI runner checkout 承载开发或运行 worktree 的 Git 元数据。

## 当前运行入口与启动来源

| 应用 | 端口 | 本机只读探测 |
|---|---|---|
| ITSM 前端 | 3001 | `/login` |
| ITSM API | 8080 | `/api/v1/health` |
| KAF 前端 | 5173 | `/` |
| KAF API | 8000 | `/health` |

```bash
curl --fail --silent --output /dev/null http://127.0.0.1:3001/login
curl --fail --silent --output /dev/null http://127.0.0.1:8080/api/v1/health
curl --fail --silent --output /dev/null http://127.0.0.1:5173/
curl --fail --silent --output /dev/null http://127.0.0.1:8000/health
```

四应用在 WSL 作为本地进程运行，启动描述位于 `/home/administrator/.local/state/itsm-kaf-baseline-20260908/config/`，文件名为 `itsm-launch.json`、`kaf-launch.json`、`itsm-web-launch.json`、`kaf-web-launch.json`。它们保存 argv、cwd 和私有环境；不能把完整 JSON、dotenv、进程环境或 Docker inspect 输出粘贴到文档/日志。

该主机已有四应用启动工具（本机交付文件，不是仓库内通用脚本）：

```bash
launcher=/mnt/c/Users/Administrator/Documents/Codex/2026-09-08/kaf-itms-wsl/outputs/dev-services.py
python3 "$launcher" check
python3 "$launcher" status
# 仅在已授权启动、确认目标进程与配置后执行：
python3 "$launcher" up
```

工具只提供 check/status/up；占用端口会跳过，不接管进程，不负责 stop/restart、构建、数据库或 worker。维护时先核对监听 PID、进程父子关系、cwd 和可执行文件，再停止目标应用；不能使用 `lsof ... | xargs kill` 或全局进程名批量终止。同机还有 CI 与其他服务。

两个 ITSM worker 在维护前已运行，本次按 `itsm-worker-1-launch.json`、`itsm-worker-2-launch.json` 原配置恢复。恢复前检查实际进程，避免重复启动消费者。四入口 HTTP 200 和 worker 存活只证明进程/基础健康，不代表 Azure、SR、SSLVPN 或授权回收验收通过。

## ITSM 修复交付必须保留

8080 使用 `/home/administrator/.local/state/itsm-kaf-baseline-20260908/bin/itsm-api-intake-catalog-discovery-v2`。其 SHA-256 匹配 `evidence/intake-catalog-discovery-v2-verification.json` 中的交付记录。

修复源码在 `/home/administrator/project/itsm/.worktrees/intake-catalog-discovery`，分支 `codex/fix/intake-catalog-discovery`，提交 `7c114b3e`，比本地 `main` 多两个提交。二进制内嵌版本仍标记 `2fa93c1b`，与交付记录不一致，构建来源/打标过程需核对。不能只从 main 重建并覆盖当前 API。目录整理不合并这项修复，也不解决其构建证据差异。

## 数据基础设施与 CI 边界

数据库、Redis 等由 WSL Docker 提供，不能因应用停止而清理 Docker。当前快照：

| 用途 | PostgreSQL | Redis | 其他 |
|---|---|---|---|
| ITSM 开发 | 5432 | 6389 / DB11 | 配置以当前私有启动描述为准 |
| KAF 开发 | 5434 | 6380 / DB10 | Qdrant 6335 |
| KAF CI acp 栈 | 5433 | 根据 Docker labels/配置核对 | CI 独立管理 |

KAF 开发目前有意共用 acp MinIO（9000）；不能据此声称 CI 与开发完全存储隔离。维护者目标是 CI 独立、Mac/WSL 开发共享，后续存储拆分需单独规划，本次没有迁移数据。对共享数据执行初始化、迁移、清理前明确目标 host、port、database、Redis DB、bucket、volume 和授权范围；通用 quickstart/fresh/bootstrap 不适用于直接接管现有实例。

## 维护、备份与回滚

目录整理按保全、核对、新主仓库恢复、运行关联迁移、重启验证、归档顺序完成。保留 Git 历史与引用不等于备份未提交文件；必须分别保存 bundle、完整 Git 元数据、工作区文件、`.superpowers/sdd` 证据、私有启动配置和实际二进制。

本机私有备份在 `/home/administrator/.local/state/kaf-repository-migration-20260908/`（权限 700）。其中 `backup-sha256.json` 和 `backup-verification.json` 记录 10 个 bundle/tar 的哈希核验；`before-*`、`after-*` 与 `launch-fingerprints.json` 保存维护前后基线，`old-runtime-registration/` 保存旧 Git 注册。此备份含敏感配置/用户文件，不能提交或上传仓库。

完整逐步回滚记录位于本机 `/mnt/c/Users/Administrator/Documents/Codex/2026-09-08/kaf-itms-wsl/outputs/repository-migration.md`。回滚前先保全迁移后新增工作并停止应用，恢复旧注册及运行 `.git` 指针、退出新注册，核对 HEAD/common dir/工作区后按原配置启动。不能只执行一次 `worktree repair` 就认定跨仓库关联迁移或回滚完成。

后续正常开发使用各自功能分支/worktree；源码编辑不会自动更新固定运行副本。版本升级、依赖安装、数据库迁移和真实业务验收分别规划，不与目录整理混做。


### 2026-09-14 任务二审查修复增量

- 当前根任务新增 `gb-remediation-test-pg-20260914` 内部测试实例，三个库分别是 `gb_review_test`（SQL 语义测试）、`gb_replay_review`（真实备份五批重放）、`gb_ticket_types_test`（产品类型初始化测试）。**都不是业务应用连接目标**，见数据库总清单。
- ITSM 工具修复 `7c8cee6fae181400573308bfb7e73043d11ed0b9` 与 KAF 工具修复 `e6fd8a50368a15505c98c8826dee5af975cef5c2` 独立复审通过；G-B 仍 BLOCKED，不能据工具测试切换连接。
- 首期范围：单租户功能收口；旧路由后续处理，使用现有分派及规范流程；不迁历史工单。SLA 日历已修复并准备新配置，但七个现有流程绑定没有 SLA ID，实际新建计时接线尚需完成。
- 运行时集成工作树 `config-launch-integration` 尚在开发，未替代 G-A 固定运行时，未启动候选应用。


### 2026-09-15 当前 DEV 与新目标的澄清

最新只读核对：8080 Backend 实际连接 `itsm-postgres-dev / itsm_config_baseline_20260908 / public`，不是 `itsm_ga_ready`。当前DEV已有26条符合核心WorkItem关联结构的记录及12条产品类型；旧库itsm和旧克隆itsm_migration_20260914仍有未完成适配的数据。新目标不是DEV的完整克隆，不应据其0工单反推DEV没有测试数据。

保留候选、046字段差额及目录/分类/SLA ID重映射要求见 [DEV数据保留核对](review/2026-09-15-dev-workitem-data-preservation-audit.md)。当前DEV未切换，未修改数据。


### 2026-09-15 最终迁移范围确认

用户确认不保留当前DEV旧工单到目标、不续跑旧流程：26条WorkItem及关联专业记录、评论、附件、关系，以及旧流程实例/任务均不迁移。原DEV库和数据保持原样；“不迁移”不表示获准删除源数据。

继续复用 `itsm_ga_ready` 承接清洗适配后的所需配置与基础数据，通过可切换配置让现有Backend连接目标；保留原DEV配置以便切回，不新建整套环境。E2E通过新建记录验证流程。为保留旧测试工单而提出的7个目录补齐、旧SLA周期及流程续跑工作不再属于本轮必需项；配置本身是否需要迁移仍按新产品规范审核。
