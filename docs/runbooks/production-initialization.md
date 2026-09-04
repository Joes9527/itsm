# 生产数据初始化运行手册

## 发布前提

- 使用 PostgreSQL 17（major 17），且 `pgvector` 0.8.6 精确可用；一次性 bootstrap 会在 DDL 前执行同一只读 preflight。
- 发布制品、迁移文件和初始化 manifest 来自同一 release version。
- 显式提供生产环境变量文件；不得使用仓库默认凭据。
- `ITSM_MIGRATION_DB_USER` 与 `ITSM_RUNTIME_DB_USER` 必须是不同登录角色；前者仅供
  init/migrate，后者供 API/Worker，且不得为 superuser 或拥有 `BYPASSRLS`。密码来自
  Compose secret/受保护环境文件，不写入 Compose。
- 普通 Web 容器必须设置 `ITSM_AUTO_MIGRATE=false`、`ITSM_AUTO_SEED=false`。
- 一次性 `itsm-init` 必须显式选择 `ITSM_BOOTSTRAP_MODE=upgrade`（常规发布）或
  `ITSM_BOOTSTRAP_MODE=fresh`（仅全新空库）；该变量不提供 reset/drop 行为。

### 全新安装与升级边界

- 全新且确认没有业务对象的数据库首次运行使用 `ITSM_BOOTSTRAP_MODE=fresh`。该路径在 DDL
  前验证空库；中断后只接受同一 release 已提交且通过定义校验的阶段，且不会伪造
  `schema_migrations` 历史。
- Fresh 成功后立即将环境恢复为 `ITSM_BOOTSTRAP_MODE=upgrade`。后续升级依据 release
  catalog 的显式 covered set 规划，不以 `version <= head` 猜测覆盖范围。
- 当前最低直接支持的 upgrade 来源是精确 cataloged `028_schema_release_state`。缺少匹配
  `schema_state` 或 schema 定义不符时，Job 在任何 migration/privilege/promotion 写入前退出；
  更老版本必须先走经评审的分阶段升级路径，不得手工补 ledger 或临时启用兼容 DDL。
- 若现有数据卷来自 PostgreSQL 15/16，禁止直接挂载到 PG17 容器。保留原卷并先做可恢复
  备份，随后选择受控 `pg_dump`/`pg_restore` 到新 PG17 集群，或按官方流程运行
  `pg_upgrade`；验证 pgvector 0.8.6、schema_state、账本和业务数据后才可切换。

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
# 仅首次空库：执行成功后把 .env.prod 恢复为 ITSM_BOOTSTRAP_MODE=upgrade
ITSM_BOOTSTRAP_MODE=fresh docker compose -f docker-compose.prod.yml --env-file .env.prod up itsm-init
# 常规发布：默认且长期保持 upgrade
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

## 发布证据

归档以下内容：release version、migration 状态与 checksum、初始化 run ID、六组件版本与
checksum、三种部署模式测试结果、R0 零写入结果、备份/恢复演练记录、已知风险与签字人。
