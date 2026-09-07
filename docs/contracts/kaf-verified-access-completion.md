# 委派权限开通的验证完成契约

本契约对应 C2。实现验证使用模拟 Graph 和独占数据库；不表示 C3 崩溃矩阵、完整用户展示或 C4 真实 Graph 演练已完成。

## 权威边界

Service Request 保存并投影获批快照和实际流程审批决定。KAF 使用认证任务上下文的 provider、externalSystem、subjectId、groupId 及审批证据；表单字段、Procedure 文本和单独的 itsm_approval 字符串不能授予执行权限。当前注册能力 external_group_grant 使用 Graph 后端的现有 ad_grant_vpn_access 工具。没有 LDAP 回退，未配置或不匹配的 Procedure/工具拒绝执行。

Graph 先查询精确成员关系。已存在成员返回 already_present，不写入、不推断初始授权时间，也不创建本系统管理的到期时间。非成员在当前租约下持久化 write_pending 后才提交一次添加请求，再持久化 verification_pending 并重新查询成员关系。只有查询确认成功才产生 C1 的 AccessGrantResult，时间字段仅为 verifiedAt。写请求不做传输层自动重试；写入超时、冲突或验证失败均不能伪装成成功。

ITSM 在原 BPMN 完成事务内校验任务、租户、认证 actor、现有 action ledger、审批快照、目标和 evidenceRef，写入不可变专业结果、到期时间、Requested Item 状态、审计和原 completion receipt，并保留最终租约 fencing。任一写入或最终租约检查失败，全事务回滚。重放保持首次 verifiedAt 和 expiresAt；同一 action key 改目标、时间或 expectedVersion 明确冲突。

手工 provisioning 的任务创建和旧任务执行均调用 Service Request owner。具有冻结权限快照、或具有权限策略却缺少快照的请求不能从该入口交付；启动时缺少 owner 也拒绝执行。普通请求继续使用其既有 provisioning 行为。

## 双级审批与拒绝分支

正式定义由 BPMNTemplateService 部署 service/bpmn/sslvpn_approval_flow.bpmn。模板 1.1.0 在主管初审和网络运维复审之后分别使用原引擎的条件网关：仅 approvalResult=approved 进入下一级或 KAF 委派，rejected 进入 EndEvent_Reject。两级分别使用 dept_manager 和 network_eng 候选组；任务 API 校验实际任务身份、当前状态和审批人，重复决定不能额外推进。

拒绝的专业状态由 Service Request 的既有审批决定投影为 rejected；不会产生 KAF 任务、委派事件或开通结果，也不会填写 Requested Item 的交付完成时间。流程到达拒绝终点的 completed 表示审批流程结束，不代表请求已交付。共享 WorkItem 状态不承担第二份审批状态。

tests/e2e/sslvpn_approval_rejection_test.go 使用正式嵌入模板、真实创建请求及审批 REST 操作、不同角色的认证会话和现有 outbox dispatcher，覆盖任一级拒绝、错误审批人、提前第二级及重复审批。接收端仅为测试内 HTTP mock；双通过恰好一次委派作为零发送断言的正向对照。

旧模板 1.0.0 缺少拒绝分支，不能作为安全审批基线。发布 owner 必须通过原 BPMNTemplateService 的定义同步机制发布更新并核对新请求绑定的定义内容；内容变化由现有 owner 发布新定义版本。源码修复不会更改已部署定义或运行中的旧实例。启用消费者前，应单独核对旧实例的定义、审批决定及现有委派记录，按既有受控运维流程处置；不得因流程 completed 或缺少新分支而推断已获授权，也不得重放授权来补证。本次测试没有更新任何 live 定义或实例。

## 同批次升级要求

两端应用和数据库必须作为同一发布批次升级，在恢复委派消费者前完成：

- ITSM 正式 InitializeStorage 执行 031_kaf_action_request_digest，保留 030 和其启动不变量修复阶段。Ent 产物必须与 schema 同步；不能只执行 Schema.Create 代替正式初始化。
- KAF 使用正式 acp.infrastructure.schema_management 从支持的历史基线升级至 038_kaf_execution_phase，并通过只读 application/checkpoint schema 校验。已有 037 运行环境也须正式升级；ORM-only 单测库或 stamp 到 head 均不能作为升级证据。
- KAF workspace 配置、Procedure 内容版本和 registry 能力必须匹配后才能启用委派执行；Graph 配置和凭据由部署 owner 管理。

初始“无需 schema 改动”的假设已被当前源码证据修正：原 action ledger 没有绑定完整结果的摘要，原 delivery.last_error 也不是安全的执行状态。必要字段仍扩展原有两张记录，不新建审批、执行或结果 ledger。

031 的历史空摘要保留为空；既有非 access 动作保留原重放规则。历史权限动作缺少摘要或已完成任务缺少原子保存的 verified result，不得被视为成功，也不能用新回执覆盖旧状态，须人工核对。

038 对已有 completion_payload 的行回填 completion_persisted，保留原 payload。仅 received 且从未 started 的行设 not_started。其他缺 payload 的历史行设 legacy_unknown，其中可再次执行的 running/retryable/failed_auth/received 转 execution_unknown。没有历史证据时不得假造 verifiedAt 或自动再授权。未知状态须由后续受控核对处理，不提供强制重发。

## 重放与恢复

completion_payload 始终优先，保存与 completion_persisted 同次提交。领取、重试、认证失败及诊断文本更新不得回退 execution_phase。已进入 write_pending、verification_pending、legacy_unknown 或未知阶段却无 payload 的记录不能再次调用 Procedure；即使重领后再次崩溃也一样。完成请求丢失响应时重放同一 payload，不重新查询 Graph、不改首次验证时间。

不得在不核对这些记录的情况下回退到不理解 execution_phase 的旧消费者；038 的自动 downgrade 被拒绝。维护期间应保留现有回执、action ledger、租约与诊断证据。完整恢复演练、用户界面和真实受控开通/移除由 C3/C4 继续验证。

固定真实演练对象、ad_grant_vpn_access 和 remove_vpn_access 的范围见 [发布收口夹具](../testing/kaf-delegation-release-closeout-fixture.md)。本契约没有执行该夹具。
