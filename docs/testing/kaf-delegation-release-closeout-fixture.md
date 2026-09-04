# KAF 委派发布收口真实变更夹具

> 状态：已纳入 [SSLVPN 场景生产化与 KAF Worker 收敛设计](../superpowers/specs/2026-09-03-sslvpn-worker-production-readiness-design.md) 的第一阶段真实演练；仅在该 Runbook 的逐项 Go/No-Go 条件满足后执行
> 日期：2026-08-31
> 范围：KAF → Microsoft Graph → Azure AD Security Group 的真实成员变更

## 1. 授权边界

本轮发布收口允许在当前机器的 KAF Dev 环境读写，并允许执行真实 Azure AD
组成员变更。禁止访问 KAF PROD `10.128.35.195`，禁止使用生产凭据，禁止调用
`ldap_grant_vpn_access`、`ldap_revoke_vpn_access` 或任何 LDAP 连接。

本夹具只固定测试对象和恢复约束，不替代发布收口设计、实施计划或证据报告。

## 2. 固定测试对象

| 项目 | 值 |
|---|---|
| 目标用户 UPN | `Julian@dawnpro.onmicrosoft.com` |
| Azure AD Security Group Object ID | `b7c7f066-3042-4a11-9e36-2ea80b979ae3` |
| KAF 授权 Tool | `ad_grant_vpn_access` |
| KAF 恢复 Tool | `remove_vpn_access` |
| KAF 后端 | `IT_BACKEND=graph` |
| KAF 组配置 | `VPN_USERS_GROUP_ID=b7c7f066-3042-4a11-9e36-2ea80b979ae3` |

Group Object ID 不是凭据，可以进入测试文档。Azure client secret、KAF automation
JWT 和 webhook secret 不得写入本文、日志、截图或验收报告。

## 3. 已知状态与执行前判定

用户已确认：

- Julian 在手工验证前不是该组成员；
- 手工将 Julian 加入该 Azure AD 组已经成功。

“手工加入测试成功”不能说明正式 KAF 场景开始时 Julian 仍是成员或已被移除。
因此不需要用户凭记忆回答当前状态：执行前由验收程序通过 Microsoft Graph 做只读
查询并保存证据。

- 如果 Julian 当前不是成员，直接进入正式 KAF 授权场景。
- 如果 Julian 当前是成员，先通过受控恢复步骤移除，再只读确认非成员；没有建立
  非成员基线前，不得开始正式授权场景。

## 4. 恢复约束

正式验收必须形成以下闭环：

1. 只读记录执行前成员状态，正式授权的基线必须是非成员。
2. KAF 委派链路调用 `ad_grant_vpn_access`，成员状态变为已加入。
3. 重放同一委派/action payload，证明不会产生第二次 Graph 副作用。
4. 收集 ITSM、KAF、BPMN、action ledger、timeline 和 audit 证据。
5. 调用 `remove_vpn_access`，只读确认 Julian 恢复为非成员。

恢复失败视为发布收口失败：立即停止后续真实变更测试，保留证据并人工修复，不能以
“业务链路已通过”覆盖未恢复的外部权限状态。
