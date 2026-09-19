# 本地 PostgreSQL 数据库总清单

- 状态：已盘点快照；2026-09-14 17:57 CST。用途归属基于固定 G-A 交接和已知历史记录；未知项明确保留。
- 范围：当前 WSL Ubuntu Docker 引擎可见的 PostgreSQL 容器及运行实例中的全部数据库，包括系统库、其他服务库和停止容器。未覆盖远端／生产、其他 Docker context、Windows 原生 PG 或已无容器关联的数据卷；WSL 未安装 pg_lsclusters，未据此断言不存在原生实例。
- 17:57 基础快照共发现 17 个 PG 镜像／配置相关容器：6 个运行，11 个停止（其中两个为 init 辅助容器，不能算作已确认数据库实例）。运行实例中有 20 个非系统逻辑库；停止候选实例另有 2 个历史已知业务／验证库；其余 10 个停止容器内部库名未核实。
- 原 17:57 盘点操作仅 Docker 元数据及强制只读目录查询。后续任务二审查修复新增独立测试实例，见下表标注“22:48 增量”；原源与 G-A 目标未因此清理或切换。
- 连接数是瞬时观察，0 不代表无人使用或可以删除；运行的是数据库实例，不等于业务应用已通过验收。大小不含完整 WAL／卷开销，也不代表备份大小。

> **2026-09-19 增量（重要，优先于本快照正文）：本表已过期，已退休并删除 4 个库。**
>
> | 库（本快照中曾列出） | 处置 |
> | --- | --- |
> | `itsm`（019，原开发库） | **已删除**；删除前备份 `retired_itsm_20260919.dump` |
> | `itsm_baseline_20260908`（019，历史基线） | **已删除**；删除前备份 `retired_itsm_baseline_20260908_20260919.dump` |
> | `itsm_intake_test`（无迁移台账） | **已删除**；删除前备份 `retired_itsm_intake_test_20260919.dump` |
> | `itsm_p1_integration_verify_20260901`（022） | **已删除**；删除前备份 `retired_itsm_p1_integration_verify_20260901_20260919.dump` |
>
> 备份位于 `/var/backups/itsm/`（custom 格式，含 `.sha256` 边车，已用 `pg_restore -l` 校验可读）。
> 删除前已核对：前三个库**只被文档引用**、无脚本或配置依赖；`itsm` 曾被 `scripts/deploy-dev.sh` 与 `.env*.example` 的默认库名引用（该默认值已改为当前开发库）。删除时四个库均无活动连接。
>
> **当前 ITSM 只剩两个库**：`itsm_config_baseline_20260908`（开发库，台账 051）与 `itsm_migration_20260914`（演练克隆库，台账 048）。
> 用途、部署版本、启动方式与准入约束的权威说明见 [开发环境状态](../development-environment.md) 与 [规范化迁移准入与克隆库约束](../deployment/canonical-migration-admission-and-clone-constraints.md)；**本快照不再更新**。

## 先认准这四个入口

| 角色 | 实例 / 数据库 | 当前用途 |
| --- | --- | --- |
| ITSM 新目标 | `ga-itsm-20260914 / itsm_ga_ready` | 任务二唯一受控配置迁移目标 |
| ITSM 保留源 | `itsm-postgres-dev / itsm_config_baseline_20260908` | 已确认基础数据／配置源，不清理 |
| KAF 新结构目标 | `ga-kaf-20260914 / kaf_ga` | 039 对账通过；不是现行业务运行库 |
| KAF 保留基线 | `kaf-dev-postgres / kaf_config_baseline_20260908` | 038 源基线；后续数据搬迁需解开时间戳差额 |

同名库必须带实例名。例如 `acp-postgres:5433 / control_plane` 与 `kaf-dev-postgres:5434 / control_plane` 是不同数据库。`itsm_migration_20260914` 是旧结构对照克隆，不是任务二目标。

## 全量清单

| 实例／容器 | 宿主端口 | 数据库 | 分类 | 用途 | 任务归属（非登录账号） | 数据库 Owner | 实例状态 | 大小 | 当前连接数 | 依据 | 处置 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ga-kaf-20260914` | 未发布 | `kaf_ga` | 新目标 | KAF 039 结构验证目标，尚非业务运行库 | 任务一交接／任务三接收 | `ga_kaf_owner` | 运行 | 10191 kB | 0 | 实时只读 | 保留；不启动业务应用 |
| `ga-kaf-20260914` | 未发布 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ga_kaf_owner` | 运行 | 7519 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `ga-kaf-20260914` | 未发布 | `template0` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ga_kaf_owner` | 运行 | 7361 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `ga-kaf-20260914` | 未发布 | `template1` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ga_kaf_owner` | 运行 | 7583 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `ga-itsm-20260914` | 未发布 | `itsm_ga` | 演练 | 首次空库初始化／迁移演练，非任务二目标 | 任务一 | `ga_owner` | 运行 | 18 MB | 0 | 实时只读 | 保留证据；不改 P037 核心权限 |
| `ga-itsm-20260914` | 未发布 | `itsm_ga_ready` | 新目标 | 任务二唯一 ITSM 配置迁移目标；G-A 已通过 | 任务二 Agent | `ga_owner` | 运行 | 24 MB | 0 | 实时只读 | 保留；仅任务二协调写入 |
| `ga-itsm-20260914` | 未发布 | `itsm_ga_ready_restore_verify` | 恢复验证 | 正式 G-A 目标迁移前备份恢复验证 | 任务一 | `ga_owner` | 运行 | 18 MB | 0 | 实时只读 | 保留至下游验收 |
| `ga-itsm-20260914` | 未发布 | `itsm_ga_restore_verify` | 恢复验证 | 首次迁移前备份恢复验证 | 任务一 | `ga_owner` | 运行 | 18 MB | 0 | 实时只读 | 保留至下游验收 |
| `ga-itsm-20260914` | 未发布 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ga_owner` | 运行 | 7478 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `ga-itsm-20260914` | 未发布 | `template0` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ga_owner` | 运行 | 7321 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `ga-itsm-20260914` | 未发布 | `template1` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ga_owner` | 运行 | 7393 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `itsm-candidate-20260914-pg` | 停止／无当前映射 | `itsm_candidate` | 原候选 | 原候选 046；实例已停止 | 原候选交付任务 | `candidate_owner` | 停止 | 未实时核实 | — | 当日重启前记录 | 保留候选证据；非任务二目标 |
| `itsm-candidate-20260914-pg` | 停止／无当前映射 | `itsm_candidate_test` | 历史恢复验证 | 原候选备份验证副本，031 | 原候选交付任务 | `candidate_owner` | 停止 | 未实时核实 | — | 当日重启前记录 | 保留；非 046 目标 |
| `itsm-candidate-20260914-pg` | 停止／无当前映射 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `candidate_owner` | 停止 | 未实时核实 | — | 当日重启前记录 | 保留；不是业务迁移目标 |
| `itsm-agent-a-pg-20260914-0918` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `codex-handoff-live-pg-20260909` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `codex-workitem-convergence-pg-20260909` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `codex-handoff-pg-20260909` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `kaf-itsm-pg16-rehearsal-20260908` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `codex-intake-fields-pg-20260908` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `codex-sslvpn-runtime-pg17-20260905` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `codex-sslvpn-intake-pg-20260905` | 停止／无当前映射 | `内部库名未核实` | 停止实例待核 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `kaf-dev-postgres-init` | 停止／无当前映射 | `内部库名未核实` | 初始化辅助容器 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `app-plandex-postgres-1` | 5435 | `plandex` | 其他服务 | Plandex 独立服务数据库 | Plandex 维护者 | `plandex` | 运行 | 8697 kB | 0 | 实时只读 | 不纳入本次 ITSM/KAF 迁移 |
| `app-plandex-postgres-1` | 5435 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `plandex` | 运行 | 7519 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `app-plandex-postgres-1` | 5435 | `template0` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `plandex` | 运行 | 7361 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `app-plandex-postgres-1` | 5435 | `template1` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `plandex` | 运行 | 7425 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `acp-postgres-init` | 停止／无当前映射 | `内部库名未核实` | 初始化辅助容器 | 未启动核查；不能按容器名推定内部库或可删除性 | 原创建者待确认 | `未核实` | 停止 | 未实时核实 | — | 未核实 | 保留；查清依赖和备份后再议 |
| `acp-postgres` | 5433 | `ai01` | 默认库 | 角色同名默认库；业务用途待确认 | 所属实例维护者 | `ai01` | 运行 | 7519 kB | 0 | 实时只读 | 保留；非新目标 |
| `acp-postgres` | 5433 | `control_plane` | 其他环境 | ACP 控制面；不是 kaf-dev 的同名库 | ACP 维护者待确认 | `ai01` | 运行 | 10 MB | 0 | 实时只读 | 不纳入本次 ITSM/KAF 迁移 |
| `acp-postgres` | 5433 | `langfuse` | 其他服务 | Langfuse；本次观察到连接 | ACP／Langfuse 维护者 | `ai01` | 运行 | 25 MB | 1 | 实时只读 | 保留；不可按旧库清理 |
| `acp-postgres` | 5433 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ai01` | 运行 | 7519 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `acp-postgres` | 5433 | `template0` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ai01` | 运行 | 7361 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `acp-postgres` | 5433 | `template1` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ai01` | 运行 | 7425 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `itsm-postgres-dev` | 5432 | `itsm` | 原开发库 | 旧开发账本／数据，019；原地升级未准入 | 原环境维护者 | `itsm_user` | 运行 | 83 MB | 0 | 实时只读 | 保留；用途依赖另核 |
| `itsm-postgres-dev` | 5432 | `itsm_baseline_20260908` | 历史基线 | 旧 ITSM 基线，019 | 原环境维护者 | `itsm_base_owner_20260908` | 运行 | 65 MB | 0 | 实时只读 | 保留；不是新迁移目标 |
| `itsm-postgres-dev` | 5432 | `itsm_config_baseline_20260908` | 保留源 | 用户确认的 ITSM 配置／Phase 1 身份基础数据源 | 原环境维护者；任务二只读消费 | `itsm_base_owner_20260908` | 运行 | 27 MB | 2 | 实时只读 | 保全；禁止当临时库清理 |
| `itsm-postgres-dev` | 5432 | `itsm_intake_test` | 历史测试 | Intake 测试；有表无迁移账本 | 原创建者待确认 | `itsm_user` | 运行 | 15 MB | 0 | 实时只读 | 保留；退役资格待核 |
| `itsm-postgres-dev` | 5432 | `itsm_migration_20260914` | 对照克隆 | 旧 dev 克隆，019；不是 046 目标 | 此前迁移 Agent／任务二只读参考 | `itsm_user` | 运行 | 83 MB | 0 | 实时只读 | 保留对照；禁止误作目标 |
| `itsm-postgres-dev` | 5432 | `itsm_p1_integration_verify_20260901` | 历史验证 | Phase 1 集成验证，022；存在账本差额 | 原创建者待确认 | `itsm_user` | 运行 | 19 MB | 0 | 实时只读 | 保留；退役资格待核 |
| `itsm-postgres-dev` | 5432 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `itsm_user` | 运行 | 8222 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `itsm-postgres-dev` | 5432 | `template0` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `itsm_user` | 运行 | 7321 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `itsm-postgres-dev` | 5432 | `template1` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `itsm_user` | 运行 | 7393 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `kaf-dev-postgres` | 5434 | `ai01` | 默认库 | 角色同名默认库；业务用途待确认 | 所属实例维护者 | `ai01` | 运行 | 7519 kB | 0 | 实时只读 | 保留；非新目标 |
| `kaf-dev-postgres` | 5434 | `control_plane` | 原开发库 | KAF 旧控制面，036 | KAF 原环境维护者 | `ai01` | 运行 | 65 MB | 0 | 实时只读 | 保留；非 GA 目标 |
| `kaf-dev-postgres` | 5434 | `kaf_baseline_20260908` | 历史基线 | KAF 旧基线，036 | KAF 原环境维护者 | `kaf_base_owner_20260908` | 运行 | 58 MB | 0 | 实时只读 | 保留；非 GA 目标 |
| `kaf-dev-postgres` | 5434 | `kaf_config_baseline_20260908` | 保留源 | KAF 配置基线，038；历史身份时间戳来源待确认 | KAF 原环境维护者／任务三 | `kaf_base_owner_20260908` | 运行 | 14 MB | 0 | 实时只读 | 保全；数据搬迁仍需消除差额 |
| `kaf-dev-postgres` | 5434 | `langfuse` | 服务库待核 | KAF 实例内 Langfuse 库；与 ACP 同名库不同 | KAF／Langfuse 维护者 | `ai01` | 运行 | 7361 kB | 0 | 实时只读 | 保留；运行依赖待核 |
| `kaf-dev-postgres` | 5434 | `postgres` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ai01` | 运行 | 7519 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `kaf-dev-postgres` | 5434 | `template0` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ai01` | 运行 | 7361 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |
| `kaf-dev-postgres` | 5434 | `template1` | 系统库 | PostgreSQL 管理／模板库 | 所属实例维护者 | `ai01` | 运行 | 7425 kB | 0 | 实时只读 | 保留；不是业务迁移目标 |

| `gb-remediation-test-pg-20260914` | 内部网络／无主机端口 | `gb_review_test` | 审查测试，22:48 增量 | R1–R4 PostgreSQL 语义测试 | 当前任务 root | `gb_test_owner` | 运行 | 未纳入原大小快照 | — | 22:48 只读确认 | 保留证据；不是业务连接目标 |
| `gb-remediation-test-pg-20260914` | 内部网络／无主机端口 | `gb_replay_review` | 审查测试，22:48 增量 | 原 pre-B0 备份恢复的五批重放夹具；no-owner/no-acl，非 G-A 准入库 | 当前任务 root | `gb_test_owner` | 运行 | 未纳入原大小快照 | — | 22:48 只读确认 | 保留证据；不是业务连接目标 |
| `gb-remediation-test-pg-20260914` | 内部网络／无主机端口 | `gb_ticket_types_test` | 审查测试，22:48 增量 | 产品默认类型定向初始化测试 | 当前任务 root | `gb_test_owner` | 运行 | 未纳入原大小快照 | — | 22:48 只读确认 | 保留证据；不是业务连接目标 |
| `gb-remediation-test-pg-20260914` | 内部网络／无主机端口 | `postgres` | 审查测试，22:48 增量 | 隔离测试实例管理库 | 当前任务 root | `gb_test_owner` | 运行 | 未纳入原大小快照 | — | 22:48 只读确认 | 保留证据；不是业务连接目标 |
| `gb-remediation-test-pg-20260914` | 内部网络／无主机端口 | `template0` | 审查测试，22:48 增量 | 隔离测试实例系统模板 | 当前任务 root | `gb_test_owner` | 运行 | 未纳入原大小快照 | — | 22:48 只读确认 | 保留证据；不是业务连接目标 |
| `gb-remediation-test-pg-20260914` | 内部网络／无主机端口 | `template1` | 审查测试，22:48 增量 | 隔离测试实例系统模板 | 当前任务 root | `gb_test_owner` | 运行 | 未纳入原大小快照 | — | 22:48 只读确认 | 保留证据；不是业务连接目标 |

22:48 增量：本轮仅额外登记上述 1 个实例、3 个测试库和 3 个系统库；没有重新盘点其它实例。原快照与其摘要保持历史事实。新增实例使用内部网络、独立 volume，不挂载源目录。

## 维护规则

1. 当前没有任何库被确认可删除；停止、无连接、名称含 test 或日期，都不是清理许可。
2. 新建或改变目标用途时，更新本表并记录任务归属；不要通过直接重命名现有库来解决认知混淆。
3. 原库退役须另行核实应用／后台写入依赖、完整可恢复备份、保留范围及切换验收。本清单不等同于备份完成证明。
4. 下游目标遵循 G-A `d91b587fe3ab40cc863321346d217d258a3a96d8`；任务二数据批次的变化归其 GBRevision 跟踪，本次目录快照没有重新执行或替代 G-A/G-B。

## 可复查证据

- 只读原始目录快照：`/home/administrator/.local/state/itsm-database-register-20260914/inventory.json`。
- 可筛选 CSV：`/home/administrator/.local/state/itsm-database-register-20260914/database-register.csv`。
- 已有迁移、角色、备份范围：[G-A 交接](2026-09-14-database-reconciliation-handoff.md)。
- 本次 inventory.json SHA256：`7b14465c4b12a6a0c8e3f39907526bea662ec7b6968329148826ed8d08865c4f`。


### 2026-09-15 09:50 CST 增量

| 实例 | 逻辑库 | 用途/边界 | 状态 |
| --- | --- | --- | --- |
| gb-remediation-test-pg-20260914 | ga_acl_rehearsal_20260915 | 本批itsm_ga_ready备份恢复与最小运行ACL/真实账号准入演练；仅测试，不是应用目标 | 已恢复、演练通过，原三个测试库保留 |

业务目标 `ga-itsm-20260914 / itsm_ga_ready` 当前12个产品类型、0工单；必要运行权限与standard角色绑定已准入，尚未应用切换或E2E验收。原DEV数据未清理。状态与操作证据见开发环境文档2026-09-15增量及受保护目录 `itsm-backend-switch-20260915`。
