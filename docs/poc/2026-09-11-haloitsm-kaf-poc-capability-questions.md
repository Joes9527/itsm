# HaloITSM × KAF PoC 集成能力核验提问清单（中英对照）
# HaloITSM × KAF PoC Integration Capability Question List (Bilingual)

> 立场 / Stance：KAF 侧（提问方）/ KAF side (requesting party)
> 目的 / Purpose：核验 HaloITSM 是否具备承载 KAF 自主委派（autonomous delegation）所需的**集成能力** / Verify whether HaloITSM provides the **integration capabilities** required by KAF autonomous delegation
> 贯穿场景 / Scenario：SSLVPN 权限申请端到端 / SSLVPN access request, end to end
> 日期 / Date：2026-09-11
> 范围 / Scope：以集成能力为主线，共 13 个集成域（A–M）；UI、商务等非集成项见第 7 节 / Integration-first: 13 domains (A–M); non-integration topics are in Section 7
> 使用方式 / How to use：会前预读；会上按「Top 12 必问」与 P0 项推进；答案与证据当场记录 / Pre-read; drive the meeting with Top 12 and P0 items; record answers and evidence live

---

## 0. 会议说明 / Meeting Guide

### 0.1 会议目标 / Meeting goal

中文：以 SSLVPN 权限申请端到端为唯一贯穿场景，逐项确认 HaloITSM 能否满足 KAF 的集成能力要求（第 1 节）；对不满足项，当场确认缺口影响、替代路径与排期承诺。本次会议**不是**产品功能演示会，请以**接口、事件、权限与可靠性证据**作答，避免用 UI 演示替代集成能力证明。

EN: Using the SSLVPN access request end-to-end flow as the single scenario, confirm item by item whether HaloITSM meets KAF's integration requirements (Section 1). For any gap, agree on impact, fallback and a committed timeline in the room. This is **not** a product demo; answers must be supported by **API, event, permission and reliability evidence** rather than UI walkthroughs.

### 0.2 参会角色 / Participants (fill in before the meeting)

| 角色 Role | 姓名 Name | 关注点 Focus |
|---|---|---|
| Halo 售前/方案架构 / Halo presales & solution architect | | 集成可行性、能力边界 / Integration feasibility, capability boundaries |
| Halo 产品/研发（如可参加）/ Halo product or engineering (if available) | | 缺口排期、扩展点 / Gap roadmap, extension points |
| Kerry 全球 IT 负责人 / Kerry global IT lead | | 全球落地、成本、合规 / Global rollout, cost, compliance |
| Kerry 区域 IT（区域待填）/ Kerry regional IT (TBD) | | 区域差异、语言、时区 / Regional variance, language, time zone |
| KAF 侧（提问方）/ KAF side (requesting party) | | 委派契约、可靠性、审计 / Delegation contract, reliability, audit |
| 信息安全/合规 / Security & compliance | | 数据驻留、密钥、渗透测试 / Data residency, secrets, pen-test |

### 0.3 判定口径 / Decision scale

| 判定 Verdict | 含义 Meaning |
|---|---|
| 满足 Met | 具备原生能力，可提供文档与沙箱证据 / Native capability with documentation and sandbox evidence |
| 部分满足 Partial | 存在能力但受限（需定制、需绕行、有频率或数量上限）/ Capability exists but constrained (custom build, workaround, rate or volume limits) |
| 不满足 Not met | 当前版本不具备，需 Halo 开发或第三方中间件 / Not available in the current version; needs Halo development or middleware |
| 待验证 To verify | 口径不清或需实测，会后台架验证 / Unclear or needs testing; verify after the meeting |

### 0.4 证据等级 / Evidence levels

| 等级 Level | 类型 Type | 可否作为 PoC 验收依据 Valid for PoC acceptance |
|---|---|---|
| E1 | 官方文档章节或 OpenAPI 定义 / Official documentation or OpenAPI definition | 可 Yes |
| E2 | 沙箱实测（双方共同执行并可复现）/ Sandbox test executed jointly and reproducible | 可（优先）Yes (preferred) |
| E3 | 现场演示 / Live demo | 可（需录像或截图留档）Yes (record or screenshot required) |
| E4 | 截图或录像 / Screenshot or recording | 可（需标注环境与版本）Yes (note environment and version) |
| E5 | 口头承诺 / Verbal commitment | 不可，须转书面确认 No; must be confirmed in writing |

规则 / Rule：任何 E5 结论必须标注「待书面确认」并指定责任人与截止日期。Any E5 conclusion must be flagged "pending written confirmation" with an owner and a due date.

### 0.5 时间盒建议（90 分钟）/ Suggested time box (90 minutes)

| 时段 Slot | 内容 Content |
|---|---|
| 0–10 分钟 min | 对齐 PoC 目标、场景与判定口径 / Align on goal, scenario and decision scale |
| 10–60 分钟 min | Top 12 必问逐条过；P0 必须给出结论或跟进计划 / Walk Top 12; every P0 gets a verdict or a follow-up plan |
| 60–80 分钟 min | 分域 P0/P1 重点项与开放问答 / Key domain-level P0/P1 items and open Q&A |
| 80–90 分钟 min | 待索取材料、责任人、时间表、Go/No-Go 初判 / Materials, owners, timeline, preliminary Go/No-Go |

---

## 1. KAF 对 ITSM 的集成能力要求 / KAF Integration Requirements

### 1.1 P0 硬性要求 / Mandatory requirements (P0)

| # | 要求 Requirement | 本质 Rationale | 不满足的后果 Impact if not met |
|---|---|---|---|
| 1 | 身份由认证上下文派生，不信任请求体 / Identity derived from the authenticated context, never from the request body | 不允许拿 body 当授权依据 / The payload must not be an authorization source | 越权建单、审计失真 / Unauthorized creation, unreliable audit |
| 2 | 流程内可设置「外部委派等待点」，KAF 完成后流程才恢复 / An in-process external delegation wait point that resumes only after KAF completes | 编排必须能挂起并等待外部执行体 / Orchestration must suspend and wait for an external executor | 无法实现 AI 自主履约闭环 / No autonomous fulfillment loop |
| 3 | 提供任务范围 API：按 taskId 取上下文、提交 typed action / Task-scoped API: read context by taskId and post typed actions | 不接受「通用工单 API 全权」 / No account-wide ticket API as a substitute | 权限过宽，影响面无法收敛 / Over-broad credentials, uncontrolled blast radius |
| 4 | 动作白名单 + 版本乐观锁（expectedVersion）/ Action allow-list plus optimistic locking (expectedVersion) | 并发与越权防护 / Concurrency and privilege protection | 重复写入、陈旧覆盖、越权动作 / Duplicate writes, stale overwrites, unauthorized actions |
| 5 | 可靠投递 + 幂等 + 回放；迟到失败不得覆盖成功 / Reliable delivery, idempotency and replay; a late failure must not overwrite success | at-least-once 收敛为一次副作用 / Converge at-least-once into exactly one side effect | 重复授权、重复通知、状态回退 / Duplicate grants, duplicate notifications, regressed state |
| 6 | 审计区分技术账号与终端用户，贯穿 correlationId / Audit distinguishes the technical account from the end user and threads a correlationId | 自动化不得伪装成人操作 / Automation must not masquerade as human action | 合规不通过、无法追溯 / Compliance failure, untraceable execution |
| 7 | 租户隔离，跨租户访问 fail-closed / Tenant isolation with fail-closed cross-tenant access | MSP 与多区域的前提 / Prerequisite for MSP and multi-region | 跨租户泄露 / Cross-tenant leakage |
| 8 | 配置化绑定 + 未知类型 fail-closed / Configuration-driven binding plus fail-closed dispatch on unknown types | 不允许静默兜底或 no-op / No silent fallback or no-op | 流程静默推进、误履约 / Silent progression, wrong fulfillment |

### 1.2 次级要求 / Secondary requirements (P1–P2)

| # | 要求 Requirement |
|---|---|
| 9 | 最小披露：只存脱敏摘要与证据引用 / Minimal disclosure: store only redacted summaries and evidence references |
| 10 | 多区域部署与数据驻留 / Multi-region deployment and data residency |
| 11 | 渠道集成（含中国区渠道）/ Channel integration, including China channels |

命名约定 / Naming convention：KAF 侧以 `Workspace` 为隔离单位，与 ITSM 的 `tenant` 约定为一一映射，不新造第三个身份概念。/ KAF uses `Workspace` as its isolation unit, mapped one-to-one to the ITSM `tenant`; no third identity concept is introduced.

---

## 2. 贯穿场景与异常分支 / End-to-end Scenario & Exception Branches

### 2.1 主路径 / Happy path

```text
1. 用户通过 KAF 对话表达申请意图 / The user states the request in a KAF conversation
2. KAF 理解意图、收集字段、确认卡片 / KAF extracts intent, collects fields, shows a confirmation card
3. KAF 以当前用户身份调用 ITSM 建单 / KAF calls the ITSM intake API as the current user
4. ITSM 创建记录并启动两级审批与 SLA / ITSM creates the record and starts the two-level approval and SLA
5. 审批通过，流程到达 KAF 委派等待点 / Approval completes and the flow reaches the KAF delegation wait point
6. KAF 读取任务上下文，执行受治理的 Procedure/Tool / KAF reads the task context and runs governed procedures and tools
7. KAF 回报结果；ITSM 校验、落地、推进流程 / KAF reports the result; ITSM validates, applies it and advances the flow
```

### 2.2 必须一并验证的异常分支 / Exception branches to verify as well

| 分支 Case | 验证点 Verification point |
|---|---|
| B1 重放 / Replay | 同一事件或动作重复到达，只产生一次副作用 / Duplicate events or actions produce exactly one side effect |
| B2 迟到失败 / Late failure | 已成功完成后收到失败回报，不得回退状态或覆盖成功回执 / A failure arriving after success must not regress state or overwrite the success receipt |
| B3 陈旧版本 / Stale version | 携带过期 version 的动作被拒绝并返回结构化原因码 / An action with an outdated version is rejected with a structured reason code |
| B4 越权 / Unauthorized | 用非任务所属租户或身份的凭据提交动作，被 fail-closed 拒绝 / An action submitted by the wrong tenant or identity is rejected fail-closed |
| B5 外部失败 / External failure | KAF 回报执行失败时，流程进入明确的失败或人工介入分支 / On a reported failure the flow enters an explicit failure or manual-intervention branch |
| B6 审批拒绝 / Approval rejection | 任一级拒绝，不产生委派任务与开通结果 / A rejection at any level produces no delegation task and no access grant |
| B7 超时 / Timeout | 外部系统长时间不回调时按配置的超时分支处理并可告警 / A non-responding external system is handled by a configured timeout branch with alerting |

---

## 3. Top 12 必问 / Top 12 Must-Ask

| # | 中文 | English | 编号 Ref |
|---|---|---|---|
| 1 | Halo Workflow 是否支持外部服务任务/等待节点：节点挂起、调用外部系统、外部回调后才继续？机制是什么？ | Does Halo Workflow support an external service task or wait node that suspends the flow, calls an external system and resumes only on callback? What is the mechanism? | Q-C-01 |
| 2 | 外部长时间不回调时，节点如何超时、告警、走失败分支？超时是否可配置？ | When the external system does not respond, how does the node time out, alert and branch to failure? Is the timeout configurable? | Q-C-02 |
| 3 | 委派节点的允许动作能否在流程定义中声明并由 API 强制执行？未声明的动作能否被提交？ | Can the delegation node declare allowed actions in the process definition and have the API enforce them? Can an undeclared action be submitted? | Q-C-03 |
| 4 | 当动作类型或 handler 未注册时，Halo 是报错阻塞还是静默跳过继续？ | When an action type or handler is unregistered, does Halo block with an error or silently skip and continue? | Q-C-04 |
| 5 | 是否有按 taskId 返回任务上下文与允许动作的 API，而非要求通用工单查询权限？ | Is there an API that returns task context and allowed actions by taskId, instead of requiring general ticket query rights? | Q-D-01 |
| 6 | 更新或动作接口是否支持乐观锁（expectedVersion / ETag / If-Match）？陈旧版本返回什么？ | Do update or action endpoints support optimistic locking (expectedVersion / ETag / If-Match)? What is returned on a stale version? | Q-D-03 |
| 7 | 出站 Webhook 的投递保证、重试、签名、去重与补拉机制分别是什么？ | What are the delivery guarantees, retries, signatures, deduplication and catch-up mechanisms for outbound webhooks? | Q-E-01 / Q-E-02 / Q-E-04 |
| 8 | 重复回调或重复动作能否保证只应用一次？迟到失败能否不覆盖已成功回执？ | Can duplicate callbacks or actions be applied exactly once? Can a late failure be prevented from overwriting a success receipt? | Q-E-06 / Q-E-07 / Q-E-08 |
| 9 | API 能否以最终终端用户身份建单，且服务端会校验或覆盖请求体中的 requester 与 tenant？ | Can the API create a record as the end user, with the server validating or overriding requester and tenant in the payload? | Q-A-01 / Q-A-02 |
| 10 | Halo 是否原生多租户或 MSP？跨租户访问是否 fail-closed？workspace 与 tenant 如何一一映射？ | Is Halo natively multi-tenant or MSP-capable? Is cross-tenant access fail-closed? How do workspace and tenant map one-to-one? | Q-G-02 |
| 11 | 审计能否区分技术账号与终端用户、贯穿 correlationId，并把外部执行证据写入时间线？ | Can audit distinguish the technical account from the end user, thread a correlationId, and write external execution evidence to the timeline? | Q-H-01 / Q-H-02 |
| 12 | 集成落地能力：私有部署与 SaaS 选项、区域与数据驻留、网络与密钥、API 版本与限流分别如何支持？ | Deployment reality: how are private and SaaS options, region and data residency, network and secrets, API versioning and rate limits supported? | Q-L-01 / Q-L-02 / Q-L-03 / Q-K-01 |

---

## 4. 集成能力分域提问 / Integration Domain Questions

> 每条问题含三行：中文问法、英文问法、不满足时的替代路径。可直接照读，也可交 Halo 书面作答。
> Each question has three lines: the Chinese wording, the English wording, and the fallback if the capability is missing. Read them aloud or hand them over for written answers.

### A. 认证与身份集成 / Authentication & Identity Integration

> KAF 要求 / Requirement：复用 ITSM 认证与身份体系；不以请求体作为授权依据；自动执行使用技术账号并保留原 requester。/ Reuse the ITSM authentication and identity system; never trust the request body for authorization; automation runs under a technical account while the original requester is preserved.

**Q-A-01 终端用户身份建单 / Create on behalf of the end user** `P0` · `E1+E3`
- 中：API 是否支持 OIDC/OAuth2 授权码或令牌交换，使 KAF 能以最终终端用户身份调用建单接口？支持哪些 IdP（Entra ID / Azure AD、ADFS、Okta）？
- EN: Does the API support OIDC/OAuth2 authorization code or token exchange so KAF can call the create API as the end user? Which IdPs are supported (Entra ID / Azure AD, ADFS, Okta)?
- 替代 / Fallback：若仅 client credentials，要求提供委托身份机制并由服务端校验，且在审计中区分。/ If only client credentials exist, require a delegated-identity mechanism validated server-side and distinguishable in audit.

**Q-A-02 请求体身份校验 / Identity validation of the payload** `P0` · `E2`
- 中：请求体中的 requester、company、tenant 字段，服务端是否按 token 归属强制覆盖或校验？能否拒绝与 token 不一致的请求？
- EN: For requester, company and tenant fields in the payload, does the server override or validate them against the token? Can it reject requests that contradict the token?
- 替代 / Fallback：KAF 侧前置校验加 Halo 侧审计告警，但需书面确认风险承担。/ Pre-validate on the KAF side and audit-alert on the Halo side, with written risk acceptance.

**Q-A-03 委托身份与模拟 / Delegated identity and impersonation** `P0` · `E1+E2`
- 中：是否支持 act-as 或 impersonation，使操作以终端用户身份呈现但以技术账号审计？两条身份记录如何同时保留？
- EN: Is act-as or impersonation supported so the action appears as the end user while being audited under the technical account? How are both identities retained together?
- 替代 / Fallback：技术账号执行加自定义字段记录原 requester，需确认字段不可被普通 API 篡改。/ Execute under the technical account and store the original requester in a custom field, with tamper resistance confirmed.

**Q-A-04 SSO 协议与目录 / SSO protocols and directory** `P1` · `E1`
- 中：支持哪些 SSO 协议（SAML 2.0、OIDC、LDAP）？跨区域多目录（多 Entra ID 租户）如何映射到同一 Halo 实例？
- EN: Which SSO protocols are supported (SAML 2.0, OIDC, LDAP)? How are multiple regional directories or Entra ID tenants mapped into one Halo instance?
- 替代 / Fallback：区域内独立实例加目录同步任务，需评估运维成本。/ Per-region instances with directory sync, with the operational cost assessed.

**Q-A-05 令牌生命周期 / Token lifecycle** `P1` · `E1+E2`
- 中：API 令牌的有效期、刷新、吊销与 IP 绑定如何管理？技术账号凭据泄露时的轮换与失效时效是多少？
- EN: How are token lifetime, refresh, revocation and IP binding managed? What is the rotation and invalidation latency if a technical account credential leaks?
- 替代 / Fallback：短周期令牌加密钥托管服务，需 Halo 支持运行时轮换。/ Short-lived tokens plus an external secret manager, requiring runtime rotation support.

### B. 受理与记录创建集成 / Intake & Record Creation Integration

> KAF 要求 / Requirement：统一建单契约，支持 idempotencyKey、recordClass、Catalog、CI、动态表单；校验失败返回机器可读错误码。/ A unified creation contract supporting idempotencyKey, recordClass, catalog items, CIs and dynamic forms; validation failures return machine-readable error codes.

**Q-B-01 统一创建端点 / Unified creation endpoint** `P0` · `E1+E2`
- 中：是否有统一创建 API 可跨 Incident、Service Request、Change、Problem 一次调用？还是每个模块独立端点？recordClass 在 Halo 中如何表达与持久化？
- EN: Is there one creation API covering Incident, Service Request, Change and Problem, or one endpoint per module? How is recordClass expressed and persisted in Halo?
- 替代 / Fallback：按类型分别调用并由 KAF 路由，需确认类型语义如何持久化与查询。/ Call per type with KAF-side routing, confirming how the class semantics are persisted and queried.

**Q-B-02 创建幂等 / Idempotent creation** `P0` · `E2`
- 中：创建接口是否支持幂等键？重复提交返回同一记录还是新建？幂等窗口多长？冲突时返回什么？
- EN: Does the create API support an idempotency key? Does a duplicate submission return the same record or create another? How long is the idempotency window and what is returned on conflict?
- 替代 / Fallback：使用唯一外部引用字段加冲突返回码自行实现，需确认唯一约束与并发行为。/ Implement via a unique external-reference field with a conflict code, confirming uniqueness and concurrency behavior.

**Q-B-03 动态表单字段 / Dynamic form fields** `P0` · `E1+E2`
- 中：创建时能否一次性提交动态表单与自定义字段值？支持哪些字段类型（text、textarea、number、date、select、multiselect、boolean、file）？必填与格式校验是否服务端强制？
- EN: Can dynamic form and custom field values be submitted in the same create call? Which types are supported (text, textarea, number, date, select, multiselect, boolean, file)? Are required and format checks enforced server-side?
- 替代 / Fallback：创建后补写字段，需评估事务一致性与半成品记录风险。/ Write fields after creation, assessing transactional consistency and half-created records.

**Q-B-04 Catalog 引用与一致性 / Catalog reference and consistency** `P0` · `E2`
- 中：创建时如何引用 Catalog Item？服务端如何校验其目标类型与 recordClass 一致？Catalog 项能否绑定表单、流程与 SLA？
- EN: How is a catalog item referenced at creation? How does the server validate that its target class matches the recordClass? Can a catalog item bind a form, a process and an SLA?
- 替代 / Fallback：KAF 侧维护映射表，Halo 仅存 ID，一致性由对账任务保证。/ Keep the mapping on the KAF side with only the ID stored in Halo, reconciling by job.

**Q-B-05 机器可读错误码 / Machine-readable errors** `P0` · `E2`
- 中：校验失败是否返回字段级机器可读错误码？能否区分不可见、不存在、目标类型不匹配、表单无效、确认策略不满足？
- EN: Do validation failures return field-level machine-readable codes? Can they distinguish not-visible, not-found, target-class mismatch, invalid form and unmet confirmation policy?
- 替代 / Fallback：解析错误文本，需 Halo 承诺文案稳定；脆弱性需记录为风险。/ Parse error text with a Halo commitment on message stability, logged as a fragile path.

**Q-B-06 创建响应内容 / Create response payload** `P0` · `E2`
- 中：创建成功的响应是否返回编号、记录类型、状态、当前流程与 SLA 摘要、版本或 ETag、可跳转链接？
- EN: Does a successful create response return the number, record class, status, current process and SLA summary, version or ETag, and a deep link?
- 替代 / Fallback：创建后二次查询补全，增加一次调用与竞态窗口。/ Follow up with a second read, adding a call and a race window.

**Q-B-07 渠道来源与自动建单 / Source channel and auto-creation** `P1` · `E3`
- 中：来源渠道能否记录并区分 KAF Web、ITSM、Teams、邮件？渠道自动建单策略能否按租户配置？
- EN: Can the source channel be recorded and distinguished across KAF Web, ITSM, Teams and email? Can auto-creation policy be configured per tenant?
- 替代 / Fallback：渠道策略由 KAF 侧控制，Halo 仅记录来源字段。/ Keep channel policy on the KAF side with only the source field in Halo.

**Q-B-08 AI 决策溯源 / AI decision provenance** `P1` · `E2`
- 中：能否持久化 KAF 的 AI 决策元数据（confidence、model、promptVersion、rationale、conversationRef）并可按记录查询？字段数量与长度上限是多少？
- EN: Can KAF AI decision metadata (confidence, model, promptVersion, rationale, conversationRef) be persisted and queried per record? What are the field count and length limits?
- 替代 / Fallback：存入少量自定义字段并保留外部索引，需确认上限与检索能力。/ Store in a few custom fields plus an external index, confirming limits and search support.

**Q-B-09 多子任务 / Multiple child tasks** `P1` · `E2`
- 中：一个请求是否支持多个 Catalog Item 或子任务？子任务能否独立指派、独立状态、独立 SLA？
- EN: Can one request carry multiple catalog items or child tasks? Can child tasks be assigned, statused and SLA-tracked independently?
- 替代 / Fallback：多事项拆为多条记录并建立关联，需确认关联查询与展示。/ Split into separate records with relations, confirming relation queries and display.

### C. 流程编排与外部委派 / Workflow Orchestration & External Delegation

> KAF 要求 / Requirement：流程到达委派节点时创建等待任务、可靠通知 KAF、KAF 完成后才沿出边继续；不静默跳过；超时与失败有明确处理。/ On reaching the delegation node the process creates a wait task, notifies KAF reliably, and continues only after KAF completes; no silent skip; timeouts and failures are handled explicitly.
> 判定重点 / Priority：这是最可能的能力缺口所在，请优先确认。/ This is the most likely gap; confirm it first.

**Q-C-01 外部委派等待点 / External delegation wait point** `P0` · `E1+E2+E3`
- 中：Workflow 是否支持外部服务任务或等待节点：流程在节点挂起、调用外部系统、外部回调后才继续？具体机制是什么（Webhook action 加 wait、REST callback、轮询任务）？
- EN: Does the workflow support an external service task or wait node that suspends at the node, calls an external system and resumes only on callback? What exactly is the mechanism (webhook action plus wait, REST callback, polling task)?
- 替代 / Fallback：用等待状态加定时轮询加条件网关模拟，需确认不会流程卡死或误推进。/ Simulate with a wait status, scheduled polling and a conditional gateway, confirming no deadlock or false progression.

**Q-C-02 超时、升级与失败分支 / Timeout, escalation and failure branch** `P0` · `E2`
- 中：外部长时间不回调时节点如何处理？是否支持超时、超时走指定分支、升级或告警？超时能否按节点或按流程配置？
- EN: What happens when the external system does not call back? Are timeout, timeout branching, escalation and alerting supported? Is the timeout configurable per node or per process?
- 替代 / Fallback：使用定时 Automation 补偿，需明确补偿动作的幂等性与重复触发保护。/ Compensate with scheduled automations, defining idempotency and duplicate-trigger protection.

**Q-C-03 允许动作的声明与强制 / Declared and enforced allowed actions** `P0` · `E2`
- 中：委派节点的允许动作能否在流程定义中声明，并由 API 强制执行？外部系统能否提交未声明的动作？
- EN: Can the delegation node declare allowed actions in the process definition and have the API enforce them? Can an external system submit an action that was not declared?
- 替代 / Fallback：Halo 侧硬性限制加 KAF 侧白名单双重校验，但源头不强制仍是风险。/ Enforce on both sides with a Halo hard limit and a KAF allow-list, accepting that the source still does not enforce it.

**Q-C-04 未注册动作 fail-closed / Fail-closed on unregistered actions** `P0` · `E2`
- 中：当动作类型或 handler 未注册或不存在时，Halo 是报错阻塞，还是静默跳过继续？
- EN: When an action type or handler is unregistered or missing, does Halo block with an error or silently skip and continue?
- 替代 / Fallback：若为静默跳过，视为架构级缺口，需 Halo 承诺修复或改用显式失败分支。/ If it silently skips, treat this as an architectural gap requiring a Halo fix or an explicit failure branch.

**Q-C-05 流程版本与在途实例 / Process versioning and in-flight instances** `P0` · `E1+E2`
- 中：发布新版本后，运行中实例是否继续按旧版本执行？能否查询某实例绑定的流程定义版本与内容指纹？
- EN: After publishing a new version, do in-flight instances continue on the old version? Can the bound process definition version and content fingerprint of an instance be queried?
- 替代 / Fallback：冻结运行实例并手工迁移，需评估在途记录数量与迁移风险。/ Freeze and manually migrate in-flight instances, assessing volume and risk.

**Q-C-06 审批、网关与拒绝分支 / Approvals, gateways and rejection branch** `P0` · `E2+E3`
- 中：审批节点是否支持多级、条件网关、拒绝分支、按角色或组候选、重复审批幂等？拒绝后是否有明确终态且不触发后续委派？
- EN: Do approval nodes support multiple levels, conditional gateways, rejection branches, role or group candidates and duplicate-decision idempotency? Does a rejection reach an explicit terminal state without triggering delegation?
- 替代 / Fallback：复杂条件在 Halo 内建模，KAF 仅处理委派节点。/ Model complex conditions inside Halo and let KAF handle only the delegation node.

**Q-C-07 流程状态外部可查 / External visibility of process state** `P0` · `E2`
- 中：流程关键节点与状态能否被外部 API 查询，包括当前等待点、状态、创建与完成时间、版本？
- EN: Can key nodes and states be queried externally, including the current wait point, status, creation and completion times and version?
- 替代 / Fallback：通过自定义字段镜像状态，存在不一致风险需对账。/ Mirror the state into custom fields, reconciling for potential divergence.

**Q-C-08 节点推进幂等 / Idempotent node advancement** `P0` · `E2`
- 中：流程节点推进是否幂等？同一回调重复到达是否只推进一次？
- EN: Is node advancement idempotent? Does a repeated callback advance the flow only once?
- 替代 / Fallback：KAF 侧去重加 Halo 侧锁，并发窗口仍需实测验证。/ Deduplicate on the KAF side with locking in Halo, verifying the concurrency window by test.

**Q-C-09 流程绑定优先级 / Process binding precedence** `P0` · `E2`
- 中：是否支持流程绑定优先级（Catalog 指定、租户加类型加场景、租户加类型默认、明确无需流程）？无匹配时是静默选其他流程还是阻断报错？
- EN: Is binding precedence supported (catalog-specified, tenant plus class plus scenario, tenant plus class default, explicitly no process)? When nothing matches, does it silently pick another process or block with an error?
- 替代 / Fallback：绑定规则放 KAF 或中间层，Halo 仅按显式流程启动，需确认不会静默兜底。/ Keep binding rules in KAF or middleware and start only explicit processes, confirming no silent fallback.

### D. 任务范围 API 与动作契约 / Task-scoped API & Action Contract

> KAF 要求 / Requirement：除建单外，KAF 的动作必须关联有效 taskId，携带 expectedVersion、runId、stepId、幂等键、correlationId 与 typed payload；结果需区分 applied、already_applied、stale_version、task_not_active、forbidden、domain_rejected。/ Besides creation, every KAF action must be bound to a valid taskId and carry expectedVersion, runId, stepId, idempotency key, correlationId and a typed payload; results must distinguish applied, already_applied, stale_version, task_not_active, forbidden and domain_rejected.

**Q-D-01 任务上下文 API / Task context API** `P0` · `E1+E2`
- 中：是否有 API 按流程任务 ID 返回该任务上下文（关联记录、表单快照、当前等待点、允许动作、当前版本），而非要求通用工单查询权限？
- EN: Is there an API that returns task context by process task ID (related record, form snapshot, current wait point, allowed actions, current version) instead of requiring general ticket query rights?
- 替代 / Fallback：通用查询加中间层裁剪，权限仍过宽需额外风险控制。/ Use a general query with middleware trimming, accepting over-broad rights and adding controls.

**Q-D-02 任务范围授权 / Per-task authorization** `P0` · `E2`
- 中：能否只授予某个任务范围的操作权限，而非账户级全量权限？授权校验发生在服务端还是仅靠调用方自律？
- EN: Can operation rights be scoped to a single task rather than account-wide? Is the check enforced server-side rather than relying on caller discipline?
- 替代 / Fallback：中间层代理加短期凭据，但需 Halo 侧配合校验。/ Use a middleware proxy with short-lived credentials, requiring Halo-side verification.

**Q-D-03 乐观锁与并发 / Optimistic locking and concurrency** `P0` · `E2`
- 中：更新或动作接口是否支持 expectedVersion、ETag 或 If-Match？陈旧版本返回什么？并发下是否保证只应用一次？
- EN: Do update or action endpoints support expectedVersion, ETag or If-Match? What is returned on a stale version? Is exactly-once application guaranteed under concurrency?
- 替代 / Fallback：唯一约束加幂等键组合，但状态机并发正确性需逐项验证。/ Combine unique constraints with idempotency keys, verifying state-machine concurrency case by case.

**Q-D-04 支持的 typed action / Supported typed actions** `P0` · `E1+E2`
- 中：支持哪些外部 typed action：complete task、update progress、record failure、assign、resolve、close？各自的服务端校验规则与拒绝码是什么？
- EN: Which external typed actions are supported: complete task, update progress, record failure, assign, resolve, close? What are the server-side validation rules and rejection codes for each?
- 替代 / Fallback：先只开放 complete、update progress 与 record failure，其余延后。/ Open only complete, update progress and record failure first and defer the rest.

**Q-D-05 结构化拒绝码 / Structured rejection codes** `P0` · `E2`
- 中：拒绝原因能否区分 applied、already_applied、stale_version、task_not_active、forbidden、domain_rejected？码值是否承诺稳定？
- EN: Can rejection reasons distinguish applied, already_applied, stale_version, task_not_active, forbidden and domain_rejected? Are the codes committed as stable?
- 替代 / Fallback：约定错误码映射表，需 Halo 承诺码值与文案稳定。/ Agree a code mapping table with a Halo commitment on stable codes and messages.

**Q-D-06 动作与完成任务的原子性 / Atomicity of action and task completion** `P0` · `E1+E2`
- 中：业务动作与完成流程任务是否原子？部分成功如何回滚，例如 resolve 成功但 task completion 失败？
- EN: Are the business action and task completion atomic? How is a partial success rolled back, for example when resolve succeeds but task completion fails?
- 替代 / Fallback：拆为两步加补偿任务，需明确补偿责任方与失败可见性。/ Split into two steps with a compensation task, defining ownership and failure visibility.

**Q-D-07 执行证据写入 / Writing execution evidence** `P1` · `E2`
- 中：外部系统能否把执行证据（摘要、证据引用、Procedure 与版本、run 与 step、correlationId）写入记录？以什么形式（时间线条目、自定义字段、附件）？是否只读？
- EN: Can the external system write execution evidence (summary, evidence references, procedure and version, run and step, correlationId) to the record? In what form (timeline entry, custom field, attachment) and is it read-only?
- 替代 / Fallback：写自定义字段加附件，需确认只读性与审计关联。/ Write to custom fields and attachments, confirming read-only behavior and audit linkage.

**Q-D-08 终态守卫 / Terminal-state guards** `P0` · `E2+E3`
- 中：已关闭记录能否被重新指派或修改？状态机守卫是否服务端强制，而不仅在 UI 层？
- EN: Can a closed record be reassigned or modified? Are state-machine guards enforced server-side rather than only in the UI?
- 替代 / Fallback：若仅 UI 限制，视为 P0 缺口，需 Halo 在 API 层补齐守卫。/ If the restriction is UI-only, treat it as a P0 gap requiring an API-layer guard.

### E. 事件、Webhook 与回调可靠性 / Events, Webhooks & Callback Reliability

> KAF 要求 / Requirement：事件推送为主路径加补拉兜底；at-least-once 加消费幂等；迟到失败不得覆盖成功；completion payload 仅可重放，不得重新执行外部 Procedure。/ Push events as the primary path with polling catch-up; at-least-once delivery with consumer idempotency; late failures must not overwrite success; completion payloads are replay-only and must never re-run an external procedure.

**Q-E-01 投递保证与重试 / Delivery guarantees and retries** `P0` · `E1+E2`
- 中：出站 Webhook 的投递保证是什么？失败重试的次数、间隔与退避策略如何？超时与并发是多少？重试期间事件是否可能重复或乱序？
- EN: What are the outbound webhook delivery guarantees? What are the retry count, interval and backoff? What are the timeout and concurrency? Can events duplicate or reorder during retries?
- 替代 / Fallback：补拉作为主路径，但需确认补拉本身可靠且游标可推进。/ Make polling the primary path, confirming it is reliable and the cursor advances.

**Q-E-02 签名、时间戳与密钥轮换 / Signatures, timestamps and key rotation** `P0` · `E1+E2`
- 中：Webhook 是否带 HMAC 签名与时间戳防重放？密钥如何轮换？能否按事件类型配置不同端点？
- EN: Do webhooks carry an HMAC signature and timestamp for replay protection? How are keys rotated? Can different endpoints be configured per event type?
- 替代 / Fallback：网络层加固、IP 白名单与双向 TLS，但签名缺失仍留伪造风险。/ Harden the network, add IP allow-listing and mTLS, accepting residual forgery risk without signatures.

**Q-E-03 载荷裁剪与最小披露 / Payload trimming and minimal disclosure** `P0` · `E2`
- 中：Webhook 载荷能否裁剪，避免携带完整工单正文与敏感字段？能否自定义载荷字段？
- EN: Can the webhook payload be trimmed to avoid the full ticket body and sensitive fields? Are payload fields customizable?
- 替代 / Fallback：KAF 侧丢弃并配合 Halo 侧日志留存治理，需评估合规暴露面。/ Discard on the KAF side with Halo log-retention controls, assessing compliance exposure.

**Q-E-04 事件补拉与保留期 / Event catch-up and retention** `P0` · `E1+E2`
- 中：是否有事件日志或审计 API 支持按游标补拉错过的事件，用于 KAF 重启恢复？保留期多久？补拉是否受分页与频率限制？
- EN: Is there an event log or audit API to catch up on missed events by cursor for KAF restart recovery? How long is retention, and are there pagination or rate limits?
- 替代 / Fallback：基于 updated_at 的差异轮询，存在漏拉与重复风险。/ Poll by updated_at differences, accepting missed and duplicate risks.

**Q-E-05 死信、告警与手动重放 / Dead letter, alerting and manual replay** `P0` · `E2`
- 中：投递失败是否有死信队列与告警？能否查看失败事件并手动重放？重放是否幂等？
- EN: Is there a dead-letter queue and alerting for failed deliveries? Can failed events be inspected and manually replayed, and is replay idempotent?
- 替代 / Fallback：自定义监控加人工重放流程，需明确责任方。/ Custom monitoring with a manual replay runbook and a named owner.

**Q-E-06 重复回调去重 / Deduplication of duplicate callbacks** `P0` · `E2`
- 中：外部回调重复到达时，Halo 侧是否有去重与幂等？去重键由谁定义、保留多久？
- EN: When inbound callbacks arrive in duplicate, does Halo deduplicate and stay idempotent? Who defines the deduplication key and how long is it retained?
- 替代 / Fallback：KAF 侧强幂等加 Halo 侧只读校验，但 Halo 不保证则仍有重复副作用风险。/ Strong KAF-side idempotency with Halo read-back validation, accepting residual duplicate side effects.

**Q-E-07 单调回执 / Monotonic receipt** `P0` · `E2`
- 中：能否保证迟到的失败回报不覆盖已成功的完成回执？Halo 侧的状态迁移是否单调、不可回退？
- EN: Can a late failure report be prevented from overwriting a successful completion receipt? Are Halo status transitions monotonic and non-regressing?
- 替代 / Fallback：KAF 侧拦截加审计对账，需 Halo 书面确认状态不会回退。/ Intercept on the KAF side and reconcile via audit, requiring written confirmation that state cannot regress.

**Q-E-08 重放不产生二次副作用 / Replay without duplicate side effects** `P0` · `E2`
- 中：若要求重放同一 completion payload，Halo 是否保证不产生第二次副作用，例如重复授权、重复通知、重复 SLA 计数？副作用清单一并提供。
- EN: If the same completion payload must be replayed, does Halo guarantee no second side effect such as a duplicate grant, notification or SLA count? Provide the side-effect list.
- 替代 / Fallback：受限重放加对账，需按副作用清单逐项验证。/ Constrain replay and reconcile, verifying each item on the side-effect list.

**Q-E-09 入站回调鉴权 / Inbound callback authentication** `P0` · `E1+E2`
- 中：入站回调如何鉴权（mTLS、签名、IP 白名单、短时令牌）？未通过鉴权的回调是否 fail-closed 并审计？
- EN: How are inbound callbacks authenticated (mTLS, signature, IP allow-list, short-lived token)? Are unauthenticated callbacks rejected fail-closed and audited?
- 替代 / Fallback：网关层统一鉴权加签名校验，需 Halo 配合支持自定义校验。/ Authenticate at the gateway with signature checks, requiring Halo to support custom validation.

### F. 数据模型、字段与主数据集成 / Data Model, Fields & Master Data

> KAF 要求 / Requirement：数据结构可承载 recordClass、CTI、Catalog、CI、表单快照与关联关系；类型不可变；关系不是生命周期转换；主数据（CI、Catalog）可通过 API 读写且幂等。/ The data model must carry recordClass, CTI, catalog items, CIs, form snapshots and relations; the class is immutable; a relation is not a lifecycle conversion; master data (CIs, catalog items) is API-accessible and idempotent.

**Q-F-01 自定义字段与对象能力 / Custom fields and objects** `P0` · `E1+E3`
- 中：自定义字段、自定义对象与自定义记录类型的能力边界是什么？能否通过 API 创建与更新，字段上限、类型与索引能力如何？
- EN: What are the boundaries of custom fields, custom objects and custom record types? Can they be created and updated via API, and what are the field limits, types and indexing capabilities?
- 替代 / Fallback：用少量标准字段加外部映射存储，需评估查询与报表能力损失。/ Use a few standard fields plus an external mapping store, assessing lost query and reporting capability.

**Q-F-02 记录类型不可变 / Immutable record class** `P0` · `E2`
- 中：记录类型在创建后是否可变？能否防止通过修改类型实现生命周期转换？类型字段能否在 API 层被锁定？
- EN: Is the record class mutable after creation? Can a lifecycle conversion via class change be prevented, and can the class field be locked at the API layer?
- 替代 / Fallback：API 层禁止类型字段更新并加审计告警。/ Block class changes at the API layer with audit alerting.

**Q-F-03 关联关系 API / Relation APIs** `P0` · `E2`
- 中：关联关系（Incident 到 Problem、Request 到 Change、记录到 CI）能否通过 API 建立、查询并保持双向一致？关系类型是否可配置？
- EN: Can relations (Incident to Problem, Request to Change, record to CI) be created and queried via API with bidirectional consistency? Are relation types configurable?
- 替代 / Fallback：单向存储加 KAF 侧维护映射，一致性需对账任务保障。/ Store one direction with the mapping held by KAF, reconciling consistency by job.

**Q-F-04 CMDB 与 CI 集成 / CMDB and CI integration** `P0` · `E1+E2`
- 中：CI 的查询、创建与关联是否有 API？CI 唯一标识与幂等导入如何保证？关联关系变更是否有审计与影响面校验？
- EN: Are there APIs to query, create and relate CIs? How are unique identification and idempotent import guaranteed, and are relation changes audited and impact-checked?
- 替代 / Fallback：CI 主数据由外部 CMDB 持有，Halo 仅存引用 ID。/ Keep CI master data in an external CMDB with only reference IDs in Halo.

**Q-F-05 Catalog 发布组合校验 / Catalog publish-time validation** `P0` · `E2`
- 中：Catalog Item 发布时能否校验「表单、目标类型、流程、SLA、履约能力」组合一致性？提交与执行时能否复验？
- EN: At publish time, does Halo validate consistency across form, target class, process, SLA and fulfillment capability? Is it re-validated at submission and execution?
- 替代 / Fallback：发布前检查清单加 KAF 侧校验，易漏配需人工评审。/ Use a pre-publish checklist plus KAF validation, accepting manual review against misconfiguration.

**Q-F-06 字段上限与大数据承载 / Field limits and large payloads** `P1` · `E2`
- 中：单字段长度、单记录字段数、附件数量与总大小上限是多少？超限时返回什么错误？
- EN: What are the limits on field length, field count per record, attachment count and total size? What error is returned when exceeded?
- 替代 / Fallback：KAF 侧截断加引用外部存储，需逐字段确认上限。/ Truncate on the KAF side with external storage references, confirming each limit.

### G. 权限作用域与租户隔离 / Authorization Scope & Tenant Isolation

> KAF 要求 / Requirement：API 凭据作用域可收敛；多租户隔离 fail-closed；workspace 与 tenant 一一映射。/ API credential scope can be narrowed; multi-tenant isolation is fail-closed; workspace maps one-to-one to tenant.

**Q-G-01 API 应用权限粒度 / API application granularity** `P0` · `E1+E2`
- 中：API Application 的权限模型粒度如何？能否创建仅可读指定记录、仅可提交指定动作的受限凭据，而非全量工单读写？作用域是否可按模块、动作、字段细分？
- EN: How granular is the API application permission model? Can a credential be limited to reading specific records and submitting specific actions rather than full ticket read-write? Can scope be split by module, action and field?
- 替代 / Fallback：用中间件代理收敛权限，增加一跳与运维成本。/ Narrow rights through a middleware proxy, adding a hop and operational cost.

**Q-G-02 多租户与 MSP / Multi-tenancy and MSP** `P0` · `E1+E2`
- 中：是否原生多租户或 MSP？隔离是行级、库级还是实例级？同一凭据跨租户访问是否 fail-closed？workspace 与 tenant 如何一一映射？
- EN: Is multi-tenancy or MSP native? Is isolation row-level, database-level or instance-level? Is cross-tenant access fail-closed for a single credential, and how does workspace map to tenant?
- 替代 / Fallback：多实例部署，成本与运维复杂度显著上升需评估。/ Deploy multiple instances, assessing the significant cost and operational overhead.

**Q-G-03 跨租户访问的拒绝与审计 / Cross-tenant denial and audit** `P0` · `E2`
- 中：跨租户访问被拒时是否产生审计记录与告警？能否提供越权尝试的可见性？
- EN: Does a denied cross-tenant access produce an audit record and alert? Is visibility provided into unauthorized attempts?
- 替代 / Fallback：网关层检测加独立审计通道。/ Detect at the gateway with an independent audit channel.

**Q-G-04 行级数据范围 / Row-level data scope** `P1` · `E2`
- 中：是否支持按公司、站点、团队的行级数据范围？外部技术账号的数据范围如何限定？
- EN: Is row-level data scope supported by company, site or team? How is the data scope of an external technical account constrained?
- 替代 / Fallback：按租户拆分实例或库，代价较高。/ Split instances or databases per tenant at higher cost.

**Q-G-05 权限变更生效时效 / Permission change propagation** `P1` · `E2`
- 中：权限或角色变更后，已签发的令牌多久失效？是否存在缓存导致越权窗口？
- EN: After a permission or role change, how quickly do issued tokens lose effect? Is there a cached window that allows stale privileges?
- 替代 / Fallback：短令牌加每次请求实时校验，需 Halo 支持。/ Short-lived tokens with per-request validation, requiring Halo support.

### H. 审计、时间线与证据集成 / Audit, Timeline & Evidence

> KAF 要求 / Requirement：审计同时记录技术账号、KAF agent、Procedure、run 与 step 及结果；只保存脱敏摘要与证据引用；correlationId 用于关联，不作为绕过权限读取数据的凭据。/ Audit records the technical account, KAF agent, procedure, run and step and the outcome; only redacted summaries and evidence references are stored; the correlationId links records and is never a data-access credential.

**Q-H-01 审计字段与查询 / Audit fields and query** `P0` · `E1+E2`
- 中：审计日志覆盖哪些动作？能否记录 actor、tenant、前后状态、来源、correlationId 与客户端标识？能否通过 API 查询与导出？
- EN: Which actions are audited? Can actor, tenant, before-and-after state, source, correlationId and client identity be recorded? Can audit be queried and exported via API?
- 替代 / Fallback：自定义字段加外部审计仓，需保证不可绕过。/ Custom fields plus an external audit store, ensuring the path cannot be bypassed.

**Q-H-02 时间线自定义条目 / Custom timeline entries** `P0` · `E2+E3`
- 中：能否在记录时间线追加系统或自动化条目并与人工评论区分？外部系统以什么身份显示？能否限制为只读？
- EN: Can system or automation entries be appended to the record timeline and distinguished from human comments? Under what identity is the external system shown, and can entries be read-only?
- 替代 / Fallback：以固定技术账号发评论并加前缀标记，可伪造需权衡。/ Post as a fixed technical account with a prefix marker, accepting forgery risk.

**Q-H-03 关联标识可检索 / Searchable correlation identifiers** `P1` · `E2`
- 中：能否把外部系统的 correlationId、runId、stepId 作为可检索字段持久化并按此反查记录？
- EN: Can external correlationId, runId and stepId be persisted as searchable fields and used to look records up in reverse?
- 替代 / Fallback：存自定义字段加外部索引服务。/ Store in custom fields with an external index service.

**Q-H-04 不可篡改、导出与保留 / Immutability, export and retention** `P1` · `E1`
- 中：审计是否不可篡改？保留期多久？能否按租户隔离并导出给第三方审计？
- EN: Is the audit trail immutable? What is the retention period, and can it be tenant-isolated and exported for third-party audit?
- 替代 / Fallback：双向同步到独立审计存储，需 Halo 支持事件或批量导出。/ Sync to an independent audit store, requiring event or bulk export support.

**Q-H-05 最小披露与字段上限 / Minimal disclosure and field limits** `P1` · `E2`
- 中：是否支持只存脱敏摘要与证据引用，不存原始 prompt 与敏感工具输出？字段长度上限是多少？超长内容如何处理？
- EN: Can Halo store only redacted summaries and evidence references without raw prompts or sensitive tool output? What are the field length limits and how is oversized content handled?
- 替代 / Fallback：KAF 侧截断加限制符，需逐字段确认上限。/ Truncate on the KAF side with explicit limits, confirming each field.

**Q-H-06 日志脱敏 / Log redaction** `P0` · `E1`
- 中：接口日志与 trace 中的 token、密码、密钥如何遮蔽？能否提供日志治理说明与关闭详单日志的选项？
- EN: How are tokens, passwords and secrets redacted in API logs and traces? Can a log-governance statement be provided along with an option to disable verbose logging?
- 替代 / Fallback：关闭详单日志加网络层旁路采集，需 Halo 支持。/ Disable verbose logging and capture at the network layer, requiring Halo support.

### I. 附件与敏感数据 / Attachments & Sensitive Data

> KAF 要求 / Requirement：附件引用不可猜测、需鉴权；跨租户 fail-closed；支持保留与删除策略。/ Attachment references are unguessable and authenticated; cross-tenant access is fail-closed; retention and deletion policies are supported.

**Q-I-01 附件引用安全 / Attachment reference safety** `P1` · `E2`
- 中：附件如何被引用（ID 还是 URL）？是否不可猜测、需鉴权、可设过期？能否只返回附件 ID 与名称，不暴露存储路径或签名 URL？
- EN: How are attachments referenced (ID or URL)? Are references unguessable, authenticated and expirable? Can the API return only the attachment ID and name without storage paths or signed URLs?
- 替代 / Fallback：仅存附件 ID 并通过 API 代理下载。/ Store only the attachment ID and download through an API proxy.

**Q-I-02 上传限制与杀毒 / Upload limits and antivirus** `P0` · `E1+E2`
- 中：上传的大小与类型限制是什么？是否进行杀毒扫描？扫描状态是否可查询？
- EN: What are the upload size and type limits? Is antivirus scanning performed and is the scan status queryable?
- 替代 / Fallback：上传前 KAF 侧扫描加白名单校验。/ Scan and allow-list on the KAF side before upload.

**Q-I-03 跨租户附件隔离 / Cross-tenant attachment isolation** `P0` · `E2`
- 中：附件跨租户访问是否 fail-closed？共享或转发的附件如何控制权限？
- EN: Is cross-tenant attachment access fail-closed? How are permissions controlled for shared or forwarded attachments?
- 替代 / Fallback：独立附件服务加显式授权，改造量需评估。/ Use a separate attachment service with explicit grants, assessing the build effort.

**Q-I-04 保留与删除 / Retention and deletion** `P0` · `E1`
- 中：数据保留与删除（GDPR 被遗忘权、PIPL 最小化）如何支持？能否按租户配置保留期？删除是否覆盖附件、审计与备份？
- EN: How are data retention and deletion supported (GDPR right to erasure, PIPL minimisation)? Can retention be configured per tenant, and does deletion cover attachments, audit and backups?
- 替代 / Fallback：独立归档与删除流程，需法务确认责任边界。/ A separate archive and deletion process with legal confirmation of responsibility.

### J. 扩展点与 fail-closed 分发 / Extensibility & Fail-closed Dispatch

> KAF 要求 / Requirement：优先配置化而非硬编码；扩展点可治理；未知动作或未知类型必须 fail-closed。/ Prefer configuration over hard-coding; extension points are governed; unknown actions or types must fail closed.

**Q-J-01 脚本与自定义扩展 / Scripting and custom extensions** `P1` · `E1+E3`
- 中：是否支持脚本扩展（PowerShell、Python、JavaScript）？运行沙箱、超时、权限与审计限制是什么？
- EN: Is script-based extension supported (PowerShell, Python, JavaScript)? What are the sandbox, timeout, permission and audit restrictions?
- 替代 / Fallback：外部中间件承担扩展逻辑，需保证不绕过领域规则。/ Put extension logic in external middleware, ensuring domain rules are not bypassed.

**Q-J-02 自定义 API 端点与版本 / Custom API endpoints and versioning** `P1` · `E1`
- 中：能否新增自定义 API 端点？其版本管理、鉴权与升级兼容如何保证？
- EN: Can custom API endpoints be added? How are their versioning, authentication and upgrade compatibility guaranteed?
- 替代 / Fallback：自建集成层统一暴露稳定契约。/ Expose a stable contract from a self-built integration layer.

**Q-J-03 未知动作 fail-closed 统一契约 / Unified fail-closed contract for unknown actions** `P0` · `E2`
- 中：扩展点与流程调用遇到未知动作或未知类型时的统一行为是什么？是否有统一契约覆盖流程、Automation、Webhook 与脚本？
- EN: What is the unified behavior at extension points and process calls when an action or type is unknown? Is one contract applied across workflow, automation, webhooks and scripts?
- 替代 / Fallback：逐面确认并在 KAF 侧补探测，需登记为架构风险。/ Confirm per surface and add KAF-side probing, registering it as an architectural risk.

**Q-J-04 连接器与入站扩展治理 / Connector and inbound extension governance** `P1` · `E1+E2`
- 中：连接器或入站扩展是否有生命周期（安装、启停、回滚）、密钥管理、健康检查与审计？新增连接器是否需要修改核心代码？
- EN: Do connectors or inbound extensions have a lifecycle (install, enable, disable, roll back), secret management, health checks and audit? Does adding a connector require core code changes?
- 替代 / Fallback：自建连接器并在网关层统一治理。/ Build connectors in-house with governance at the gateway.

**Q-J-05 升级兼容承诺 / Upgrade compatibility commitment** `P0` · `E1`
- 中：二次开发与自定义扩展在升级时如何保持兼容？是否有扩展 API 的稳定性承诺与弃用周期？
- EN: How do customizations and extensions stay compatible across upgrades? Is there a stability commitment and deprecation window for extension APIs?
- 替代 / Fallback：锁定版本加升级即回归，需评估长期成本。/ Pin the version with a full regression on upgrade, assessing long-term cost.

### K. API 契约治理、环境与可观测性 / API Governance, Environment & Observability

> KAF 要求 / Requirement：接口契约稳定、可版本化；有沙箱可实测；限流与可观测性满足高频事件与排障需要。/ The interface contract is stable and versioned; a sandbox is available for real testing; rate limits and observability support high-frequency events and troubleshooting.

**Q-K-01 API 版本策略与弃用 / API versioning and deprecation** `P0` · `E1`
- 中：API 版本策略是什么？兼容承诺与弃用通知周期多久？Webhook 载荷是否版本化？破坏性变更如何通知？
- EN: What is the API versioning policy? What are the compatibility commitment and deprecation notice period? Is the webhook payload versioned, and how are breaking changes announced?
- 替代 / Fallback：锁定版本加回归测试，需 Halo 提前通知。/ Pin the version with regression tests and require advance notice.

**Q-K-02 限流、配额与容量 / Rate limits, quotas and capacity** `P0` · `E2`
- 中：API 限流与配额是多少？吞吐与延迟指标如何？高频补拉与批量操作是否受限？超限返回什么？
- EN: What are the API rate limits and quotas? What are the throughput and latency figures? Are high-frequency catch-up and bulk operations throttled, and what is returned when limits are exceeded?
- 替代 / Fallback：批量接口加退避策略，需确认容量上限。/ Use bulk endpoints with backoff, confirming the capacity ceiling.

**Q-K-03 沙箱环境与数据 / Sandbox environment and data** `P0` · `E2`
- 中：PoC 使用哪个环境？能否提供独立沙箱、测试数据与重置能力？沙箱与生产的版本差异如何管理？
- EN: Which environment is used for the PoC? Can an isolated sandbox, test data and reset capability be provided? How is the version gap between sandbox and production managed?
- 替代 / Fallback：私有部署自建 PoC 环境，周期更长。/ Build a private PoC environment, accepting a longer lead time.

**Q-K-04 可观测性与排障 / Observability and troubleshooting** `P0` · `E2`
- 中：能否向双方提供 API 调用日志与 Webhook 投递日志用于排障？是否有健康检查端点与投递状态查询接口？
- EN: Can API call logs and webhook delivery logs be shared with both parties for troubleshooting? Are there health-check endpoints and delivery-status query APIs?
- 替代 / Fallback：双方各自埋点加定期对账，排障效率下降。/ Instrument on both sides with periodic reconciliation, accepting slower diagnosis.

**Q-K-05 错误码目录与引用 / Error catalogue and reference** `P0` · `E1`
- 中：是否有稳定的错误码目录与结构化错误格式？能否提供 OpenAPI 定义、Postman 集合与示例代码？
- EN: Is there a stable error catalogue and structured error format? Can the OpenAPI definition, a Postman collection and sample code be provided?
- 替代 / Fallback：基于实测反向整理契约，需投入联调工时。/ Reverse-engineer the contract from testing, requiring integration effort.

### L. 部署形态与网络集成 / Deployment & Network Integration

> KAF 要求 / Requirement：部署形态可选（SaaS 与私有）；区域与数据驻留可控；网络与密钥可达且可管理。/ Deployment options include SaaS and private; region and data residency are controllable; network and secrets are reachable and manageable.

**Q-L-01 部署形态与 PoC 环境 / Deployment options and PoC environment** `P0` · `E1`
- 中：支持哪些部署形态（SaaS、私有部署、混合）？PoC 采用哪一种？私有部署的架构图、依赖清单、容器化（K8s 或 Docker）与离线安装能力如何？
- EN: Which deployment options are supported (SaaS, private, hybrid) and which one is used for the PoC? For private deployment, what are the architecture diagram, dependency list, containerization (K8s or Docker) and offline install capability?
- 替代 / Fallback：传统虚拟机部署，需评估运维与升级成本。/ Traditional VM deployment, assessing operations and upgrade cost.

**Q-L-02 区域与数据驻留 / Region and data residency** `P0` · `E1`
- 中：SaaS 有哪些区域节点？能否与 KAF 同区域部署？数据驻留、跨境传输与备份位置如何约定？中国区可用性如何（是否兼容国内云与信创环境）？
- EN: Which regional nodes exist for SaaS? Can it be deployed in the same region as KAF? How are data residency, cross-border transfer and backup location handled? What is the China availability, including domestic cloud and Xinchuang compatibility?
- 替代 / Fallback：区域独立实例加数据同步策略，需评估一致性。/ Per-region instances with a data sync strategy, assessing consistency.

**Q-L-03 网络接入与出站回调 / Network access and outbound callbacks** `P0` · `E1+E2`
- 中：网络接入方式有哪些（IP 白名单、双向 TLS、专线、代理）？从 Halo 到 KAF 的出站回调是否支持自定义域名、端口与证书？是否支持私有网络内网互通？
- EN: What network access methods are supported (IP allow-list, mTLS, private link, proxy)? Do outbound callbacks from Halo to KAF support custom domains, ports and certificates? Is private-network connectivity supported?
- 替代 / Fallback：经网关中转并统一鉴权，增加一跳。/ Relay through a gateway with centralized auth, adding a hop.

**Q-L-04 密钥管理与轮换 / Secret management and rotation** `P0` · `E1`
- 中：密钥如何存储（Vault、HSM、KMS）？能否不落盘、支持运行时轮换与最小权限？Webhook 签名密钥与 API 客户端密钥是否可以分离？
- EN: How are secrets stored (Vault, HSM, KMS)? Can they avoid disk persistence and support runtime rotation with least privilege? Can the webhook signing key be separated from the API client secret?
- 替代 / Fallback：外部密钥服务加短期凭据，需 Halo 支持动态获取。/ External secret service with short-lived credentials, requiring dynamic retrieval support.

**Q-L-05 可用性、灾备与升级窗口 / Availability, DR and upgrade windows** `P0` · `E1`
- 中：可用性 SLA 是多少？灾备的 RTO 与 RPO 如何？升级节奏与窗口如何安排？升级是否影响在途流程与自定义扩展？
- EN: What is the availability SLA? What are the DR RTO and RPO? How are upgrade cadence and windows scheduled, and do upgrades affect in-flight processes and customizations?
- 替代 / Fallback：约定变更冻结窗口与升级回归清单。/ Agree change-freeze windows and an upgrade regression checklist.

**Q-L-06 安全能力与认证报告 / Security capabilities and certifications** `P0` · `E1`
- 中：能否提供渗透测试报告、合规证书（ISO 27001、SOC 2）、GDPR 与 PIPL 说明、DPA 与子处理者清单？审计日志可否导出给第三方审计？
- EN: Can penetration-test reports, compliance certifications (ISO 27001, SOC 2), GDPR and PIPL statements, a DPA and a sub-processor list be provided? Can audit logs be exported for third-party audit?
- 替代 / Fallback：补充合同条款加第三方评估，需法务与安全介入。/ Add contractual clauses and a third-party assessment with legal and security involvement.

### M. 通知与渠道集成 / Notification & Channel Integration

> KAF 要求 / Requirement：ITSM 是用户通知的权威来源；通知只含服务阶段、下一步与可见摘要；中国区渠道可用。/ ITSM is the authoritative source of user notifications; notifications contain only the service stage, next step and a visible summary; China channels are available.

**Q-M-01 支持渠道 / Supported channels** `P1` · `E1+E3`
- 中：支持哪些通知渠道（邮件、Teams、Slack、站内、移动推送）？能否按租户与事件类型配置？
- EN: Which notification channels are supported (email, Teams, Slack, in-app, mobile push)? Can they be configured per tenant and event type?
- 替代 / Fallback：KAF 承担部分渠道投递，需明确分工与权威来源。/ Have KAF deliver some channels, defining ownership and the authoritative source.

**Q-M-02 中国区渠道 / China channels** `P0` · `E1+E3`
- 中：是否支持企业微信（WeCom）、钉钉（DingTalk）、飞书（Feishu）？原生支持还是需自建连接器？若自建，是否有连接器扩展点、密钥治理与审计？
- EN: Are WeCom, DingTalk and Feishu supported? Natively or through a self-built connector? If self-built, are there connector extension points, secret governance and auditing?
- 替代 / Fallback：自建连接器并由 KAF 投递，需 Halo 开放事件与回调能力。/ Build connectors in-house with delivery by KAF, requiring open events and callbacks from Halo.

**Q-M-03 通知内容与最小披露 / Notification content and minimal disclosure** `P1` · `E2`
- 中：通知模板能否按租户、语言与渠道配置？能否只发送服务阶段、下一步与可见摘要，不带内部细节？失败是否重试与去重？
- EN: Can notification templates be configured per tenant, language and channel? Can they send only the service stage, next step and a visible summary without internal detail? Are failures retried and deduplicated?
- 替代 / Fallback：KAF 侧组装摘要，Halo 仅发送最小通知。/ Assemble the summary on the KAF side and send only minimal notifications from Halo.

**Q-M-04 原渠道回帖 / Posting back to the origin channel** `P2` · `E3`
- 中：外部系统能否以原渠道会话回帖，例如 KAF 向 Teams 或 WeCom 原会话发送进度？是否需要消息 ID 映射？
- EN: Can an external system post back into the origin channel conversation, for example KAF sending progress into the original Teams or WeCom thread? Is a message-ID mapping required?
- 替代 / Fallback：KAF 直接调用渠道 API，Halo 仅提供事件。/ Call the channel APIs directly from KAF with Halo providing only events.

---

## 5. 集成 Go/No-Go 判定矩阵 / Integration Go/No-Go Matrix

### 5.1 P0 集成分门槛 / P0 integration gates

| # | 门槛 Gate | 关联问题 Refs |
|---|---|---|
| 1 | 流程可设置外部委派等待点，支持回调恢复、超时与失败分支 / The process supports an external delegation wait point with callback resume, timeout and failure branching | Q-C-01 / Q-C-02 / Q-C-05 |
| 2 | 未知动作与未知类型 fail-closed，不得静默跳过 / Unknown actions and types fail closed instead of silently skipping | Q-C-03 / Q-C-04 / Q-C-09 / Q-J-03 |
| 3 | 任务范围 API、乐观锁、动作白名单与结构化拒绝码 / Task-scoped API, optimistic locking, action allow-list and structured rejection codes | Q-D-01 / Q-D-02 / Q-D-03 / Q-D-04 / Q-D-05 / Q-D-06 |
| 4 | 事件可靠投递、签名、补拉、去重、单调回执与无二次副作用 / Reliable delivery, signatures, catch-up, deduplication, monotonic receipts and no duplicate side effects | Q-E-01 / Q-E-02 / Q-E-04 / Q-E-06 / Q-E-07 / Q-E-08 |
| 5 | 身份由认证上下文派生；API 凭据可收敛；多租户 fail-closed / Identity derived from context; credential scope narrowable; multi-tenant fail-closed | Q-A-01 / Q-A-02 / Q-A-03 / Q-G-01 / Q-G-02 |
| 6 | 服务端状态机权威、终态守卫、审计身份区分与 correlationId 贯穿 / Server-side state machine authority, terminal guards, audit identity separation and correlationId threading | Q-D-08 / Q-F-02 / Q-H-01 / Q-H-02 / Q-H-06 |
| 7 | 数据模型可承载 recordClass、关系、CMDB 与 Catalog 组合校验 / The data model carries recordClass, relations and CMDB and catalog consistency checks | Q-F-01 / Q-F-02 / Q-F-03 / Q-F-04 / Q-F-05 |
| 8 | 部署与接入可达：环境、区域、网络、密钥、限流与可观测性 / Deployment and connectivity: environment, region, network, secrets, rate limits and observability | Q-K-02 / Q-K-03 / Q-K-04 / Q-L-01 / Q-L-03 / Q-L-04 |

### 5.2 判定规则 / Decision rules

| 结论 Verdict | 条件 Condition |
|---|---|
| Go | 全部 P0 满足，且关键项具备沙箱证据 E2 / All P0 gates met with E2 sandbox evidence on the critical items |
| 有条件 Go / Conditional Go | P0 中不超过 2 项为部分满足，且 Halo 出具书面集成方案、责任人与时间表 / No more than two P0 gates partially met, with a written integration plan, owners and timeline from Halo |
| No-Go | 任一 P0 为不满足且无可行替代，或 Halo 无法承诺排期 / Any P0 gate not met without a viable fallback, or no committed timeline from Halo |

### 5.3 PoC 集成验收场景 / PoC integration acceptance scenarios

| # | 场景 Scenario |
|---|---|
| 1 | 主路径：KAF 受理、建单、两级审批、委派、外部执行、回执闭环 / Happy path: intake, creation, two-level approval, delegation, external execution, closed loop |
| 2 | B1 重放：同一事件或动作重放，副作用基数保持为一 / Replay: duplicate events or actions keep the side-effect count at one |
| 3 | B2 迟到失败：不覆盖已成功回执，状态不回退 / Late failure: no overwrite of the success receipt, no state regression |
| 4 | B3 陈旧版本：返回结构化 stale_version 拒绝码 / Stale version: a structured stale_version rejection is returned |
| 5 | B4 越权：错误租户或身份凭据被 fail-closed 拒绝并产生审计 / Unauthorized: wrong tenant or identity rejected fail-closed with audit |
| 6 | B5 外部失败：流程进入明确失败或人工介入分支并可观测 / External failure: the flow enters an explicit failure or manual branch with observability |
| 7 | B6 审批拒绝：不产生委派任务与授权结果 / Approval rejection: no delegation task and no access grant |
| 8 | B7 超时：按配置的超时分支处理并告警 / Timeout: handled by the configured branch with alerting |

---

## 6. 待索取集成材料 / Integration Materials to Request

| 类别 Category | 材料 Material | 责任方 Owner | 截止 Due |
|---|---|---|---|
| 接口 API | OpenAPI 或 Swagger 定义、Postman 集合、鉴权说明 / OpenAPI or Swagger definition, Postman collection, authentication guide | Halo | |
| 事件 Events | Webhook 事件清单、载荷示例、签名与重试说明、事件补拉 API / Webhook event list, payload samples, signature and retry specification, catch-up API | Halo | |
| 流程 Workflow | Workflow 与 Automation 引擎能力说明（外部服务任务、等待、回调、超时、fail-closed）/ Workflow and automation capability statement (external service task, wait, callback, timeout, fail-closed) | Halo | |
| 权限 Permissions | API Application 权限模型、作用域与多租户或 MSP 架构说明 / API permission model, scopes and multi-tenant or MSP architecture | Halo | |
| 数据 Data | 自定义字段与对象能力、CMDB 与 CI 接口、Catalog 发布校验说明 / Custom field and object capabilities, CMDB and CI APIs, catalog publish validation | Halo | |
| 部署 Deployment | 部署架构图、区域与数据驻留清单、私有部署要求、网络接入方式 / Deployment architecture, region and residency list, private deployment requirements, network access methods | Halo | |
| 合规 Compliance | ISO 27001 或 SOC 2 证书、GDPR 与 PIPL 说明、DPA、子处理者清单 / ISO 27001 or SOC 2 certificates, GDPR and PIPL statements, DPA, sub-processor list | Halo | |
| 环境 Environment | PoC 沙箱账号、测试数据、重置方式 / PoC sandbox credentials, test data, reset method | Halo | |
| 运维 Operations | API 版本策略、限流与配额、可用性 SLA、升级策略 / API versioning policy, rate limits and quotas, availability SLA, upgrade policy | Halo | |
| 案例 References | 具备外部系统或 Agent 委派集成经验的参考客户 / Reference customers with external system or agent delegation integration | Halo | |
| 缺口 Gaps | 本次识别出的能力缺口的书面方案与排期承诺 / Written remediation plan and committed dates for identified gaps | Halo | |

---

## 7. 非集成类补充（次要）/ Non-Integration Supplementary

> 以下不属于集成能力主线，供会上快速确认，不作为 Go/No-Go 硬门槛。/ These are outside the integration scope; confirm briefly if time permits. They are not Go/No-Go gates.

| 编号 Ref | 中文 | English | 优先级 Priority |
|---|---|---|---|
| Q-N-01 | 自助门户能否展示服务阶段、下一步、SLA 与审批结果，且映射由后端配置？ | Can the self-service portal show service stage, next step, SLA and approval outcome with back-end configured mapping? | P1 |
| Q-N-02 | 申请人可见信息与坐席可见信息能否隔离，不暴露内部 Procedure、taskId 与审计引用？ | Can requester-visible and agent-visible information be separated, hiding internal procedures, taskIds and audit references? | P1 |
| Q-N-03 | 界面支持哪些语言？能否按用户切换中英文与简繁？ | Which UI languages are supported, and can users switch between Chinese and English? | P1 |
| Q-N-04 | 许可与计费模型是否按区域、坐席数或租户计算？PoC 是否收费？ | Is licensing per region, seat or tenant? Is the PoC chargeable? | P2 |
| Q-N-05 | PoC 的成功标准、双方责任人与退出条件由谁定义？支持模式与响应时效如何？ | Who defines PoC success criteria, owners and exit conditions, and what are the support model and response times? | P1 |

注 / Note：Q-N-05 属于商务与交付范畴，不计入集成 Go/No-Go 门槛，但建议在会议结束前明确。/ Q-N-05 is commercial and delivery scope, not an integration gate, but should be clarified before the meeting ends.

---

## 附录 A. 接口契约摘要（供 Halo 对照）/ Appendix A: Interface Contract Summary

### A.1 统一建单 / Unified creation

```ts
type CreateWorkItemCommand = {
  idempotencyKey: string
  source: "kaf_web" | "itsm_web" | "teams" | "wecom"
  confirmation: "confirmed" | "channel_auto_create"
  intent: {
    recordClass: WorkItemRecordClass
    cti: { categoryId?: string; typeId?: string; itemId?: string }
    catalogItemId?: string
    ciIds?: string[]
  }
  content: {
    title: string
    description: string
    formData: Record<string, unknown>
    attachmentRefs?: AttachmentRef[]
  }
  aiDecision?: {
    confidence: number
    model: string
    promptVersion: string
    rationale: string
    conversationRef?: string
  }
}
```

返回需包含 / Response must include：`id`、`number`、`recordClass`、`status`、CTI、Catalog、流程与 SLA 摘要、`version` 与详情链接。/ `id`, `number`, `recordClass`, `status`, CTI, catalog, process and SLA summary, `version` and a deep link.
requester、tenant 与 actor 由认证上下文派生，不接受调用方任意指定。/ requester, tenant and actor are derived from the authenticated context and cannot be set freely by the caller.

### A.2 委派事件 / Delegation event

```ts
type KafDelegateRequested = {
  eventId: string
  tenantId: string
  workItemId: string
  ticketId: string
  taskId: string
  recordClass: WorkItemRecordClass
  actor: { id: string; kind: "system"; displayName: string }
  timestamp: string
  version: number
  correlationId: string
}
```

事件推送为主路径；KAF 重启或事件遗漏时通过补拉接口获取未完成任务。/ Push is the primary path; after a KAF restart or a missed event, unfinished tasks are collected through the catch-up API.

### A.3 任务范围 API / Task-scoped API

- `GET /bpmn/process-tasks/{taskId}/kaf-context`：返回关联记录、冻结受理快照、当前等待点、允许动作与当前版本。/ Returns the related record, frozen intake snapshot, current wait point, allowed actions and current version.
- `POST /bpmn/process-tasks/{taskId}/actions`：提交 typed action；完成动作成功时推进流程。/ Submits a typed action and advances the process when the completion action succeeds.

授权要求 / Authorization：按 taskId 读取任务，再校验技术账号角色、任务租户、任务类型与等待状态；不接受「通用权限即可」的实现。/ Read the task by taskId, then verify the technical account role, task tenant, task type and wait state; a generic-permission implementation is not acceptable.

### A.4 Typed action 基础结构 / Typed action base

```ts
type AutomationActionBase = {
  taskId: string
  workItemId: string
  expectedVersion: number
  execution: {
    procedureRef: string
    procedureVersion: string
    runId: string
    stepId: string
    idempotencyKey: string
    correlationId: string
  }
}
```

动作集合 / Actions：`complete_bpmn_task`、`update_progress`、`record_execution_failure`、`assign`、`resolve`、`close`。
结果码 / Result codes：`applied`、`already_applied`、`stale_version`、`task_not_active`、`forbidden`、`domain_rejected`。

### A.5 可靠性语义 / Reliability semantics

- 幂等边界 / Idempotency boundary：`tenantId + taskId + runId + stepId`。
- 事件投递为 at-least-once，消费端必须幂等。/ Delivery is at-least-once; consumers must be idempotent.
- 完成回执单调，迟到的失败不得覆盖已成功的完成回执。/ Completion receipts are monotonic; a late failure must not overwrite a success.
- completion payload 仅可重放，不得因重放重新调用外部 Procedure 或 Tool。/ Completion payloads are replay-only and must not trigger a new external call.
- ITSM 只保存脱敏摘要与证据引用，不保存原始 prompt、完整对话或敏感工具输出。/ ITSM stores only redacted summaries and evidence references, never raw prompts, full conversations or sensitive tool output.

---

## 附录 B. 会议记录模板 / Appendix B: Meeting Record Template

| 编号 Ref | 问题摘要 Question | Halo 答复 Answer | 证据等级 Evidence | 判定 Verdict | 责任方 Owner | 截止 Due |
|---|---|---|---|---|---|---|
| Q-C-01 | 外部委派等待点 / External delegation wait point | | | | | |
| Q-C-04 | 未注册动作 fail-closed / Fail-closed on unknown actions | | | | | |
| Q-D-03 | 乐观锁 / Optimistic locking | | | | | |
| Q-E-08 | 重放不产生二次副作用 / No duplicate side effects on replay | | | | | |
| Q-G-02 | 多租户与 MSP / Multi-tenancy and MSP | | | | | |
| | | | | | | |

备注 / Notes：
1. 所有 E5（口头承诺）结论必须在会后 3 个工作日内转书面确认。/ Every E5 conclusion must be confirmed in writing within three working days.
2. 会议结束时需明确：P0 门槛初步结论、待验证清单、待索取材料与责任人。/ Close the meeting with preliminary P0 verdicts, a to-verify list, requested materials and owners.

---

## 附录 C. 参考依据（KAF 侧现有设计与证据）/ Appendix C: References

| 主题 Topic | 文档 Document |
|---|---|
| KAF 自主受理与 BPMN 委派设计 / KAF autonomous intake and BPMN delegation design | `docs/superpowers/specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md` |
| SSLVPN KAF 对话受理与授权闭环 / SSLVPN KAF intake and access-grant loop | `docs/superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md` |
| 委派权限开通验证完成契约 / Delegated access verification contract | `docs/contracts/kaf-verified-access-completion.md` |
| 委派执行完整性验收报告 / Delegation execution integrity report | `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md` |
| 架构与产品评估（含 KAF 委派面）/ Architecture and product assessment | `docs/architecture-product-assessment-2026-09-01.md` |
| 旧 ITSM 到新 ITSM 功能对标（业务背景）/ Legacy-to-new ITSM benchmark | `prd/嘉里大通功能对标分析.md` |
