# SSLVPN Fulfillment and End-to-End Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** draft — 实施计划待执行；设计已确认，未宣称代码完成。

**Goal:** 在真实 KAF Web → ITSM 审批 → KAF 执行链路中确认外部用户组授权，并验证回执、审计、故障恢复和用户展示。

**Architecture:** ITSM Catalog 定义获批授权目标和有限期限，BPMN 只委派任务；KAF 按既有工具治理执行并保存稳定结果，ITSM 专业服务原子收敛回执与授权事实。复用现有双 Worker 和任务范围 API，不实现到期调度/自动回收。

**Tech Stack:** 当前 ITSM Go/Ent/PostgreSQL、KAF Python/Graph、Playwright、Docker Compose。

**Spec:** [设计 §10-13](../specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)；依赖 [A](2026-09-05-sslvpn-itsm-intake-foundation.md)、[B](2026-09-05-sslvpn-kaf-web-intake.md)。

## Global Constraints

- [总计划](2026-09-05-sslvpn-end-to-end-implementation.md) 全部约束适用。
- “成功终点是获批用户已成为获批外部用户组成员，且 ITSM 的回执、业务状态和审计一致。”
- “BPMN 不固定 Procedure/Tool。”
- “已持久化确认结果的重放不能延长期限。”
- “演练后的受控清理是测试环境恢复要求，不代表到期自动回收功能已交付。”
- 外部演练使用已定义专用夹具；记录目标环境、唯一 change/reference ID、恢复责任和事后清理。
- 现有组成员不重复添加、不声称本系统新授予；不因申请到期自动移组。

## 文件与接口

| 仓库/文件 | 职责 |
| --- | --- |
| ITSM `handlers/service_catalog/access_policy.go` | 类型化授权目标和有限期限配置读取/验证，接入 A5 preflight |
| ITSM `ent/schema/catalog_access_policy.go` | Catalog 专业配置，一对一 Catalog；外部系统/目标组和期限选项配置，不存 Procedure/Tool |
| ITSM `handlers/service_request/access_result.go` | 授权结果验证、期限计算、回执事务贡献者 |
| ITSM `ent/schema/service_request_access_result.go` | WorkItem 的专业履约结果，一对一关联；不是第二个 WorkItem 或执行台账 |
| ITSM `service/{kaf_delegation_service,bpmn_kaf_completion}.go` | 暴露最小受权授权快照，原子消费验证结果 |
| ITSM `service/bpmn/sslvpn_approval_flow.bpmn` | 两级审批到既有 KAF 委派，保持配置解析 |
| KAF `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py` | 保留执行台账、首次确认时间、completion payload replay-only |
| KAF `src/acp/contracts/kaf_delegation.py` | 定义授权结果严格模型，保持现有委派 envelope |
| ITSM `tests/e2e/sslvpn_scenario_test.go` | 真实应用内业务流程回归 |
| ITSM frontend `tests/e2e/sslvpn-approval-flow.spec.ts` | 复用审批浏览器路径 |
| ITSM frontend `tests/e2e/flows/sslvpn-kaf-intake.spec.ts` | 跨 KAF Web/ITSM 的角色 E2E |
| 根 `docs/contracts/kaf-access-result.schema.json` | 两端授权结果契约 |

新 schema 通过 A1/A7 的迁移 owner 分配序号；在当前完整迁移流中验收，不直接修改共享数据库。

## C1：有限期限配置与授权结果模型

**前置依赖：** A3/A5/A6 完成后执行，必须先于 A7 和 B4。此任务的 KAF 类型工作仅涉及现有 delegation contract；新 Intake 客户端模型由 B1 根据本任务的结果契约实现，不反向依赖尚未创建的 B1 模块。

**Files:** 新增上表 access_policy/access_result/schema/契约文件与相邻测试；Modify Catalog DTO/preflight、KAF context 投影、ITSM/KAF 展示类型。

**Interfaces:** `ComputeAccessExpiry(verifiedAt time.Time, durationSeconds int64) (time.Time, error)` 属 SR 域。审批快照引用 CatalogAccessPolicy 的版本、外部系统、目标组 ID、期限选项 key 和数值；身份源使用受信请求人映射。授权结果契约包含：

```json
{
  "outcome": "granted",
  "provider": "graph",
  "subjectId": "external-user-id",
  "groupId": "external-group-id",
  "baseline": "not_member",
  "verifiedAt": "2026-09-05T08:00:00Z",
  "evidenceRef": "opaque-execution-evidence-reference"
}
```

`outcome` 仅允许 `granted/already_present`；未知结果不走成功回执。时长由 ITSM 获批策略计算，KAF 不提交任意 expiresAt。结果记录关联 WorkItem、委派 task 和证据，租户通过关联 WorkItem 授权；不重复存专业无关 title/status/requester。

- [ ] 写时间红测试：

```go
func TestComputeAccessExpiryUsesVerificationTime(t *testing.T) {
    verified := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
    expiry, err := ComputeAccessExpiry(verified, 30*24*60*60)
    require.NoError(t, err)
    require.Equal(t, verified.Add(30*24*time.Hour), expiry)
    _, err = ComputeAccessExpiry(verified, 0)
    require.Error(t, err)
    _, err = ComputeAccessExpiry(time.Time{}, 3600)
    require.Error(t, err)
}
```

- [ ] 运行 `go test ./handlers/service_request -run 'AccessExpiry|AccessResult' -count=1`。
- [ ] 实现有限期限/溢出校验：拒绝非正时长、零验证时间以及转为 time.Duration 纳秒的溢出；最大允许期限来自目录策略，不加业务硬编码上限。转换 UTC 后相加，重放从已持久化结果读取，不重新取 now。
- [ ] CatalogAccessPolicy 以 Catalog FK 保存 typed provider 和外部 target 引用；期限选项用无实体关系的结构化配置表达 key/label/seconds，选项不可含无限期。外部对象引用不是内部关系 JSON 的替代。
- [ ] SR 履约结果以 WorkItem FK 唯一存储，保存 baseline、external subject/group、verifiedAt、expiresAt 和 evidenceRef。`already_present` 不写“本系统新授权时间”；记录验证时间并明确归属不受本次申请管理。
- [ ] 更新原 SSLVPN fixture，去掉“长期有效”，用配置化 option key；用户信息来自认证映射，不信任表单 applicant_upn 覆盖 subject。界面只展示申请有效期，不承诺自动回收。
- [ ] 执行 schema/迁移测试和 `go test ./handlers/service_catalog ./handlers/service_request -count=1`，提交 `feat(service-request): record verified finite access grants`。

## C2：审批后 KAF 执行与原子完成

**Files:** Modify ITSM KAF context、BPMN completion、SR workflow callback；Modify KAF pipeline、contract、`src/acp/mcp/tools/o365.py`、`src/acp/graph/groups.py`、`scripts/procedures/o365_group_membership.md` 及工具注册；Extend `tests/test_kaf_delegation_pipeline.py`、`tests/test_kaf_delegation_contract.py`、ITSM `service/bpmn_kaf_completion_integration_test.go`。

**Interfaces:** 沿用现有 `complete_bpmn_task/update_progress/record_execution_failure` 动作；成功 payload 增加 C1 严格授权结果，参与现有 action ledger 摘要。ITSM 回执入口通过 SR 域事务贡献者写入履约结果，不能 Controller 直接写表。

- [ ] 红测试覆盖：任一级拒绝无委派；同一请求只能两级顺序通过；实际不同审批角色；未审批时 KAF 工具调用被拒绝；目标组/subject 与获批快照不一致时完成回执拒绝。
- [ ] KAF 新增严格结果模型测试（`AccessGrantResult` 在本任务定义到 contract 文件）：

```python
def test_grant_result_rejects_unknown_success():
    import pytest
    from pydantic import ValidationError
    from acp.contracts.kaf_delegation import AccessGrantResult
    with pytest.raises(ValidationError):
        AccessGrantResult.model_validate({
            "outcome": "unknown", "provider": "graph", "subjectId": "u",
            "groupId": "g", "baseline": "not_member",
            "verifiedAt": "2026-09-05T08:00:00Z", "evidenceRef": "e"
        })
```

- [ ] 运行 KAF 两个测试文件和 ITSM `go test ./service ./handlers/service_request -run 'Kaf|KAF|SSLVPN|AccessResult' -count=1`。
- [ ] ITSM context 投影获批目标/期限/审批证据，不发送秘密和整个用户目录。KAF 按 registry 选择与该系统/能力匹配的 Procedure/Tools，不能落入 LDAP 实现处理 Graph 目标。
- [ ] Graph 工具先查目标成员关系，已有成员产生 `already_present`；新增后查询确认再写 durable result。工具风险门禁接收并验证委派审批证据，不通过关闭确认要求或全局 trusted 标志绕过治理。
- [ ] 外部验证失败、限流或超时保存实际执行阶段和未知状态；只有确定未执行/安全幂等的步骤按策略重试。持久化 completion payload 后只重放，不重新运行 Procedure。
- [ ] 在同一个 ITSM 事务中校验任务、写授权结果/审计/专业状态和完成回执。回执重复相同摘要返回原结果；不同目标或时间的重复 payload 冲突，不能覆盖已有成功。
- [ ] 对接口版本变更更新双方 contract tests、镜像和部署要求，在同一发布批次切换；不接受旧无证据回执作为本场景成功。
- [ ] 通过两端测试后分别提交 `feat(kaf): verify delegated access grants before completion` 和 `feat(bpmn): atomically finalize verified access fulfillment`。

## C3：崩溃、重放与用户结果展示

**Files:** Extend ITSM `service/{kaf_delegation_service_test,bpmn_kaf_completion_integration_test,kaf_outbox_dispatcher_test}.go`、SR E2E；Extend KAF pipeline tests；Modify B3 现有结果显示；不新增运维平台。

**Interfaces:** 沿用 delivery 的 pending/blocked/delivery_unknown 与回执 replay-only；用户显示经 ITSM 专业状态映射，不能把 delivery published 当作业务完成。

- [ ] 用现有 fake Graph/client、barrier 和注入故障实现四个测试：外部已成功但响应丢失、验证结果已持久化但完成响应丢失、旧 lease 回写、晚到失败回执。
- [ ] 区分外部已写入但尚无 durable 成功结果的窗口：重查成员关系并关联原执行证据，无法确认授权归属或首次验证时间时进入结果未知/人工核查，不能把恢复时的 now 伪装成原成功时间。
- [ ] 对同一成功 payload 连续及并发重放，断言以下不变量：

```text
external_add_count == 1
process_task_completion_count == 1
service_request_access_result_count == 1
replayed_verified_at == original_verified_at
replayed_expires_at == original_expires_at
late_failure_cannot_replace_success
```

- [ ] 将断言加入现有 SQLite 业务测试和独立 PostgreSQL 并发测试；SQLite 通过不替代 PG lease/CAS 验收。
- [ ] 修改现有 KAF 结果展示，区分“提交确认、申请已创建、待审批、履约中、结果未知、已完成”；显示实际工单引用及受权详情入口。配置缺失/未知结果提供可理解原因，不展示原始堆栈或 provider payload。
- [ ] 浏览器刷新、会话恢复后读取 ITSM 当前状态，拒绝时不显示过期缓存；原已是成员显示“权限已存在”，不显示“刚刚新增”。
- [ ] 运行两端完整受影响测试和前端构建，提交 `test(sslvpn): cover crash recovery and authoritative user results`。

## C4：跨进程、浏览器与外部受控验收

**Files:** Create ITSM frontend `tests/e2e/flows/sslvpn-kaf-intake.spec.ts`；Extend 已有 SSLVPN Playwright/helpers；Create 总计划指定 verification report；Modify 既有部署/runbook 仅记录本次新增必需配置。

**Interfaces:** 使用现有 [受控夹具](../../testing/kaf-delegation-release-closeout-fixture.md)。KAF URL、ITSM URL、测试角色会话、外部测试身份/组都由隔离环境配置提供，不进入源码。此任务标记为共享环境/外部变更，执行前记录具体环境与恢复责任。

- [ ] 环境预检：ITSM API、至少两个 Worker、KAF gateway/backend ready；实际数据库角色隔离；required secrets 可读且不泄露；迁移处于预期版本。Compose 配置解析 仅算静态验证，不能替代运行检查。
- [ ] 浏览器脚本通过真实 KAF Web 对话收集并点击原确认卡片；使用独立用户 context 与两个审批角色 context，沿现有 ITSM 待办页完成审批，不通过后台直接改审批表。
- [ ] 对契约固定字段检查，不依赖模型逐字回复：确认卡片包含目录策略规定字段；捕获创建返回的 WorkItem；查询状态并验证同编号的审批/履约；不得用任意 sleep 等待模型或 Worker。
- [ ] 用独立 browser context 隔离角色，storage state 在测试开始用 `node:fs` 的 `accessSync` 验证文件存在，缺失直接失败；统一在 `finally` 关闭 context。审批 helper 复用已有页面标签，但以本次 WorkItem 编号限定唯一行。若当前页面不显示编号，在同任务补上 WorkItem 引用再验收，禁止模糊选择第一行：

```typescript
import { expect, type Page } from '@playwright/test';

async function approveRequest(page: Page, itsmUrl: string, number: string, comment: string) {
  await page.goto(new URL('/approvals', itsmUrl).href);
  const row = page.getByRole('row').filter({ hasText: number });
  await expect(row).toHaveCount(1);
  await row.getByRole('button', { name: '领取', exact: true }).click();
  await row.getByRole('button', { name: '批准', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '批准任务' });
  await dialog.getByPlaceholder('可填写审批意见').fill(comment);
  await dialog.getByRole('button', { name: '确认批准', exact: true }).click();
  await expect(dialog).not.toBeVisible();
}
```

- [ ] 同一测试先使用主管会话调用 `approveRequest`，再使用网络运维会话调用；每次审批后通过受权任务查询断言任务属于本次 WorkItem 和预期 BPMN 节点。已领取任务的恢复用例独立验证，不用条件跳过领取掩盖角色/分配错误。

- [ ] 先验证纯拒绝路径无 Graph add，再执行受控正例：查询非成员基线 → 正常申请/审批 → 一次 add → 查询成员关系 → ITSM 完成 → 同 payload 重放 → 外部状态和时间不变。
- [ ] 在可恢复阶段停一个 Worker 并重启，记录另一个 claim 与租约恢复；两 Worker 同时运行时不能重复副作用。禁止在结果未知时强制重新派发 Procedure。
- [ ] 测试结束通过夹具指定恢复能力移除本次测试新增成员关系，再次查询确认非成员。若测试开始已是成员，不自行删除不属于测试的权限；换用合格夹具或先由负责人员完成明确基线恢复。
- [ ] 执行最终门禁：

```bash
# ITSM backend
go test ./... -count=1
go build ./...
go test ./tests/e2e -run '^TestSSLVPNScenarioE2E$' -count=1
# ITSM frontend
npm run type-check
npm run lint:check
npm run test:ci
npm run build
npx playwright test tests/e2e/flows/sslvpn-kaf-intake.spec.ts --workers=1
# KAF
uv run pytest
# KAF frontend
npm run build
```

同时执行 A7/B4 的隔离 PG、身份与客户端合同测试。所有集成测试确认有实际运行用例；记录失败/skip 原因，不以命令 exit 0 自动视为验证完成。

- [ ] 输出报告：两端 commit、执行角色/环境别名、命令/退出码、task/event/correlation 引用、外部调用计数、首次/重放时间、最终清理确认。截图/trace 存受限测试产物目录，报告不含秘密、个人身份和完整 payload。
- [ ] 独立审查后提交 `test(e2e): verify KAF to ITSM SSLVPN fulfillment`；任何剩余关键失败保持 No-Go。

**完成定义：** 从用户对话到外部组成员关系真实成功、回执与展示一致、重复不产生副作用，且受控测试对象恢复完成。自动到期回收仍为 Backlog，不在报告中勾选。
