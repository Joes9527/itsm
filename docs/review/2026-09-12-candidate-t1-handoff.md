# T1 候选代码集成交接

- TaskID：T1
- Owner：Agent A / Mac
- Status：completed（G1 代码集成门禁；不代表 G2/G3 或 WSL 部署通过）
- CandidateSHA：`d7470a32dbb87acc9b5e4d9a895a146410723561`（B 必须构建此版本，后续交接文档提交不替代此字段）
- Branch：`codex/feat/candidate-integration`
- Worktree：`/Users/julian/.worktrees/itsm-candidate-integration`
- SourceSHA：main `a25e108d2a08a55469fa5ad547aac5a9adc251ff`；WorkItem `8152ee668a6096c98f90349fd7e43ebcd4f1c587`；主题 `eb76c3bca6a4cee4809711477231f486d01d04c5`。
- ConsumedHandoffs：设计/计划提交 `2a993f7159ed43afc471a6ae938c379a47149ec2`；尚未收到 T2/T3 交接。
- Resources：WSL not-created/not-modified；仅本机任务 worktree、依赖与私有验证日志。所有原 worktree 保留。

## 1. 集成与复核

先 fast-forward 纳入 WorkItem，再以 merge 纳入主题，保留两来源历史。分类与 RCA 修复已通过 ancestry 验证包含在 WorkItem 来源内，没有重复移植其他 runtime 分支。两来源共同修改 24 个前端文件，其中 9 个需人工解决冲突。

| 冲突 | 决议 |
| --- | --- |
| changes detail/edit | 采用主题表面/文本，保留业务字段和版本操作 |
| problems detail | 保留 panelRevision，禁止恢复已删除的第二套关联面板 |
| reports/change-success | 保留 outcome 成功率、错误提示和已删除的 byType；应用主题图表外观 |
| service-catalog/request | 保留 requesterResource 权限边界 |
| standard-changes | 保留 beforeNewConfirmation 关系刷新 |
| tickets/create | 保留分类 ID/CTI，禁止恢复 category code 推断 |
| ChangeImpactAnalysis | 保持 WorkItem 已有删除，不因主题改动恢复旧组件 |
| WorkItemSLA | 保留当前/历史周期、暂停、完成和配置缺失语义；改用主题表面 |

独立审阅者未继承会话历史，逐项检查 24 个重叠文件及自动合并，确认无 Critical/Important；主题侧非重叠文件保持来源。另一次独立复审确认测试环境修正未弱化断言。此审阅限于集成差异，不重复认证整个来源分支或目标环境。

同时纠正 AGENTS/CLAUDE 的过时实现状态、历史设计报告当前入口及 P 阶段误放完整 V1 的顺序。没有新增后端业务机制；后端源码与 WorkItem 来源相同。

## 2. 验证

运行版本：Node v24.13.1、npm 11.8.0、Go go1.26.0 darwin/arm64。独立副本使用锁文件 npm ci，不升级依赖；候选前端锁文件 SHA256 与主题来源一致。测试通过白名单环境启动，没有继承业务环境变量或复制 .env；未使用共享数据库配置。

| 检查 | 结果 | 边界 |
| --- | --- | --- |
| 最终前端全量单元 | 236/236 套件通过；3264 passed、13 skipped、0 failed | 跳过项不计为通过；不是浏览器或真实后端验收 |
| 集成重点组件/API | 5 套件、52 用例通过 | SLA、Shell、Requester、关系、Change 分类 |
| 类型/主题一致性 | npm run type-check 通过，包含 theme:check | 修正后再次通过 |
| lint | 0 errors、1 既有 unused eslint-disable warning | BPMNDesigner.tsx:348；未为消除警告混入修改 |
| 前端生产构建 | npm run build 通过，standalone prepared | 未启动候选服务；后续测试修正仅改变测试文件 |
| 后端构建 | go build ./... 通过 | Mac 构建，不能代替 WSL/Linux 构建 |
| 后端定向测试 | migration、internal/bootstrap、common/workitemidentity、authentication：164 test events passed、7 skipped、0 failed | common/workitemidentity 无测试；7 项为未配置真实 DB 的 opt-in 测试，留给隔离验证 |
| 差异检查 | git diff --check 通过 | 无未解决冲突；运行生成的已跟踪 junit.xml 已恢复，不提交测试输出 |

后端命令：`go test ./migration ./internal/bootstrap ./common/workitemidentity ./authentication -count=1 -json`。前端命令和 cwd 在私有 evidence-manifest.json 中逐项记录。完整后端/真实 PG、浏览器 E2E、Linux 构建及外部副作用不在本轮通过范围。

### 来源失败与修正证据

- 原 WorkItem 全量基线：3 failed / 225 passed 套件；10 failed、13 skipped、3206 passed 用例。
- 原主题全量基线：2 failed / 221 passed 套件；5 failed、13 skipped、3184 passed 用例。
- 候选初次全量：1 failed / 235 passed 套件；4 failed、13 skipped、3260 passed 用例。失败均在 classification-edit 的 Problem 路径。
- 单独 RED 重现 4 failed / 4 passed。代码使用 crypto.randomUUID 生成 operationId，但 jsdom 缺少该浏览器 API，调用在 API 请求前失败。
- 仅在 classification-edit.test.tsx 提供 node:crypto 的真实 randomUUID；fixture 增加 version:5，新增版本及 UUID 格式断言。没有修改生产代码或降低分类/调用次数/导航断言。
- 单独 GREEN 为 8/8；最终全量为上表 236 套件通过。
- 目录申请/通知来源曾有弹层可见性和超时失败，候选单独复现 2 套件 23/23 通过，候选两次全量也未出现同类失败；保留来源日志，不宣称已修复其根因。

## 3. 证据位置与摘要

原始日志仅在 Mac 私有目录 `/Users/julian/.local/state/itsm-candidate-delivery/t1/`，未提交源码库。`evidence-manifest.json` SHA256：`300f3f36c33956e219e84bf050741fa6ba1e54b6f6b517724cb1716ebe0ea96e`。交接包可携带该脱敏清单；需要原日志时按精确文件传递，不复制环境配置或备份。

| 日志名（.log） | SHA256 |
| --- | --- |
| candidate-unit-final | `303837441f8ba72beda95fe7ee90387c5baef76a6ca3a9f4551695f5619a8bf6` |
| candidate-focused | `f31e877919b900cf3b9350e02364de81e8fcdce28d5f76d9277b41f76729c1ea` |
| candidate-typecheck-final | `c3bd005dde7eeeb3f37382b09d2b0bf3280f5350f47c0cc15912c57fb101a83c` |
| candidate-lint-final | `c19dd0bc65816ca8cc4e6524bac3a17b445a4d3f9aaaaa2aa25e35f24ea134af` |
| candidate-build | `aa5e93318617755058d1e71f1f4d62bd582ebae1702fcee806e2389cd096d053` |
| candidate-go-build | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| candidate-go-test | `f3d63548357ec1667643e74444716b7cffc9ffe4359a68403ce0ab44600e82a3` |
| classification-red | `bf4134034a87c4cc339b8e30defb31bc7784cfd4e5f5aefe661af4770a70dc7c` |
| classification-green | `9f70774be32469492fd8b0e41682745b44bd90ff29c7e38d5181928e6732103d` |
| failure-reproduction | `d13f9cb9a1dd4bff8bd5f67939c15a072017e0dba1542f01ff074245ba58641a` |
| base-workitem-unit | `61cb33ef34a59310cd8f17d2009d1a12416a1ecdc4be06ac9f1d5b4a87e86012` |
| base-theme-unit | `30d9fcbfb3da7e2fbf78d527b1b799e7feb517f7e6f2aeede459ff048bce6015` |

## 4. B 的下一步与未验证范围

**NextAllowedAction：** B 完成 T2，基于 CandidateSHA 重核完整迁移语义、角色、后台写入者及目标资源。只有全部准入通过，才进入 T3；不能把 G1 当作应用启动许可。

**Blockers：** T1 无未关闭代码集成阻塞；T2 尚未交付，目标迁移与 API 内消费者隔离能力尚未认证。已知 API 启动会运行后台消费者，普通迁移亦有历史删除/回填门禁；B 按 T2 提供实际证据。必要前置代码设计由 A 接收，不能由 B 绕过或清空数据。

**NotValidated：** WSL/Linux 构建；真实源/副本恢复；P/普通迁移；全部后台写入隔离；真实 DB opt-in 测试；浏览器完整 G2；Redis 冷启动撤销目标验收；HMAC 恢复；60 分钟稳定运行；维护者旅程。R、真实企业外部写入和生产上线仍排除。

A 在收到 T3 固定 EnvironmentRevision 后才执行 T4；此时不修改 B 配置、不启动其服务，也不自行执行共享数据库变更。未推送、未合并 main。
