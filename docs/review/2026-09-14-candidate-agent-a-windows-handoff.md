# Agent A 全量工作交接：Mac → Windows/WSL

状态：accepted handoff scope；2026-09-14。维护者明确交接全部剩余工作直到 M4，不仅 R1。

## 目标与职责

接任 Agent A：负责代码集成、业务安全收口、鉴权修复验证、候选版本冻结、G1/G2 业务与主题验收，以及 G3 证据复核/维护者交付。终点是在 WSL 稳定可访问、通过 G1/G2/G3 的 WorkItem 与双主题候选版本。

Windows Agent 接任后从 R1 开始，持续承担 R1–R9 中 A 的责任直到 M4。B 仍拥有环境配置、恢复/迁移、部署、T3/T5 实施。接任 A 不等于自动获得 B 的环境操作授权。不要仅完成 R1 就宣称交接目标完成。

唯一剩余范围/优先级/状态入口：`docs/superpowers/plans/2026-09-14-itsm-candidate-remaining-delivery.md`（位于 design 分支）。原 integration delivery spec 定义 G1/G2/G3；scope/auth/T1–T5 旧计划只提供既有技术合同及历史证据。本交接是进入工作的方法，不建立第二套状态清单。

## 当前进度与精确版本

| 用途 | Git 分支 | 交接时代码/文档基线 |
| --- | --- | --- |
| 设计与唯一剩余清单 | codex/docs/candidate-delivery-design | 收口提交 a45ba3c7bae2d41e9b75def2f77c4e9e5c558cc2；本交接随后在同分支提交，最终头见包内 manifest |
| 已实现的隔离/业务/邮件修复 | codex/fix/candidate-execution-scope | 3098f9718b98fbcd2fa4fc422a8496c14a141d7f |
| 初次候选集成与交接记录 | codex/feat/candidate-integration | d62420b6cddbb37cb0fa6d1ec739e959689d2a7e |
| 固定 CandidateSHA（不可当当前全部修复） | 上述分支历史中的提交 | d7470a32dbb87acc9b5e4d9a895a146410723561 |
| 原 main | 不修改 | a25e108d2a08a55469fa5ad547aac5a9adc251ff |
| WorkItem 来源 | 已包含历史 | 8152ee668a6096c98f90349fd7e43ebcd4f1c587 |
| 主题来源 | 已包含历史 | eb76c3bca6a4cee4809711477231f486d01d04c5 |

修复和设计工作区交接前干净。候选应用未启动；未执行 WSL 共享数据库变更、企业实发、push 或 main 合并。最终 bundle 包含三个相关分支的完整可达历史，不要求接收机器已有这些提交。

已完成 M0；M1–M4 未完成。目标工具上次显示 blocked，累计约17小时7分钟及1280万tokens：这是旧执行记录，不是完成证据，也不意味着本地 R1 无法开展。不要自动复活原无限循环，不创建新的无限目标。

## 全部交接范围（状态只在唯一清单维护）

| 里程碑 | 工作与接任职责 |
| --- | --- |
| M0 已完成 | 范围已冻结，独立审阅无阻断；不要重新规划另一份平行路线图 |
| M1：R1–R3 | R1 核对实际候选入口与已有证据差额；R2 最小业务/异步安全收口（含飞书）；R3 私有整体验收、历史保全、周期/恢复证据和独立审阅 |
| M2：R4–R5 | R4 按 accepted 鉴权计划完成持久状态及故障恢复；R5 集成已审阅修复、重核 G1、冻结新 CandidateSHA/指纹/bundle 交 B |
| M3：R6–R7 | B 完成 T2 差额/T3 EnvironmentRevision，A 复核；收到完整交接后 A 在固定候选执行真实业务、权限、消费者、附件及双主题 G2 |
| M4：R8–R9 | B 实施受控重启和至少60分钟观察，A 复核恢复/增量保全；维护者完成代表性旅程后关闭 G3 与完整目标 |

6类延后项：D1 企业实发；D2 未声明可选能力功能完善；D3 全平台 provider/任意并发/高可用扩展；D4 I2/R038/物理清理/历史回填/多WorkOrder等原排除项；D5 无关重构/旧测试迁移/文档整理/额外打磨；D6 main合并/生产上线/新平台功能/长期优化。精确条件见唯一清单：不得以 D 类豁免 G2 必需路径或已批准安全要求。

执行规则：复用已有证据；A 同时只实现一个 R 项；每批最多60分钟后报告关闭断言、差额和耗时；新发现必须对应已有 R 的具体交付断言；同一失败连续两轮无进展时停止盲目重跑；按影响范围测试，里程碑末一次集成验证/独立审阅。不能默默增加开发波次或将全关消费者当业务通过。

## 已经有效的实现与证据

- S1 范围登记、S2 无副作用构造和显式生命周期已有完成证据。
- 工单编辑原事务、Meta/receipt、版本CAS、前端组件重试，手动/BPMN升级，队列范围/恢复，工具及 Stream/Webhook 已有多个实现检查点。旧复合 checkbox 未勾选不代表这些主体未实现。
- 邮件目标六项已完成：可信配置描述与激活分离、不可变 v2 Graph/SMTP 目标、045迁移、原生产事务、发送前/后目标验证、候选 runtime/system 职责链。notification 与 Incident 原队列均已有真实本机发送证据。
- 近期提交：58af40837 notification原邮件目标；9c555b5b3 Incident v2/actor；ff9ffe323 领取和来源负例；19227dbb7 outbox禁用前置门禁；3098f9718 Graph本地初始化准入。
- 实现分支权威测试证据：`docs/review/2026-09-12-candidate-t1-handoff.md`；运行约定：同分支 `docs/DEVELOPMENT_GUIDE.md`。不要只读 design 分支的旧文件来推断实现现状。
- 最近 `s5-graph-activation-core.log`：connector/...、database、bootstrap默认标签全包race通过；`s5-graph-activation-final-build.log`：全后端build通过；`s5-graph-activation-final-private-corrected.log`：规定私有PG16/Redis/MinIO suite race PASS，无FAIL/SKIP/DATA RACE。29个 Incident协议场景及候选SMTP/Graph路径包括在内。
- 上述结果只代表 Mac 任务私有依赖/本机接收端；不是完整所有测试、WSL PG17、真实 Graph 企业邮箱、真实浏览器或 G2/G3 的完成证明。原始日志留在 Mac 受保护目录，包中不携带数据库、配置秘密或原始业务数据。

## 下一步：从 R1 开始，但持续负责到 M4

1. 校验交接包 SHA256 和 bundle；在新目录导入，不覆盖 Windows/WSL 已有 checkout、分支或未跟踪文件。先读 AGENTS.md 与工程治理，再读唯一剩余清单、两份 accepted 前置设计及实现分支最新 T1 证据。
2. R1 做一轮定向差额盘点：对候选实际可达入口记录路径、所属旅程、启用/明确拒绝、已有提交/日志和唯一未满足断言；直接更新唯一清单中的 R1 证据节。不是全仓库无限审计，不先编码。
3. 按 R2 的明确差额最小实现；每项复用原业务所有者/事务/队列。完成 R3 再进入 M2；R4 可先读取核查已有实现，但不从旧空框推断全部重写。
4. R5 形成新 CandidateSHA 后按原 bundle 协议交 B。R6 未具备 EnvironmentRevision 不执行 R7；等待时只能做独立准备，不代替 B 迁移。
5. R7–R9 完成后分别报告候选交付、main、企业外发和生产状态，不把候选交付等同生产发布。

### R1 飞书已知线索（仅调查结果，尚未新增实现）

文件：`itsm-backend/service/feishu_creation_delivery.go`、`feishu_update_delivery.go`、`feishu_sync_service.go`、`ticket_creation_effects.go`、`controller/feishu_controller.go`、`internal/bootstrap/app.go`、`connector/builtin/feishu/{connector.go,client.go}`。

- creation provider 目前主要按tenant返回对象并比较专业Destination，payload缺完整固定实例协议；creation handler与update handler的领取/后置核验程度不同，不能假定两者均已完成。
- 真实HTTP `FeishuSyncService.SyncTicketToFeishu` 仍是有效所有者，必须与自动创建和编辑更新一起核对。保留原 GUID/映射/有序outbox。
- `TaskDestinationIdentity`从保留的cfg凭证map读取app_id，实际Client捕获了另一个appID值；调用者改map可能让声明身份与实际Client不一致。尚无新增RED或修复，不应报告已证实运行漏洞或已修复。还需核对Init重复调用和HTTP redirect行为。
- 当前多个旧SQLite飞书集成测试直接 Deliver、无真实claim。不得去掉生产锁/许可校验仅为旧夹具过绿；新增跨域/真实依赖测试应放tests/integration。
- 这不是授权另起飞书平台扩展；先关联R1/R2的具体断言。

## 有效方法与不要重复的失败

有效：用真实原producer→持久队列→原worker→原handler→私有接收端；禁用路径比完整原始行，正向验证实际接收与回执；独立角色区分业务runtime与system worker；请求重放复用原命令；审阅只按本任务验收合同。

不要重复：

- Ent JSON隐藏claim_token/payload等字段，不能证明整行保全；PG使用row_to_json/完整SQL快照。
- SQLite不支持生产FOR UPDATE，不要削弱锁来迁就测试；需要锁的协议使用任务私有PG。
- 可空OperationID是指针，断言需检查非空并解引用；Manager.CloseAll返回void。
- 不把“缺依赖导致提前拒绝”当目标授权负例通过；正向和负向应从合法原producer生成意图。
- 不在Go test/build/generate尚运行时编辑Go文件；从服务goroutine不要调用require.FailNow。
- 不让每次局部修复都触发无关全量回归、复审和追加计划。此前17小时的重要原因是范围不断细分与重复循环。
- `git count-objects`曾报告worktree refs下garbage警告；不要趁交接执行prune/gc/clean，bundle完整性以verify和独立恢复为准。

## Windows/WSL 环境与 B 交接

Windows路径和WSL路径不可直接互换；执行Go/PG测试优先在已选定WSL发行版的独立任务目录中。导入说明使用相对路径，可放在任意新目录；不得直接使用Mac路径运行服务。

Mac参考路径（不代表Windows存在）：
- 仓库 `/Users/julian/development/itsm`；三个worktree在 `/Users/julian/.worktrees/itsm-candidate-*`。
- 日志 `/Users/julian/.local/state/itsm-candidate-delivery/b2/`；环境清理包装 `/Users/julian/.local/state/itsm-candidate-delivery/t1/run-clean.py`。
- 私有PG16.14 socket `b2/pg.T0mNnL`，port25439、owner candidate_test_owner、marker candidate-test-instance。不复制凭据/实例数据；WSL须显式创建/验证自己的任务私有依赖。
- 既有测试要求 `CANDIDATE_SCOPE_TEST_SOCKET`、`CANDIDATE_TEST_REDIS_BINARY`、`CANDIDATE_TEST_MINIO_BINARY`。缺失时skip不能视为通过；禁止默认回落到B的共享源。

B最新已知 T2 revision-2 bundle：`candidate-t2-admission-revision-2.bundle`，SHA256 `932c91873632a41211f8930042b95a859374e710688b7ffbad127114b2d3daea`，head `a0ad4617c6467df87f6bf942d5969004c60ca21c`；此前尚未合并。B原路径 `/home/administrator/.local/state/itsm-candidate-delivery/handoff/`。源映射曾报告 `itsm_config_baseline_20260908/public`、PG17.10/vector0.8.6、旧迁移账本24/24、Redis DB11，均是旧交接观察，接任必须只读复核，不当当前事实或操作许可。

尚无本任务已验收的 T3 EnvironmentRevision/具体备份窗口。Windows Agent与B即使在同一机器，也必须保持独立角色、worktree及任务私有测试资源；A不修改B配置，不重启共享服务，不执行共享数据库迁移。

## 交接产物与接收核验

外部交接目录含：本文件的 `HANDOFF.md` 副本、唯一清单副本、`candidate-agent-a-full.bundle`、`manifest.json`、`SHA256SUMS`、`IMPORT.md`，以及校验相符的原 B T2r2 bundle（若存在）。源码、测试、计划、T1证据的权威版本均在Git bundle内；根HANDOFF是便于新会话打开的入口，符合仓库禁止新增根过程报告的约束。

发送包到Windows是维护者的传输步骤；本机生成不代表Windows已收到。导入后接任Agent应首先报告三分支SHA、工作区保全、文档入口及R1批次范围。Mac A在交接后停止新增实现，避免双写。
