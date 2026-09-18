# 候选运行交付说明

状态：候选已运行，当前核心范围G1/G2/G3证据通过，R9维护者实际验收待定；尚未生产上线。当前Agent同时承担代码与环境交付。

## 已接受的功能范围与鉴权限制（2026-09-14）

维护者以核心功能、架构可用和上线为目标，明确排除本轮飞书同步，并接受以下未完成项，但要求在文档中披露。

**新 PostgreSQL 鉴权存储还需接入应用启动、登录、刷新、注销，再验证重启与恢复。此项未完成，不计为验收通过。**

当前实现包含严格 JWT/验签租户校验、PostgreSQL TokenStateStore 和 `046_auth_token_state` 迁移，证据提交为 `d4bedf7b7da4d3a3560f93ef834c6d3f8f585c23`。新存储接口及私有故障测试通过不代表生产入口已经使用它；运行仍走原 Redis/内存鉴权路径。

业务影响：首次 Redis 连接失败可能保留内存撤销存储；进程重启或 Redis 状态丢失后，未过期 token 的注销和刷新防重放状态可能无法完整保留。尚未验证真实 API/PG 重启、旧备份恢复后旧 token 拒绝和新登录正向，不能声称这些能力已交付。数据库迁移成功也不能替代接线与恢复验证。

维护者接受将上述差额保留为后续 R4/A3–A4 工作，本轮继续候选集成与环境交付。既有普通业务重启、消费者恢复、持续观察仍须执行，不冒充新鉴权状态恢复测试。

## 当前访问与资源

Web：<http://localhost:3301>；API：<http://localhost:18080>。Windows与WSL实测可访问，保留现有登录账号。入口仅绑定本机，不是企业对外生产地址。

运行代码SHA `0788a9bb196ab37a8389b3f366bed9877b2f72c3`。初始EnvironmentRevision及备份、迁移、角色证据见[2026-09-14 T3交接](../review/2026-09-14-candidate-t3-handoff.md)；后续配置增量与验收边界见[T4证据](../review/2026-09-14-candidate-t4-evidence.md)。当前EnvironmentRevision为f058c8227e7f350387ecfa9f5296bc0a1fd852c1c392d1071619de27f1c24424；G3及维护者验收状态以唯一计划最新检查点为准。

Docker资源前缀统一为`itsm-candidate-20260914-`：pg、redis、minio、receivers、api、worker、web、ingress。数据库`itsm_candidate`，独立Redis实例DB11，MinIO桶`itsm-candidate-uploads`；附件目录、密码和配置在私有状态目录，不进Git。API/Worker处于内部网络，不可连接源数据库/Redis/对象服务或互联网；ingress只向固定API/Web上游转发。

Incident默认使用已声明的人工生命周期，自动应急流程未启用（依据功能优先作出的可逆假设，维护者偏好仍待确认）；Change和目录申请使用真实BPMN。SMTP仅为本地隔离收件器，Webhook仅向已声明的内部接收器投递，KAF仅为隔离传输接收器；不代表企业邮件发送或真实KAF执行。飞书未启用。历史队列及验收记录均保留。

## 日常启停

以下在WSL Ubuntu执行，仅针对本候选；不操作源服务。Windows PowerShell可用`wsl -d Ubuntu --`调用相同docker命令。

停止应用入口和消费者（保留PG/Redis/MinIO数据服务）：

```sh
docker stop itsm-candidate-20260914-ingress itsm-candidate-20260914-web itsm-candidate-20260914-worker itsm-candidate-20260914-api
```

启动已存在且已准入的容器：

```sh
docker start itsm-candidate-20260914-pg itsm-candidate-20260914-redis itsm-candidate-20260914-minio itsm-candidate-20260914-receivers
docker start itsm-candidate-20260914-api itsm-candidate-20260914-worker itsm-candidate-20260914-web itsm-candidate-20260914-ingress
curl --fail http://localhost:18080/api/v1/health
```

必须等待数据服务就绪后启动应用。以上是现有容器操作说明，不自动创建/覆盖资源；2026-09-14 13:30已验证API/Worker受控重启、业务表保全及重启后申请审批/履约；第一次观察在约14:07因WSL意外重启中断；原容器/数据恢复、网络和业务再验证通过。第二次独立观察14:13:08–15:18:14完成，单调时长3601.71秒、60样本、0失败，独立复核通过。根因尚未确定，不能声称宿主问题已修复。不要启动保留的`redis-failed-v72`容器，也不要用compose down -v、清库或清Stream修复状态。

## 备份与恢复边界

维护者已批准并执行源一致性窗口，2026-09-14 11:55:50–11:55:53 CST完成，原API/两个Worker恢复且源health200。私有备份目录：`~/.local/state/itsm-candidate-delivery/t3/backup-20260914-115550`，包括PG自定义dump、DB11二进制DUMP/绝对期限、manifest和保密的进程恢复信息。源PG/Redis/MinIO未停止。

两次独立逻辑恢复内容核对通过；不是物理集群灾难恢复演练。恢复应新建独立资源、先校验manifest与镜像/版本，再恢复PG及Redis绝对期限、核对附件引用、通过准入后启动。不得把原始备份覆盖到已有人工验收候选，也不得运行旧破坏性测试harness。未执行R(038)，不允许在恢复过程中自动执行它。

唯一进度来源为文档分支`codex/docs/candidate-a-remaining-delivery`中的remaining-delivery计划。本说明不建立第二份任务清单。
最终运行证据见[2026-09-14 T5记录](../review/2026-09-14-candidate-t5-evidence.md)。R9仍需维护者查看代表性工单/附件、变更审批及请求履约页面并确认；Agent未代签M4。本轮检查结束不代表已部署持续监控服务，候选保持运行供验收。