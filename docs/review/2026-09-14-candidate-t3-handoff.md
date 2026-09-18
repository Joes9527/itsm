# T3 候选环境交接（2026-09-14）

状态：可进入有界T4；G2/G3和维护者验收尚未完成。当前Agent同时负责代码和环境。

- CandidateSHA：`0544e159adf47a5fa18af175611a77ea09cab574`。
- EnvironmentRevision：`f8a2caac2c536832f304c05dc6f5798d11e66adb70b81090e76a29b848015b1b`。
- Web：`http://localhost:3301`；API：`http://localhost:18080`。WSL及Windows宿主实测登录页面200；真实Playwright表单登录及auth/me通过，1/1。
- 精确资源、镜像、二进制、配置和证据摘要：`~/.local/state/itsm-candidate-delivery/t3/environment-20260914/environment-revision.json`。API/Worker为新SHA的Linux构建；前端与3142247e的Git tree完全一致，复用其已通过的production standalone。

## 备份、恢复与迁移

维护者批准“可以，随时可做”。窗口11:55:50–11:55:53 CST；精确停止原API和两个Worker，实际静默约2秒。PG/DB11无其他连接后保全源；原三个进程已恢复，源 `/api/v1/health` 200。最初探测 `/health` 404是路径不匹配，不记为服务故障。共享PG/Redis/MinIO未停止。

私有备份 `t3/backup-20260914-115550`：源 `itsm_config_baseline_20260908/public` 的custom pg_dump、角色/ledger元数据、DB11 DUMP与绝对期限、实际进程恢复配置；秘密不进Git。8工单、4评论，附件/评论附件/导入导出结构化引用0，本地源uploads不存在，因此未复制共享桶的其他对象。

专属PG与源同镜像PG17.10/vector0.8.6。两次逻辑恢复的152表内容一致；不是物理集群灾难恢复演练。人工候选 `itsm_candidate` 已迁移；`itsm_candidate_test` 当前只作未迁移备份验证副本，未放行旧破坏性harness。

实际升级关闭两处组合缺口：

1. 原账本缺回执列；canonical `-up` 的EnsureMigrationsTable补齐。037首次事务被拒后回滚，没有手工SQL补账本。执行入口误按数值拒046后手动038，`a014c1b9`修复终端手动阶段顺序；RED/GREEN与独立复核通过。
2. 039之后P校验误拒正规登记trigger；`0544e159`按039正确回执及精确触发器/函数正文/owner/权限接纳，缺失、多余、篡改、禁用仍拒。真实私有PG RED/GREEN与独立复核通过。

037和032–036、039–046已提交，ledger38条，038未执行。152张原表的原有记录/字段对账无变化；登录后新增1条审计、迁移新增14条账本分别记录，不误判为修改历史记录。当前原始行保全证据 `original-row-preservation-122540.json`。

Redis初次选用7.2镜像无法读取源7.4 DUMP，失败容器已停并保留。改用源的精确7.4.10镜像后恢复222键，1键自然过期不复活；逐键DUMP和绝对期限复核无差异，包括原Stream。AOF always/noeviction。源Redis未改配置。

## 启动边界与未验证项

8个运行容器均有任务label；API/Worker与专用本地SMTP/KAF运输接收器共享内部网络命名空间，PG/Redis/MinIO专属卷。只读镜像与配置，日志/新附件单独可写。入口代理只转发固定API/前端，拒绝CONNECT；应用没有接入入口外网网络。

实际探测：候选依赖可达，源PG/Redis/MinIO、Docker网关源端口及测试公网443不可达。MinIO应用凭据仅访问候选bucket。candidate_runtime非owner/non-super/non-bypass；system依原allowlist，仅另加候选范围SELECT与流程id/tenant_id/execution_work_item_id三列SELECT，启动角色校验通过。原角色名称仅在副本中NOLOGIN保留，源凭据不作为候选运行凭据。

必需outbox/callback/notification/KAF/SLA/escalation/event_audit/webhook/tool_queue/request_async配置scoped。飞书未配置/未激活；embedding/connector polling/cloud discovery/import-export/diagnostics禁用。KAF接收器只证明HTTP运输接受，明确不是实际KAF业务执行，不伪造任务完成回执。

首次周期的system读取缺权限已按既有合同补齐并重启，随后周期无同类错误；不是通过禁用worker消除失败。callback/outbox/notification周期及历史保全已观察。SLA/escalation完整周期、真实业务写入/恢复及双主题旅程继续由T4记录；G3的60分钟观察未开始。

维护者已接受的新PG鉴权生产接线与恢复验证缺口继续保留：运行仍是Redis/内存路径，不能宣称新鉴权状态恢复已交付。AI embedding因未配置模型不可用；不把它或企业外发说成成功。

下一步：固定本Revision执行T4，只新建本轮记录，不修改历史工单；不运行旧harness恢复/R模式。后续代码/配置变更必须发新Revision。当前没有合并main、真实企业写入或生产切换。