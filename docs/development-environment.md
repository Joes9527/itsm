# KAF / ITSM maintained WSL development environment

Status: maintained operational contract, updated 2026-09-15. The maintainer selected **3010 as the ITSM frontend port**. Deployment filenames containing `ga`, `candidate`, or `prod` do not establish environment identity or release acceptance. This environment is named **WSL development**.

## Agent 必读：当前数据库状态（2026-09-14）

完整实例／库名、端口、用途、任务归属、大小及核实状态统一维护在[本地 PostgreSQL 数据库总清单](review/2026-09-14-postgresql-database-register.md)。该清单覆盖本次可见的 WSL Docker 环境；停止容器未启动取证，内部库名未知的项不能当作空库或可删除资源。下表仅标明本轮交接入口，不复制全量清单。

| 角色 | 实例 / 数据库 / schema | Agent 使用边界 |
| --- | --- | --- |
| ITSM 新目标 | `ga-itsm-20260914 / itsm_ga_ready / public` | 任务二配置迁移唯一目标，由任务二 Agent 协调写入 |
| ITSM 保留源 | `itsm-postgres-dev / itsm_config_baseline_20260908 / public` | 已确认配置与 Phase 1 身份数据来源；其他任务只读核对，不清理 |
| KAF 新结构目标 | `ga-kaf-20260914 / kaf_ga / public` | 039 结构验证目标；尚不是业务运行库 |
| KAF 保留基线 | `kaf-dev-postgres / kaf_config_baseline_20260908 / public` | 038 基线保全；历史身份时间戳来源未决，阻塞对应数据升级／搬迁 |

### 2026-09-19 更新：本地开发栈只保留两个库

上表记录的是 **G-A 任务环境**（`ga-itsm-20260914` / `ga-kaf-20260914` 等实例在本次核对时已不存在）。当前**维护中的本地开发栈**情况如下：

| 项 | 值 |
| --- | --- |
| 后端实际连接 | `itsm-postgres-dev / itsm_config_baseline_20260908 / public`（依据维护栈 recipe `config/itsm-launch.json`，非 `.env`） |
| 保留库（共 2 个） | `itsm_config_baseline_20260908`（开发库，台账 051）、`itsm_migration_20260914`（演练克隆库，台账 048） |
| 已退休并删除 | `itsm`、`itsm_baseline_20260908`、`itsm_intake_test`、`itsm_p1_integration_verify_20260901`——删除前均已备份至 `/var/backups/itsm/retired_*_20260919.dump` |
| 默认库名已修正 | `scripts/deploy-dev.sh` 与 `.env.dev.example` 原默认 `itsm`（019 旧库，连上会缺列）已改为 `itsm_config_baseline_20260908` |

迁移准入、克隆库定位、`/api/v1/readyz` 与维护栈的启动方式见 [规范化迁移准入与克隆库约束](deployment/canonical-migration-admission-and-clone-constraints.md)。

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


> Before changing Dev, read the [verified Dev031/main047 divergence analysis](review/2026-09-16-dev-schema-divergence-report.md). The maintainer accepted the [restoration design](superpowers/specs/2026-09-16-dev-restoration-two-database-design.md); use the [four-stage execution checklist](superpowers/plans/2026-09-15-migration-validation-ledger.md#two-database-execution) and its evidence gates instead of historical proposals. The selected code/schema compatibility target remains047.

## Selected schema target: 047

**Current development and migration-validation target, confirmed 2026-09-15: `047_bpmn_assignment_source`.** Both Dev and the migration-validation database must support this selected current-code schema. Do not choose an older backend to accommodate a database at 031 or 046. This is the target contract, not a statement that either live database has already been upgraded.

| Decision | Authority for the current task |
| --- | --- |
| Schema target for both database roles | **047_bpmn_assignment_source**, with the complete required canonical dependency chain and actual receipts |
| Existing Dev upgrade path from its verified 031 baseline | Read-only classification and backup/restore verification → controlled P037 → ordinary032–036 and039–047 → matching runtime/system/inspection configuration and acceptance |
| Maintained validation clone | Proposed sequence: restore and accept Dev at047, then create a traceable Dev snapshot clone. The former isolated046 target is a preserved evidence/difference source; this table does not authorize upgrading it. Follow the [two-role convergence plan](superpowers/plans/2026-09-15-migration-validation-ledger.md#dev-clone-alignment) |
| Migration tool prerequisite for the old Dev ledger | Use the merged PR38 atomic ledger preparation fix (`0e1afe997`, merged in `32c39dda3`) or a reviewed descendant |
| R038 retirement | Remains a separate manual stage; **not required to restore Dev** and not implicitly included by saying “upgrade to047” |
| Actual destination and progress | Read the [dated target-status table and single ledger](superpowers/plans/2026-09-15-migration-validation-ledger.md#schema-target-status); inspect the live recipe/database before acting |

047 adds the persisted, immutable `process_tasks.assignee_source` contract for explicitly bound WorkItem-assignee tasks. It does not turn historical tasks into bound tasks, repair process routing configuration, import legacy data, or establish business acceptance. The canonical [migration registry](../itsm-backend/migration/migrations.go) and [047 SQL](../itsm-backend/migrations/047_bpmn_assignment_source.sql) define the structure; the [assignment report](review/2026-09-15-work-item-task-assignment-report.md) defines the associated behavior and evidence.

For coding agents: start from this selected target and the current source registry. Treat later sections describing earlier port/candidate work and older migration numbers as historical or feature-specific evidence. If a later task intentionally advances beyond047, update this target and its linked execution ledger together; do not silently freeze development at047 or silently deploy a newer schema. Matching the maximum receipt number alone does not prove that required migrations, privileges, configuration and UI paths are valid.

## Endpoint ownership

| Service | Windows/LAN host port | Authority |
| --- | --- | --- |
| ITSM frontend | **3010** | Native Next.js standalone process; same-origin `/api/*` forwards to ITSM 8080 |
| ITSM backend | 8080 | Native Go binary pinned by SHA-256 |
| KAF frontend | 5173 | Its own configured frontend release |
| KAF API | 8000 | Its own configured API release |
| Langfuse | **3000** | `acp-langfuse` container; never start ITSM on this host port |
| Former ITSM frontend | **3001 — retired** | Do not use for startup, tests, callbacks, or current documentation |

Windows host: `192.168.31.66`. SSH reaches Ubuntu WSL as `administrator`, port `22222` (the host also has an SSH forwarding alias on `22223`). Browser address from the LAN/Mac: `http://192.168.31.66:3010`; Windows localhost: `http://localhost:3010`. A Mac `localhost` is the Mac, not WSL. A container's internal port 3000 is not the Windows/WSL published port and need not be renamed.

## Development and migration validation database contract

**Accepted by the maintainer on 2026-09-15.** The goal is to make the agreed legacy ITSM data work in the new ITSM while normal development continues. Development and migration validation use one evolving product codebase. Their intended difference is the database's purpose and data, not a permanently older application or data model.

| Dimension | Daily development | Migration validation |
| --- | --- | --- |
| Application | Current selected, reviewed frontend/backend release | The same selected frontend/backend release for comparative acceptance |
| Database purpose | Dev database for ongoing development and development test records | A traceable clone of Dev for cleaned legacy data and acceptance records |
| Schema | Canonical migrations required by the selected code | The same required schema contract; verify migration receipts and structure independently |
| Data/configuration | Development fixtures and configuration | Approved source mappings, transformed data and target business configuration |
| Shared entry | 3010 → 8080 targets Dev for daily work | Temporarily switch the same entry to the validation profile for a scheduled validation window |

### Version and schema discipline

- Record frontend commit/build ID, backend commit/artifact hash, database instance/name/schema, migration receipts and the active profile. A branch name or a maximum migration number alone is insufficient proof of compatibility.
- Develop fixes once in the shared codebase. Propagate the selected release and its required canonical schema changes to both database roles. Different business data and environment configuration are expected; every difference affecting acceptance must be recorded.
- A schema-changing task includes dependency-aware upgrade and verification for both roles. If either database lags, record a blocking gap and complete its upgrade before using the new code there. Do not report results from different code/schema contracts as equivalent acceptance.
- **Do not restore or retain old application code as the solution for switching back to Dev.** Preserve Dev data and service stability by planning a compatible upgrade. “Keep Dev stable” does not mean freezing its schema indefinitely.
- A source merge is not a deployment. Select and verify a concrete frontend/backend release together; do not automatically deploy every new main commit or blindly run all migrations.

### Three separate workstreams

1. **Schema compatibility:** use the existing canonical Migrator, dependency checks and truthful receipts. Separate structural preparation, ordinary migration, business acceptance and controlled retirement. Do not edit historical SQL/checksums, fabricate receipts, use Ent overlays, or enroll historical WorkItems to pass admission. Apply the [controlled retirement contract](../AGENTS.md#accepted-workitem-decisions-and-migration-boundaries).
2. **Legacy data adaptation:** clean and map approved legacy master/configuration data to the new model. Old tickets, comments, attachments, approvals and process instances remain excluded. Existing five-batch reconciliation is completed evidence, not a reason to rerun all imports.
3. **Reusable validation:** reuse Migration Validation Toolkit v1 for its implemented users/departments verification and missing-object supplementation. It is not a schema upgrade tool or a replacement for the five-batch configuration executor. Its source/status and remaining tasks are maintained in the [single ledger](superpowers/plans/2026-09-15-migration-validation-ledger.md#development-restoration-update).

### Switching and acceptance procedure

1. Inspect the live destination and all dependencies read-only; compare the chosen code's requirements with each target's actual schema, privileges and migration evidence. Use explicit database identity, never a label such as GA or Dev alone.
2. Before an approved Dev schema change, verify backup coverage and restoration in an isolated rehearsal target. Produce the exact migration/preparation scope, role grants, downtime expectations, acceptance checks and data-preserving recovery/remediation plan. Retirement and deletion retain their separate authorization boundaries; this document does not authorize their execution.
3. Verify the required canonical changes on the rehearsal target, then apply the reviewed and authorized scope to Dev. Keep automatic migration/seed disabled in normal service startup. Missing execution-domain tables are a real startup blocker even in standard mode; do not bypass admission or grant owner/superuser access.
4. Switch the **complete profile** through the maintained startup authority: runtime/system/inspection identities must target the same database/schema/deployment; also select Redis namespace/database, attachment storage, origins/session settings and execution capabilities. Stop affected consumers, isolate pending work and caches, and verify session handling so no state or work crosses targets. Never mix a Dev runtime connection with validation inspection or system connections.
5. Verify actual process destination, readiness, login and the representative 3010 → 8080 UI path; record the profile and release evidence. Restore daily development to Dev after the validation window. Pending migration acceptance must not become a permanent dependency for continuing development.

One shared 8080 serves one target at a time. This topology does not provide simultaneous access to both databases; coordinate the validation window with development. Any future concurrent topology needs an explicit operational decision and documented port ownership, not ad hoc use of 3000/3001.

Database labels describe roles, not lineage: `itsm_ga_ready` was prepared as an isolated new-model target, not a full Dev clone or a GA release. The maintainer reaffirmed on 2026-09-16 that the maintained validation role must use a verified Dev clone. Existing isolated targets retain their prior evidence and serve as transition sources; they are not interchangeable with that clone. See the [two-database convergence proposal and current observations](superpowers/plans/2026-09-15-migration-validation-ledger.md#dev-clone-alignment). Dated observations and the ordered recovery tasks belong in the [single ledger](superpowers/plans/2026-09-15-migration-validation-ledger.md#development-restoration-update); this contract is not a live deployment report.

## One startup authority

The maintained entrypoint is `/home/administrator/apps/itsm-kaf/stack`, deployed from [`scripts/wsl-stack.py`](../scripts/wsl-stack.py). Its private state remains `/home/administrator/.local/state/itsm-kaf-baseline-20260908`; the historical date is a storage path, not a version identifier.

- `config/<service>-launch.json` is the active startup recipe: argv, cwd, private environment, port, source revision and artifact/build identity where available.
- `evidence/<service>-process.json` binds a managed process to PID, process start time, cwd and the recipe fingerprint.
- `active-release.json` is a sanitized deployment snapshot containing endpoint ownership, actual frontend revision/build ID, backend executable hash and source provenance, and known verification limits. Use current process/config checks to detect drift after this snapshot.
- `logs/<service>.log` contains service output; never publish secrets or whole environment/config files.

```bash
/home/administrator/apps/itsm-kaf/stack status
/home/administrator/apps/itsm-kaf/stack status itsm-web
# Only when startup/restart is part of the authorized task:
/home/administrator/apps/itsm-kaf/stack stop itsm-web
/home/administrator/apps/itsm-kaf/stack start itsm-web
```

Use a named service for bounded maintenance. Status detects recorded/configured version drift and untracked port owners; do not defeat it by deleting process records. Stop validates process identity and signals only the recorded PID. A listening port alone does not prove service health.

Old one-off launchers, historical JSON snapshots and handoff reports are rollback evidence, not additional active deployment authorities. Do not run historical `ga-frontend-switch.py`, `ga-backend-switch.py`, `pin-ga-frontend.py`, or `dev-services.py` to replace the active recipe. Any new deployment must update the canonical recipe, process evidence and sanitized release snapshot together.

## Version selection and build boundaries

Always verify **host → port → listener PID/start time → executable/cwd → artifact hash/build ID → source revision → backend destination**. Never infer identity from a branch name, directory name, file modification time, or port alone.

Historical port/theme integration evidence (not the current release authority): the 2026-09-15 frontend integration started from the running workbench source `93480226` and merged A/C visual theme source `eb76c3bc`. The exact deployed merge/fix revision and Next.js build ID are recorded in `active-release.json`. This preserves current workbench commands while adding the completed theme. Newer `main` also contains unrelated domain/database work; updating it is not authorization to deploy its entire backend.

**Superseded for the live runtime** — the current backend/frontend identity is recorded in the 2026-09-18 increment at the end of this document. The backend source provenance recorded by the deployment before that was `c3c880df`; its binary fingerprint before the port change was `d395dbd5a03739d48cde6fe7898ec45d7daadb19686ac265f5bf6e70239a58ef`. A source label is recorded provenance, not a fresh reproducible-build attestation. That completed frontend-port task kept that binary and database target and changed only the frontend URL/origins necessary for 3010, and recorded the resulting configuration fingerprint. It did not run migrations or grant database permissions.

Source checkout edits do not automatically update a standalone build. Build in an isolated worktree, verify current command/theme behavior, copy required `.next/static` and `public` artifacts into standalone output, verify `/api/*` targets 8080, and record revision/build ID before switching. Avoid concurrent builds or dependency installs against the active runtime directory.

## Verification and honest status

```bash
curl --fail --silent --output /dev/null http://127.0.0.1:3010/login
curl --fail --silent http://127.0.0.1:8080/api/v1/readyz
curl --fail --silent http://127.0.0.1:8000/health
curl --fail --silent --output /dev/null http://127.0.0.1:5173/
```

Also verify representative frontend assets against the deployed filesystem, same-origin API responses, light/dark theme behavior, LAN access, no ITSM listener on 3001, and unchanged Langfuse ownership of 3000. Authenticate through normal sessions when business UI validation is needed. Do not create requests, approvals, provider calls or IAM mutations merely to validate a port change.

On 2026-09-15 before this change, ITSM `/health` returned 200 but `/readyz` returned 503 because its database identity could not read `schema_migrations`; this is a permission/readiness failure, not proof of missing schema. KAF 8000/5173 and ITSM workers were stopped. A frontend deployment does not certify these independent services or fix their readiness. Report their current status separately.

## Shared infrastructure and rollback

Preserve existing PostgreSQL, Redis, MinIO, Qdrant, Langfuse and Ollama containers and volumes. Do not run generic `init`, `reset`, `down -v`, migration/bootstrap tools, or database cleanup against this shared environment.

Before changing a runtime, save private launch recipes, exact PID identities, executable hashes, frontend build ID and relevant configuration. The 3010 task's pre-change backup is `/home/administrator/.local/state/itsm-wsl-3010-20260915T035625Z` (private). Record any later supplemental backups alongside it. Restore only the identified affected service and configuration; verify no concurrent operator replaced its process. Rollback is an explicit operation, not a second normal startup path.

Development repositories remain `/home/administrator/project/itsm` and `/home/administrator/project/kaf`, with task worktrees. Retain linked-worktree Git metadata, uncommitted work, `.superpowers/sdd` ledgers and archived source copies. Do not delete or reorganize another agent's work as part of port/version maintenance.

## Ticket detail experience deployment (2026-09-15)

PR #35 unifies the existing detail-page refresh, preserves editing context, and separates current tasks from collapsed task history. The maintainer authorized merging this PR and applying its frontend to local WSL development on 3010. Build from the branch after integrating current main; rerun affected frontend tests before switching the canonical `itsm-web` recipe.

This release changes the frontend only. Preserve the existing 8080 executable, backend configuration, databases, workers and KAF services. Back up the old frontend recipe and `active-release.json`, retain the previous standalone directory, and record the new source revision/build ID in the canonical recipe and sanitized snapshot. Validate actual 3010 login/detail behavior with the repository's guarded Playwright tests. Failed verification requires restoring the saved frontend recipe and starting the previous release through `stack`.

The exact applied revision, build ID and verification outcome belong to the local `active-release.json`; a merged PR alone is not proof that the running frontend was switched.
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


### 2026-09-15 09:50 CST 切换前准备增量（尚未切换）

本节更新之前的0类型/未配置运行权限状态；原G-A快照仍保留历史意义。唯一协调写入者为当前根任务。

- `itsm_ga_ready` 已定向初始化12个产品默认工单类型，复跑0新增；工单仍0。13张基础身份表、迁移账本与四核心表ACL保持不变。原DEV26条记录仍留在原库。
- 目标最小运行权限和 `ga_runtime / itsm-ga-ready-20260914 / standard` 绑定已通过隔离恢复库演练、目标事务回滚预演后提交；ga_system无新增权限，四核心表ACL、P037控制文件/证据及全部原表数据摘要未变。真实运行/inspection账号执行启动前角色、执行模式及迁移准入检查通过；这不是应用或E2E验收。
- Redis沿用原实例6389，目标DB12已PING验证为空，DEV DB11未清理；独立目标附件桶 `itsm-ga-e2e-20260915` 已建立且为空，未动DEV附件。PostgreSQL新鉴权存储仍未接入bootstrap，不能当作通过；当前代码仍使用Redis刷新/撤销。
- 新增隔离测试库 `gb-remediation-test-pg-20260914 / ga_acl_rehearsal_20260915`，仅用于恢复本批备份与ACL真实角色演练，不是业务环境，不得让应用连接。原有三个测试库未覆盖。
- 本批受保护配置、备份、恢复/准入证据均在 `/home/administrator/.local/state/itsm-backend-switch-20260915/`。不得把凭据、原始用户数据、argv/environment或备份提交Git。
- 前后端兼容补丁仍在独立审查；8080/3001尚未切换，启动与实际登录/新建/人工任务操作仍待验证。SLA、未测专业动作、外部投递、重启恢复均不能因这些预检关闭。

### 2026-09-18 审批可见性修复：DEV 运行时身份更正与共享库变更

本节取代上面的运行时身份记录；核对当前值请以本节为准。

| 服务 | PID | 身份 |
| --- | --- | --- |
| itsm (8080) | 1508265 | `itsm-api-fixes-d310caba`，sha256 `285dacfe04937898ab4ea8e9ae6cf4a67d8430cc6a73e7ba0e370a800fc711ab` |
| itsm-web (3010) | 1507567 | `itsm-web-d310caba-9BhCMagW1eM6lr4CPURkS`，build_id `9BhCMagW1eM6lr4CPURkS` |

两者均由 `stack` 管理，recipe 与 `active-release.json` 已同步更新。前端在独立 worktree 构建，只含本次变更的文件，未纳入并发会话对其它文件（如 `ServiceItemCard.tsx`）的在途改动。

**配置更正**：后端 recipe 一度按 `dev-launch-proposed.json` 记录 `ENV=production`，而进程实际捕获的环境是 `ENV=development`。现按实际运行值记录。以在运行进程的环境为准，不要据提案文件回填。

本次改动共享开发库 `itsm_config_baseline_20260908`（tenant 1），改动前已备份（`sha256 20586838e9a98373ccf582b9042201cf49cbfaeeb8ddea073fe8359c545df42f`）。两处变更：

- `teams.id=1` 的负责人由空置改为 1203。理由：`ticket_general_flow` 的派单节点声明 `assigneeTeamId=1`，团队无负责人时团队负责人解析失败、任务无人可领。
- `ticket_general_flow` 由 1.3.0（id 65）发布为 **1.4.0（id 152）**，旧版本停用，该 key 恰好 1 个 `is_active` 版本。回滚：`id=65` 置 `is_active/is_latest=true`、`id=152` 置 false。

**未执行**：2026-09-18 批次的 E2E 工单（54–72）未删除。该批 19 条 `intake_requests` 全部为 `completed`，`validate_intake_receipt_provenance` 触发器使已完成收据不可变，而外键动作为 `SET NULL`——执行该 UPDATE 本身即报错。没有禁用该保护，批次保留，改用新建工单验证流程行为。

完整变更、验证与回滚证据保存在私有目录 `/home/administrator/.local/state/itsm-dev-fixes-20260918/evidence/DEPLOYMENT-RECORD.md`，该文件不入库。未修复项（可达但未声明路由的履行节点）登记为 [BL-BPMN-UNROUTED-TASK-FALLBACK](../ROADMAP.md#bl-bpmn-unrouted-task-fallback--stop-silently-assigning-unrouted-user-tasks-to-the-requester)。
