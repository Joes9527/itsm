# 生产数据初始化运行手册

## 发布前提

- 使用 PostgreSQL 17，并已完成可恢复备份和恢复抽检。
- 发布制品、迁移文件和初始化 manifest 来自同一 release version。
- 显式提供生产环境变量文件；不得使用仓库默认凭据。
- 普通 Web 容器必须设置 `ITSM_AUTO_MIGRATE=false`、`ITSM_AUTO_SEED=false`。

## 标准发布

1. 停止新变更，记录数据库版本、应用版本和租户模板版本。
2. 校验备份、可用磁盘、数据库 DDL/DML 权限和连接数余量。
3. 运行一次性 `itsm-init` Job。该 Job 是唯一允许启用
   `ITSM_BOOTSTRAP_ONLY=true` 的生产进程。
4. 查看初始化 run 和 component attempt，确认六个组件均为 `succeeded`：
   `identity-rbac`、`itil-core`、`workflow-core`、`sla-core`、`cmdb-core`、
   `extension-core`。
5. 启动 Web 实例。只有 `GET /api/v1/readyz` 返回 200 才允许接入流量。
6. 对 private、saas 或 saas_msp 的目标租户执行开通验证，并保留 run ID。

Docker Compose 必须显式传入环境文件：

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod config
docker compose -f docker-compose.prod.yml --env-file .env.prod up itsm-init
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
```

## 失败与重试

- 不得手工修改账本为成功。
- 先通过 run ID 定位失败组件和错误；修复根因后使用初始化 CLI 的 `retry`。
- 组件写入和组件内验证在同一事务内执行；失败组件不会提交部分数据。
- lease 未过期时禁止强制接管。executor 崩溃后，等待 lease 到期并确认原进程已停止，
  再由新 executor 重试。
- checksum 不匹配表示发布制品或 manifest 被修改，必须停止发布并重新生成制品。
- 单租户失败只隔离该租户；平台组件失败必须保持全局 Not Ready。

## 回滚与恢复

- 数据库迁移遵循 expand/contract；优先回滚应用镜像，不反向删除运行数据。
- 初始化模板采用 forward-fix。已经被流程实例引用的定义不得物理删除。
- 若结构迁移不可兼容，恢复到发布前备份，在隔离环境验证后再恢复服务。
- 恢复完成后先运行 migration verify，再执行初始化 `verify`；不得直接绕过 readiness。

## 已存在租户的 ticket_types 定向补齐

已完成 bootstrap 的目标可以单独补齐产品默认业务子类型。此入口不是首次
bootstrap，也不替代初始化引擎、迁移验证或 readiness；它不创建租户、账号或
角色，不调用 `itil-core` 或其他 seed，不修改初始化／迁移账本。

在 `itsm-backend` 目录使用经核对的配置文件和数据库环境变量运行：

```bash
go run ./cmd/initialize_ticket_types --tenant-id <existing-tenant-id> --actor-id <existing-admin-id>
go run ./cmd/initialize_ticket_types --tenant-id <existing-tenant-id> --actor-id <existing-admin-id> --apply
```

未指定 `--apply` 时仅执行只读计划，输出实际 database/schema、租户和操作者
ID、产品默认清单摘要、待建／保留／自定义 code。核对环境和计划后再显式 apply。
凭据继续使用既有环境变量或 secret-file 配置，不写进命令参数或计划结果。

定向入口从数据库重新读取现有活跃租户及本租户活跃操作者，通过现有实时
RBAC 校验 `system_config:update`，不信任调用方声称的管理员名称或角色。
apply 在一个 serializable 事务中重新校验权限和全部冲突，再补齐缺少的默认
code，并在同一事务写入 `ticket_types.initialize` 审计。任一插入、并发冲突、
审计或提交失败均不会提交部分配置；失败后先复核原因，再重新运行计划。

默认定义只维护在 `pkg/seeder/ticket_types.go`，bootstrap 和此入口共同消费。
已存在的默认 code 必须与产品配置一致，包括状态、SLA／审批／自动分配开关
及规则配置；差异会拒绝整批，不覆盖管理员自定义。其他 code 保留。历史 ID、
创建人、时间戳和使用次数不被重写；部分初始化可以补缺，完整重跑不新增类型。

验证应读取真实 `ticket_types` 并核对现有 `TicketService.Prepare` 的 code／ID
解析；旧静态 `/ticket-types` 查询不能作为本次补齐证据。Prepare 层验证不代表
完整工单创建、审批、SLA 或运行环境准入已经验收。

## 发布证据

归档以下内容：release version、migration 状态与 checksum、初始化 run ID、六组件版本与
checksum、三种部署模式测试结果、R0 零写入结果、备份/恢复演练记录、已知风险与签字人。
