# 配置迁移审查修复与隔离重放证据

日期：2026-09-14。状态：工具重放通过，独立复审通过，功能收口进行中；**G-B 仍为 BLOCKED**。

## 固定修订与边界

- 原 ITSM 交付：`82ca81878d3731801a61b278ed3b89c5ab69f6dc`。
- ITSM R1：`0f10f726944604c8f29f05477dc1b32b17ab2239`；R2/R3：`78c00b3dc5db312ab967569f115689f5d7884104`；R4：`7c8cee6fae181400573308bfb7e73043d11ed0b9`。
- KAF R5–R8：`e6fd8a50368a15505c98c8826dee5af975cef5c2`，独立复审 ADDRESS，相关离线测试 68/68。
- 配置与 BPMN 源：G-A 固定运行时 `0788a9bb196ab37a8389b3f366bed9877b2f72c3`，seed SHA256 `372d605235f45b597e5b4ba256683cdc904a8ea32e4a2939c074e423d89ac191`。
- 使用原 pre-B0 备份，SHA256 `6c3861587ae58367159ecd1f5be08edff97ed6e3740cf5ca00244ac2676bec10`。恢复命令使用 no-owner/no-acl，仅作实际 schema 与数据的工具重放夹具，不据此证明 G-A 权限或应用准入。
- 独立容器 `gb-remediation-test-pg-20260914`，数据库 `gb_replay_review`，内部 Docker 网络、无主机端口。未改原源库、G-A 目标、原交付 checkout、历史任务或 main。

## 实际重放

五批按顺序执行。每批先只读快照绑定预状态，完整执行至 ROLLBACK 并确认状态未变，再提交并以同一制品即时重试。五批均成功；数据与五条收据同事务提交。

| 批次 | 回滚预演 | 即时重试 | 执行 SQL SHA256 |
| --- | --- | --- | --- |
| B0 | 通过 | 状态未变 | `edf0a270476dbb2d803bd6a25d63bce150609d5b0f909993d6d3e0d0a43b8be9` |
| Process | 通过 | 状态未变 | `8fe99261430cf404425324d29e7e5a3511229c3efc58f33ceb326353d25553e9` |
| B1 | 通过 | 状态未变 | `de98835d2df6876d3adeb483cb83d8890d71510ffa7399c38cd27975cd4e4831` |
| B2 | 通过 | 状态未变 | `47bf6970cfb1caae1bf495e49c5bf2a0d0cf7c2f1a5658d1e1b903a0331f1b73` |
| B4 | 通过 | 状态未变 | `332a85712ba89e22b49ecf1b04b63c00520308fe414e930f6c6c5a29703a2df2` |

最终计数：分类 185、模板 10、字段 59、SLA 7、目录 8、CI 类型 9、CI 46、标准变更 3、占位 KE 1、标签 4、视图 5；流程部署/定义 20/20，绑定 7。tickets 与 ticket_types 均为 0。

重放前后 tenants、departments、users、roles、permissions、role_permissions、user_roles、external_identities 及 schema_migrations 全行摘要和计数相同。此处只声明实际检查的这些表，不将其外推为额外未检查表。

受保护证据目录：`/home/administrator/.local/state/itsm-task2-remediation-20260914/`，含 replay-restore.json/log、各批固定 context/SQL/preflight、replay-before.json、replay-progress.json、replay-result.json。原始用户数据与凭据未提交。

## 测试与限制

实现者报告 ITSM 相关 Node 30/30、真实隔离 PostgreSQL 15/15；根任务另完成上述真实备份五批重放。独立复审固定 `7c8cee6f` 的 16 个变更文件：R1–R4 全部 ADDRESS，独立 Node 30/30、隔离 PostgreSQL 15/15，额外 dollar delimiter 字面值用例通过；未发现修复新增阻塞。

`make verify-scripts` 有原基线 `build-start-scripts.test.js:157` 镜像引用数量断言失败（预期 2、实际 3）；相关文件未改。KAF 全套测试存在既有环境收集错误，不能声称全量测试通过。

SLA 修复 `e290fd97c7f60838b966e0089ee843f01cb5afa8` 在独立分支，限定独立复审通过；`TestSLACalendar`、`TestTicketSLAService`、`TestA5FixSLA` 分别通过，尚未集成到运行时。产品默认 ticket_types 定向初始化仍在开发。没有启动候选应用或执行真实新建业务验收。

## 首期范围

用户确认单租户功能收口，不开展 MSP 产品扩展；以新规范为准，无法确定的旧优先级规则登记差异。旧路由后续处理，首期使用现有分派和规范流程；不迁移近似路由，不恢复历史工单。范围缩减不代替剩余实际功能验证。

## 日历配置后续修订与新发现

五批重放固定在 `7c8cee6f`，其中 B4 仍为原来的 89 假日配置。后续配置修订把原已登记的 19 补班日接入 `makeup_days`，声明 `valid_from=2024-01-01` / `valid_until=2026-12-31`；旧字段逐值不变。独立审查确认无重复、无假日交集，并用实际 JSON 验证新 parser 接受、末日闭店有效、2027 明确拒绝。该新制品尚未应用 G-A，不能冒用前述旧制品五批重放作为其执行证据；跨年度使用前必须续订日历。

2026-09-14 22:48 对真实 G-A 目标只读核实：7 条 `process_bindings.sla_policy_id` 全为空。当前新建路径依赖此 ID 执行 SLA，因此日历修复不等于新建 SLA 生效。**SLA 选择与新建截止时间尚为功能阻塞**，需验证实际接线后关闭。

新日历 JSON SHA256：`75444599fa6b380f9e372cca6442e026db28c260049ca5bf075aeccb5b60fd1c`。
