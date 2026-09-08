# WSL 开发基础设施整合操作记录

状态：draft；2026-09-08 只读盘点进行中。尚未创建目标栈、导出备份、迁移、停机或切换。

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

这些配置来源已核对；连接池实际连接与后台消费者覆盖仍需后续数据库会话/进程核验。Mac 应用配置尚未核对。

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

现阶段没有运行任何数据导出、恢复、角色变更或资源创建命令。
