# KAF / ITSM 本机 WSL 开发环境

状态：已核对，2026-09-08。本文记录维护者的本机环境，不是所有部署的默认配置；路径、进程和提交在后续操作前必须复核。目录整理已经完成，版本升级和业务验收另行进行。

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
