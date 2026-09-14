# SSLVPN 手工全生命周期测试文档更新计划

> **For agentic workers:** Use executing-plans to execute sequentially in this session. This is documentation-only work; no application implementation or runtime mutation is required.

状态：implemented，2026-09-14；文档内容与本地静态核对完成，WSL 实际验收未执行。

**Goal:** 交付含自定义字段、BPMN、Ticket 全生命周期和 KAF 部署准备的 WSL 手工测试手册。

**Architecture:** 业务操作以 `docs/testing/sslvpn-manual-lifecycle-runbook.md` 为主入口。部署操作维护在原 SSLVPN 部署手册，模块用例保留编号并补充当前适用范围。

**Tech Stack:** Markdown；只读核对 Go、Next.js/TypeScript 与 KAF Python/Vite 源码。

## Global Constraints

- 本次只改文档；不执行 WSL 部署、迁移、审批或真实 Graph 授权。
- 自定义字段单独成章，贯穿提交、审批、履约和历史查看。
- 运行环境、字段值、身份和授权目标均以执行时登记的基线为准。
- 不迁移、删除或重编号既有文档与测试；不修改业务源码。

## Task 1：核对并编写主手册

**Files:** 新建 `docs/testing/sslvpn-manual-lifecycle-runbook.md`。

**Interfaces:** 输入为已确认设计、实际页面和 owning API；输出为可逐步执行的角色操作表与测试记录。

- [x] 核对目录管理、自定义字段编辑器、申请页、工单详情、审批中心和流程版本/实例页面。
- [x] 核对字段值快照、授权回执、生命周期和 KAF Procedure/config/proxy。
- [x] 编写环境准备、表单、流程、拒绝/正常路径、异常分支、关闭与恢复步骤。
- [x] 核对每项预期是否属于已有能力、验收要求或尚缺入口。

## Task 2：更新部署手册和现有测试入口

**Files:** 修改 `docs/deployment/sslvpn-wsl-deployment-and-manual-verification.md`、`docs/README.md`、`docs/testing/README.md`、`docs/testing/test-cases/TC-TICKET.md`、`TC-SERVICE-CATALOG.md`、`TC-SERVICE-REQUEST.md`、`TC-WORKFLOW.md`。

**Interfaces:** 主手册负责操作顺序；部署手册负责环境命令；模块用例负责可复用验证点。

- [x] 增补现有 WSL 日常开发环境的部署分流和 KAF/Worker 检查。
- [x] 修正审批入口与版本发布入口，注明特殊 policy 的 API 配置边界。
- [x] 增补字段用例，纠正服务请求与 Ticket 关系及关闭/重开说明。
- [x] 更新索引和交叉链接。

## Task 3：文档验证与交付

- [x] 对新增/修改文档运行相对文件链接检查；核对章节锚点和代码路径。
- [x] 运行 `git diff --check`，复核无私有配置、凭据、运行日志和无关变更。
- [x] 检查自定义字段五类输入、两个受理入口、两级审批、履约、关闭、异常和恢复均有步骤。
- [x] 提交文档到独立分支，交付手册绝对路径；明确未执行 WSL 验收。

## 用户补充范围：UI 与邮件闭环

- [x] 主手册改为 UI 全流程，删除 API 补验与回执重放操作。
- [x] 新增邮件 UI 手册，分别覆盖建单、自动确认、原会话回复、两类附件、通知与内部备注隐私。
- [x] 更新 Agent 执行约束、记录模板、部署与模块索引；缺 UI 入口不能补成 PASS。
- [x] 静态核对路径和文档差异；未执行 WSL、实际邮件收发或业务测试。
