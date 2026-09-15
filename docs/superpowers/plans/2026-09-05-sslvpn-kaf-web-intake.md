# KAF Web Intake Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** draft — 实施计划待执行；设计已确认，未宣称代码完成。

**Goal:** 复用 KAF 已有对话收集、确认卡片和恢复链路，以真实用户身份可靠创建新 ITSM WorkItem。

**Architecture:** 现有 Unified Agent 和 ServiceRequestWorker 负责对话与确认，新增的新 ITSM 用户客户端只承担系统边界通信。提交状态复用 pending interaction 持久化，业务当前状态从 ITSM 受权查询；任务执行继续使用原 KAF delegation pipeline。

**Tech Stack:** 当前 KAF Python/FastAPI/SQLAlchemy/httpx/Pydantic、现有 React/TypeScript Web 与测试工具。

**Spec:** [设计 §5-6、§10-11](../specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)；依赖 [A2/A6](2026-09-05-sslvpn-itsm-intake-foundation.md) 的冻结契约。

## Global Constraints

- [总计划](2026-09-05-sslvpn-end-to-end-implementation.md) 的范围、身份、错误和发布纪律适用。
- KAF `AGENTS.md` 禁止硬编码路由、第二个分类器、平行实现；多文件重构后运行完整测试。
- 不调用非对话 `/api/v1/intake/analyze` 对聊天结果重新分类。
- 不新增确认 UI；使用 `InteractionCard`、`HitlPreview`、`action.required` 和当前恢复机制。
- 不共享 ITSM JWT 签发密钥给 KAF；用户 exchange secret、webhook HMAC 和 automation token 分开。
- 现有 KAF `uv.lock` 修改不得覆盖；实现 worktree 从已记录提交创建。
- 示例字段与身份只写测试 fixture，生产标签放 messages/prompt/config。

## 文件与公共接口

以下路径相对 KAF 仓库。

| 文件 | 职责 |
| --- | --- |
| Create `src/acp/contracts/workitem_intake.py` | A2 wire 请求/响应/error 的严格模型 |
| Create `src/acp/itsm_intake/{__init__,client,identity}.py` | 新 ITSM 用户身份交换、受权目录/状态查询和创建；不复用遗留系统登录 |
| Modify `src/acp/orchestration/workers/service_request_worker.py` | 现有确认后提交新 Intake；保留用户快照 |
| Modify `src/acp/pending_interactions.py` | 提交状态的条件更新、claim、结果恢复 |
| Modify `src/acp/models/pending_interaction.py` | 仅在现有 context/状态无法表达必要 CAS 时添加版本字段；不得创建第二套草稿表 |
| Modify `src/acp/orchestration/hitl.py` | 复用确认归属与 action 关联 |
| Modify `src/acp/tools/metadata.py` 和现有域注册配置 | 注册受治理用户目录/创建能力；无 SSLVPN 字符串路由 |
| Modify `frontend/src/chat/components/ChatView.tsx`、`frontend/src/chat/types.ts` | 复用卡片，区分已确认、提交中、失败、已创建 |
| Create `tests/test_workitem_intake_client.py`、`tests/test_workitem_intake_identity.py`、`tests/test_service_request_intake_submission.py` | 协议、身份、恢复与正反例 |
| Create `frontend/src/chat/components/ChatView.test.tsx` | 卡片及恢复可见行为 |

创建模型保留 A2 `CreateWorkItemCommand`/`CreateWorkItemResult` 字段，Pydantic 采用 `extra='forbid'`，不能接受两套 schema 自动猜测。`IntakeError` 保存 `http_status/code/retryable/field_errors`，不包含原始 token 或完整响应体。

公共客户端签名：

```python
class ItsmIntakeClient:
    def __init__(self, base_url: str, http: httpx.AsyncClient): ...
    async def exchange(self, assertion: IdentityAssertion, *, purpose: Literal["create", "read"]) -> ExchangeResult: ...
    async def list_catalogs(self, query: str, cursor: str | None, token: str) -> CatalogPage: ...
    async def create(self, command: CreateWorkItemCommand, token: str) -> CreateWorkItemResult: ...
    async def get_catalog(self, catalog_id: int, token: str) -> CatalogContract: ...
    async def get_work_item(self, work_item_id: int, token: str) -> WorkItemView: ...
```

以上为类型签名，不是空实现模板。`IdentityAssertion/ExchangeResult/CatalogPage/CatalogContract/WorkItemView` 在 `contracts/workitem_intake.py` 由 A2/A6 OpenAPI 定义；生产函数全部实际校验状态/envelope/model，失败抛结构化错误。现有 `HttpKafItsmContextClient` 不改成通用用户客户端。

## B1：跨语言协议与用户身份交换

**Files:** 新 contracts、client、identity；Modify `src/acp/config.py`、`src/acp/auth/deps.py` 的调用方；Create 两个 client/identity 测试与 `tests/fixtures/contracts/intake-identity-signature.json`、`intake-create.json`。

**Interfaces:** 消费 A2/A6 OpenAPI 与共享签名 fixture。本地 fixture 是固定版本的契约测试镜像，修改来源仍在 ITSM 契约文件；两端记录同一内容 hash。

- [ ] 将 A2/A6 已冻结契约 fixture 复制到测试目录并记录来源提交，不引用开发者绝对路径作为运行依赖。
- [ ] 写签名红测试：

```python
def test_identity_signature_matches_itsm_fixture():
    import json
    from pathlib import Path
    from acp.itsm_intake.identity import sign_assertion_fields
    f = json.loads(Path("tests/fixtures/contracts/intake-identity-signature.json").read_text())
    assert sign_assertion_fields(f["fields"], f["secret"]) == f["signature"]
```

- [ ] 执行 `uv run pytest tests/test_workitem_intake_identity.py -q`，确认签名模块/行为未实现导致失败。
- [ ] 实现 `sign_assertion_fields(fields: list[str], secret: str) -> str`，严格要求 10 个 v2 字段、拒绝 CR/LF 和首尾空白，与总计划 §3.2/A6 顺序一致：

```python
if len(fields) != 10 or any(not isinstance(v, str) or v != v.strip() or "\n" in v or "\r" in v for v in fields):
    raise ValueError("invalid_assertion_fields")
normalized = [value.strip() for value in fields]
if any(not value for value in normalized):
    raise ValueError("invalid_assertion_fields")
return hmac.new(secret.encode(), "\n".join(normalized).encode(), hashlib.sha256).hexdigest()
```

- [ ] assertion 由验证后的 `CurrentUser.sub`、服务端会话和 workspace 成员关系构建；provider/channel 由配置固定允许范围，nonce 随机生成。用户填写的邮箱、employeeId、tenant 不可作为签名身份来源。exchange 使用独立 secret 文件。
- [ ] client 正确验证 HTTP status、`code/data` 和 Pydantic 响应，不记录 bearer token。测试过期、错误签名、nonce 重放、映射禁用、用户无 workspace 成员关系、automation token 误用均拒绝。
- [ ] 编写创建成功与冲突测试，使用 `httpx.MockTransport` 捕获 payload，断言没有 tenant/actor/requester 注入；201/200 同样读取 result，409 保留 error code 不转成成功。
- [ ] 执行两个测试文件及 `tests/test_auth_router.py tests/test_channel_identity.py tests/test_user_enterprise_identity.py`，提交 `feat(intake): add user-scoped ITSM client and identity exchange`。

## B2：确认快照、提交幂等与崩溃恢复

**Files:** Modify ServiceRequestWorker、pending_interactions、hitl、必要的 pending model/migration；Create `tests/test_service_request_intake_submission.py`。

**Interfaces:** 复用 pending context 新增唯一 `submission` 字段：

```json
{
  "submission": {
    "idempotency_key": "stable-key",
    "state": "confirmed",
    "command": {
      "idempotencyKey": "stable-key", "intakeKind": "catalog_item",
      "recordClass": "service_request_item", "confirmation": "confirmed",
      "title": "SSLVPN access request", "description": "Confirmed request",
      "catalogItemId": 101, "catalogVersion": "catalog-fixture-revision",
      "formSchemaVersion": "form-fixture-revision",
      "formValues": {"vpn_level": "level_1", "access_duration": "duration_30d"}
    },
    "result": null,
    "error_code": null
  }
}
```

`command` 写入 B1 严格模型的完整、已确认内容；上述 ID/版本仅为测试示例，生产值取自用户已确认的目录快照。`state` 为 `confirmed/submitting/unknown/created/rejected`；凭据不落 context。它属于已有交互记录，不创建新草稿或 WorkItem 状态表。

- [ ] 在现有 PostgreSQL pending fixture 增加用例：确认后退出、远程创建成功但本地丢响应、重复点击、卡片取消、过期卡片、另一用户点击、同会话切换 workspace、重新确认修改内容。每种用例明确远程调用次数和 key。
- [ ] 为纯模型增加可执行测试，`ConfirmedSubmission` 定义在 contracts 文件，`new(command, idempotency_key)` 接收已冻结命令，不隐式每次生成键：

```python
def test_submission_roundtrip_preserves_key_and_command():
    from acp.contracts.workitem_intake import ConfirmedSubmission, CreateWorkItemCommand
    import json
    from pathlib import Path
    command = CreateWorkItemCommand.model_validate_json(Path("tests/fixtures/contracts/intake-create.json").read_text())
    original = ConfirmedSubmission.new(command, command.idempotencyKey)
    restored = ConfirmedSubmission.model_validate(json.loads(original.model_dump_json()))
    assert restored.idempotency_key == original.idempotency_key
    assert restored.command == original.command
```

- [ ] 运行 `uv run pytest tests/test_service_request_intake_submission.py -q`，记录红测试。
- [ ] 确认前生成并持久化稳定提交 ID/键和快照；确认使用 action ID、session、user/workspace 的现有归属检查和 CAS。不要在写入恢复上下文之前把 pending 标记为完成并丢弃它。
- [ ] 接口提交前持久化 `submitting`；成功后写不可变创建回执并标记 `created`；网络未知写 `unknown`；业务校验拒绝写 `rejected`。进程在请求前或响应后退出都能加载已 resolved 的提交记录恢复，不只读取 pending 状态。
- [ ] 恢复使用原键和 command 重放，刷新过期用户凭据而不改变创建内容。允许重复远程请求，但必须由 ITSM 幂等收敛到一个 WorkItem；本地单次 claim 不能被误认为跨系统 exactly-once。
- [ ] confirmed 卡片恢复不再次执行分类或查询另一个 Procedure 版本改变字段。目录变化导致 409 时清楚提示并创建新的确认快照，旧回执保留。
- [ ] 运行 `uv run pytest tests/test_pending_interactions.py tests/test_pending_interaction_expiry.py tests/test_hitl.py tests/test_service_request_intake_submission.py -q`，提交 `fix(intake): persist confirmed submissions and recover unknown outcomes`。

## B3：将已存在卡片接到新 ITSM，保持 KAF 决策边界

**Files:** Modify ServiceRequestWorker、`src/acp/contracts/agent_output.py`、域 operation/workflow 注册、prompt/messages、frontend ChatView/types；Create ChatView.test.tsx。

**Interfaces:** `HitlPreview` 继续渲染 fields/actions；输入由 ITSM CatalogContract + KAF 收集值生成，输出确认命令为 B1 模型。Procedure 受理元数据可用于对话说明，但不把 Intake 时选中的 Procedure 当作审批后执行绑定。

- [ ] UI 红测试覆盖：已收集字段按目录定义显示；确认按钮后显示提交中；远程失败不会显示申请编号；创建后显示实际编号；刷新未知状态能恢复；取消不提交。使用 React Testing Library，查询角色/标签而非 CSS。
- [ ] 对 service-request worker 注入 fake client，断言用户确认只调用 `client.create`，不调用 Graph/LDAP/原授权 workflow handler；审批之前 Tool 执行次数为零。
- [ ] 通过现有域 operation 注册和配置指定新 ITSM 提交能力，不写 `if 'VPN' in text` 或字符串路由。复用同一 Agent 的结构化 CTI/Catalog/recordClass，不调用 headless intake 第二次分类。
- [ ] 将目录、字段、期限候选获取接入 A6 用户读契约，校验可见性与版本；既有卡片组件仅补提交状态与结果引用，不新增 SSLVPN 卡片。用户不需要再次在聊天中输入“确认”。
- [ ] 禁止本期入口进入遗留 `ticket_create`/web form/邮件成功 fallback；删除本期 operation 的旧提交注册。其他独立遗留系统调用不混入新端点，也不借本任务全局清理。
- [ ] 首次授权结果从 ITSM 权威 API 查询，客户端只保留 WorkItem ID 和创建回执；查询 403 不使用历史缓存冒充最新结果。
- [ ] 执行 `uv run pytest tests/test_service_request_intake_submission.py tests/test_field_collection.py tests/test_confirmation_digression.py -q`；前端执行 `npm test`，`npm run build`；提交 `feat(chat): submit confirmed service requests through ITSM Intake`。

## B4：跨端合同与 KAF 完整回归

**Files:** Extend B1-B3 测试；Create `tests/test_workitem_intake_live_contract.py`，运行时用明确集成标记和独立测试环境；记录总计划验收报告。

**Interfaces:** 消费 A7 实际部署基线（已包含 C1 的授权策略和结果契约）与测试角色，禁止用同一管理员账号代替用户和审批人。

- [ ] 测试用户交换、查询目录、创建、同键重放、跨用户访问拒绝；确认真实返回符合 A2 OpenAPI 和 B1 模型。
- [ ] 网络代理注入“ITSM 已提交但 KAF 没收到响应”，重启 KAF 后恢复：一个 WorkItem、相同编号、一次启动记录，无重复确认或外部执行。
- [ ] 使用同用户不同 workspace 和不同用户相同 key 验证隔离；不允许 KAF 的现有角色字符串直接授予 ITSM 管理权限。
- [ ] 执行完整 `uv run pytest`，报告通过/失败/跳过数量；执行 KAF 前端测试和构建。锁文件变更只在确有依赖变更时生成，本计划不要求新增依赖。
- [ ] 检查日志、pending context、异常响应没有 token/assertion secret；`git diff --check` 后提交 `test(intake): verify KAF user submission contracts and recovery`。

**完成定义：** 真实用户从已有卡片创建 ITSM 申请成功，重复/未知/权限负例通过；不以 fake client 单测替代跨端合同。
