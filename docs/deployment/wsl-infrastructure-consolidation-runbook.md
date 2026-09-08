# WSL 开发基础设施整合操作记录

状态：in progress；2026-09-08 已完成在线备份和隔离 Qdrant 恢复核验；PG16 镜像构建完成，12 个数据库的恢复及数据比对通过。尚未停机、切换应用或创建最终共享栈。

依据：[已批准设计](../superpowers/specs/2026-09-08-wsl-infrastructure-consolidation-design.md)、[实施计划](../superpowers/plans/2026-09-08-wsl-infrastructure-consolidation.md)。这是实际盘点证据，不是可直接执行的完整切换清单；目标配置和迁移命令须在版本、对象归属与恢复方法完成评估后固化。

## 连接与资源

维护者通过 WSL 本机控制台核对 SSH 公钥指纹后，本机已备份 known_hosts 并仅替换该主机条目。后续严格主机校验通过；远端主机 JULIAN，用户 administrator。

只读资源快照：WSL 根文件系统约 706 GiB 可用；内存约 31 GiB，总体可用约 19 GiB。仅说明初步可用资源，尚未形成完整备份与恢复空间预算。

## 已核实源端点

| 用途 | 当前容器 | 版本 | WSL 宿主机端口 | 当前运行配置 |
| --- | --- | --- | --- | --- |
| ITSM PostgreSQL | itsm-postgres-dev | 17.10，pgvector 镜像 | 5432 | itsm_config_baseline_20260908 |
| KAF PostgreSQL | kaf-dev-postgres | 16.14 | 5434 | kaf_config_baseline_20260908 |
| ITSM Redis | itsm-redis-dev | 7.4.10 | 6389 | DB11 |
| KAF Redis | kaf-dev-redis | 7.2.14 | 6380 | DB10 |
| ITSM MinIO | itsm-minio-dev | RELEASE.2025-09-07T16-13-09Z | 9012 / 9013 | itsm-uploads |
| KAF 使用的 MinIO | acp-minio | RELEASE.2024-04-18T19-09-19Z | 9000 / 9001 | kaf-kb-files |
| KAF Qdrant | kaf-dev-qdrant | 镜像标签 v1.9.2；运行版本待核验 | 6335 / 6336 | 保留集合；不主动重建 |

ITSM 运行使用私有启动目录下 config.yaml；KAF 使用 launcher 指定的 kaf.env。ITSM 当前仍运行专用 `itsm-api-intake-catalog-discovery-v2` 二进制，两个 itsm-worker 均从同一私有配置目录启动。ITSM 实际运行环境为 `RLS_MODE=enforce`，迁移时必须保留，不能沿用旧文档 off 默认值。

这些配置来源已核对；连接池实际连接与后台消费者覆盖仍需后续数据库会话/进程核验。Mac 本地配置已核对：ITSM 指向 5432/itsm、Redis 6389/DB0；KAF 指向 5434/control_plane、Redis 6380/DB1。Mac 与 WSL 当前使用不同逻辑业务库，不能在整合时静默合并或覆盖。实际运行连接仍需核验。

## 数据范围发现

ITSM 开发实例同时存在 itsm、测试库、baseline 与 config_baseline 库；KAF 开发实例同时存在 control_plane、langfuse、baseline 与 config_baseline 库。不能将名称最像主库的数据库直接作为唯一迁移源。

“全部保留”适用于全部现有数据。每个非系统数据库需要标注实际消费者和处理方式；不活跃历史库也应保全，不在整合时顺带删除。是否迁到新实例或留在保留源中，应在最终资源清单中明确。

WSL 还有多个其他任务的 PostgreSQL / Redis / Qdrant 容器。当前不将这些临时资源列为本次可修改对象，也不停止其他任务消费者。KAF 开发容器的 Compose 标签仍引用已归档 Windows 源目录；这些标签只能说明创建来源，不能直接当作当前维护入口。

## 进入下一阶段前的门禁

- 维护者已指定统一 PostgreSQL 目标为 PROD 基线 16.14 x86_64 Alpine/musl。ITSM PG17→PG16 必须采用经过验证的逻辑迁移；不复制源卷、不原地降级，不将低版本 pg_dump 用于 PG17 源。
- Redis 7.2 / 7.4、MinIO 2024 / 2025 需要确定统一版本并验证恢复、持久化和 API 行为；不采用浮动 latest 或未经验证的降级。
- 核对每个库、bucket、Qdrant 集合与 Mac/WSL/CI 的实际调用关系。
- 核对 Redis 键类型、TTL、队列/消费组、持久化和淘汰策略；核对 MinIO 历史版本、本地附件与跨系统引用。
- 目标端口、容量预算、恢复工具及精确命令在这些门禁通过后记录，不从当前源端口猜测目标可用性。

## 在线备份与恢复演练证据

备份位于 WSL 私有状态目录 `~/.local/state/kaf-itsm-dev-consolidation-20260908/`，目录 0700，数据/凭据文件 0600。仓库不收录备份、角色密码、对象内容或连接秘密。

- PostgreSQL：两个源实例共 12 个数据库已分别使用匹配源版本的 pg_dump 导出 custom archive，记录大小和 SHA256；同时私有保留 globals 与角色元数据。包括两个实例的 postgres 库，恢复时使用不同目标名称避免冲突。这是在线、单库一致备份，不能替代停止写入后的最终跨服务备份。
- Redis：两个源实例按键保留 DUMP 与绝对过期时间；导出时 KAF 1 键、ITSM 35 键。ITSM DB0 有 2 个持久 Stream 和 2 条 pending 消息；不能只迁移 WSL DB11。两源均 noeviction、maxmemory=0、AOF 关闭，RDB 周期不同。恢复不得延长 TTL 或复活已过期状态，尚未完成恢复验证。
- MinIO：itsm-uploads 的 3 个对象（共 176 字节）已下载并记录内容哈希及元数据；KAF bucket 和 audit bucket 在盘点时为空，版本管理未开启。尚未验证目标恢复、应用引用或 ITSM 本地附件回退目录。
- Qdrant：四个集合已创建、下载并校验快照文件 SHA256。在独立容器 `kaf-itsm-qdrant-rehearsal-20260908` 使用源镜像恢复；仅绑定 WSL loopback 15433。knowledge_base 36、procedure_library 35、response_cache 0、conversation_context 36 个点，数量和向量配置一致。尚未认证 payload/vector 内容哈希或业务检索。源集合及快照保留。
- PG16：采用本地已核验的 PostgreSQL 16.14 Alpine 镜像摘要作为基础，编译 pgvector 0.8.6（与 ITSM 源扩展一致）。镜像已构建成功；通过 Mac 从 Alpine 官方站点下载相同软件包，目标 apk 安装时验证签名，解决 WSL 下载缓慢。隔离目标实际版本字符串与维护者提供的 PROD 一致。12 个数据库恢复完成：2,892 张表、277,948 条记录的哈希以及 2,706 个序列值全部一致；恢复目标中 large object 数量为 0。

上述清单分别为 postgres-backup-manifest.json、redis-minio-backup-manifest.json、qdrant-backup-manifest.json 和 qdrant-rehearsal-results.json。所有应用继续使用原配置；演练容器不接收应用请求。WSL 启动配置与现用两个专用二进制已打包、核对 SHA256 并验证归档可读（约 126 MB）；Mac 两仓环境文件和 Git HEAD 已在 Mac 私有状态目录保全，未改写来源文件。

## 尚未完成

维护者已确认 Redis/MinIO PROD 版本暂时未知，目标版本保持待定，列为切换前待办。Mac/WSL 数据集最终映射、完整消费者和本地附件清单、恢复权限/业务验证、最终停写窗口、目标共享栈、切换验收及两仓正式环境文档和架构图同步均未完成。

## PostgreSQL 演练方法与边界

目标容器 `kaf-itsm-pg16-rehearsal-20260908` 使用独立卷、network=none、无宿主机端口；不会接入原应用。原归档先验证 SHA256，再通过对应源版本的 pg_restore 解码。针对 PG17 归档仅移除 PG16 不支持的 `SET transaction_timeout = 0;` 会话设置，保留原 SQL 和调整版供审计；表定义、数据、策略和索引不得静默丢弃。

恢复使用 ON_ERROR_STOP，逐个 COPY 数据块比较记录数量和排序后内容 SHA256，逐序列比较 last_value/is_called。数据库所有者和 locale 名称来自源端元数据；源角色在隔离目标中仅创建 NOLOGIN 占位以保留所有权/ACL 名称，未启用来源密码、角色属性或成员关系。因此本轮通过也只代表备份数据可恢复，尚不能认证应用账号权限、RLS 行为、glibc/musl 排序差异、业务和外部副作用正确性。

阶段验证：Mac 当前 ITSM 的 itsm 库包含 1,165 张导出表（含现有其他 schema），共 21,432 条记录；全部表数据哈希与 1,147 个序列值比对通过。不因为部分 schema 看起来属于测试用途而排除数据。

## 本轮结果（2026-09-08 17:24 CST）

PostgreSQL 12/12 数据库通过，状态为 `data-restore-passed`；完整逐表/逐序列证据在私有 postgres-rehearsal-results.json，汇总在 postgres-rehearsal-summary.json。最终演练镜像 ID 为 `sha256:08f4dbbc688e6e3ca0948ff3ea7a1fefc6cec01052c474bb9205106a0f0ba27b`。演练实例、独立卷及备份保留，原实例和运行配置未切换。

本轮仅完成备份及部分恢复门禁。Redis/MinIO 恢复、数据库登录角色与 RLS、glibc/musl 排序语义、完整引用与消费者核验、CI 拆分、维护窗口、应用验收和最终两仓文档/架构图同步仍未完成；不得据此宣称整合完成。
