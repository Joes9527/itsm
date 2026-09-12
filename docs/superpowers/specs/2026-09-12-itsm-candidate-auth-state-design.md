# PostgreSQL 鉴权状态权威设计

状态：draft（维护者已同意方向；详细设计待书面审阅）。

所有者：Agent A；隔离部署验证：Agent B。基线 `d7470a32dbb87acc9b5e4d9a895a146410723561`。解决 T2 B3，配合[执行范围设计](2026-09-12-itsm-candidate-execution-scope-design.md)，不表示已修复或部署。

## 1. 决策与范围

现有 authentication/revocation.go 默认内存存储，bootstrap 只有 Redis Ping 成功才替换它；refresh_token.go 使用 Redis SETNX 记录单次消费。Redis 不可达与 Redis 已丢记录但仍可连通是不同风险。仅增加 Ping 门禁不能证明已撤销 access token 或已消费 refresh 不复活。

选择 PostgreSQL 作为 access 撤销与 refresh 消费的唯一持久化权威，沿用既有验证/消费服务边界；删除这两类状态的 Redis 生产读写和隐式内存回退。不增加双查、双写、Redis 缓存放行或第二套会话授权系统。内存 fake 仅可位于测试文件。用户/tenant/role 的实时授权继续由现有 SessionReader 等负责。

Redis 仍承担事件等已有职责，候选仍需保全 DB11。HMAC 默认禁用；本设计不迁移 nonce、防重放、租约或任意 Redis 键，不宣称 HMAC 通过。若验收需要 HMAC，必须另有对应新身份、nonce 保全及恢复证据。

## 2. 数据与权限合同

受控迁移新增一张 purpose 受约束的 token 安全状态表及一张 authority 元数据表。逻辑记录键为 (authority_id, purpose, token_digest)，purpose 仅 access_revocation/refresh_consumption；内容为验证后的 tenant_id、actor_id、expires_at、recorded_at。digest 为完整签名 token 的 SHA-256，永不持久化原 token。purpose 防止两类语义混用。绝对期限来自已验证 claims，不因重试延长。

摘要计算前要求唯一编码：access/refresh 统一只接受三段无填充、无空白的规范 base64url compact JWT；每段严格解码后重新编码必须逐字节等于输入，验签使用严格解码并限定已有签发算法 HS256。非零尾部填充位、CR/LF、等号填充或其他非规范表示直接拒绝，不能先规范化后接受，也不能只靠原 token 字符串摘要防重放。现有 ParseWithClaims 未开启 strict decoding，此项属于本次必要修复。

authority 元数据绑定唯一候选部署与受保护配置中的 authority_id，维护者在首次启动前显式创建，API 不自动建立或重建。缺表、无 authority、版本不符或 ID 不匹配即不就绪。新配置不允许沿用源 JWT secret；旧签名 token 从密码学边界直接拒绝，不依赖把旧 Redis 摘要导入 PostgreSQL。

运行身份必须非 owner、非超级用户且无 BYPASSRLS、无 DDL/TRUNCATE/DELETE/修改 authority 权限。鉴权 repository 只使用最小权限连接与 tenant 上下文，不复用广权 system client 绕开检查。tenant 来自已验签 claims，客户端不能直接指定；存在性查询不得因错误 tenant/RLS 隐藏返回“未撤销”。实施须用实际角色证明绑定/策略不匹配会拒绝，而非获得空集后放行。跨 tenant 无关记录不能阻塞合法 token，也不能揭示其他租户状态。

authority 属部署级安全元数据，与业务租户状态分离；其读取是明确的最小全局权限，不赋予跨租户业务查询。运行中的 authority 和 schema 检查错误按不可用处理。记录仅追加，撤销重复写幂等；唯一约束处理并发。初期不实现后台清理，已过期记录保留，避免在本轮增加自动删除或恢复风险；容量需纳入 B 的预算。

## 3. 请求行为与原子性

access token：先验证签名、类型、期限和完整身份，再用已验证主体查询 PostgreSQL。命中撤销记录即拒绝；存储不可用、权限错误、authority 异常不允许当作未撤销。启动未注入真实存储时默认不可用。protected API 的错误使用现有鉴权/基础设施错误映射，不向用户暴露 DSN、SQL 或 token。

现有 ValidateAccessToken 在上层 tenant middleware 前运行，因此不得依赖随后才设置的请求租户。认证包用验签后的 claims 构造不可由外部字段拼装的已验证主体，要求 expires_at 和正数 tenant/actor、非空角色/用户名；再建立 tenant 上下文并清除继承的 system bypass。撤销与消费接口接收该主体和其绑定的 token digest/期限，替换仅传 token 字符串的接口；已有请求上下文 tenant 若与 claims 冲突直接拒绝。登录、刷新、登出和所有 ValidateAccessToken 调用点同步调整，不能保留未验证身份的旧接口作为回退。

注销/撤销：在 PostgreSQL 持久化撤销记录提交成功后才报告成功。并发相同撤销幂等，数据库错误不能报告成功；审计不含 token/digest 全值。多 API 进程不依赖进程内状态。

refresh：保留 Validate → 当前会话授权 → Consume 顺序。Consume 使用唯一键的原子 INSERT，只有实际插入并提交成功的一方继续签发；冲突返回 already-consumed，任何 DB/提交不确定错误返回不可用且不签发。消费提交后签发或响应失败，旧 refresh 仍保持已消费，用户重新登录，不以重放获得可用性。事务开始时及签发前检查期限，过期拒绝。

登录与签发：存储/authority 未就绪时不签发新 token；新候选签名材料属于受保护配置，不能回退默认 secret。签名密钥和 authority 作为同一安全配置修订交接。事务提交必须使用 synchronous_commit=on，B 验证实例 fsync 和持久存储设置；不以进程重启测试替代已确认写入的耐久性测试。

## 4. 切换、恢复与不可证明的边界

本轮只在独立候选切换：恢复/迁移准入 → 新增结构迁移 → B 显式配置新 authority 与全新签名密钥 → 配置/角色/历史状态保全核验 → 启动新 SHA。源继续原版本，不修改其 Redis、密钥或服务。候选不接纳源 access/refresh，维护者重新登录，这是明确的切换行为。旧 Redis 的 210 条消费状态和历史 Stream 原样保全，不删除、不导入、不延长 TTL。

迁移只添加所需表、约束和最小授权；不修改旧 migration checksum、不导入历史业务、不占用 R(038)。新版本不兼容未准备的鉴权 schema，缺失即失败；部署手册必须说明这项升级契约。不是可对现有生产环境直接滚动升级的授权。

Redis 冷启动、不可达、FLUSH 或丢卷测试只在任务测试实例：PostgreSQL 中的撤销/消费记录仍拒绝旧 token；事件依赖可导致应用不就绪，但不得退回内存或允许鉴权绕过。PostgreSQL 连接断开/权限错误/表缺失一律 fail closed。

PostgreSQL 自身恢复到旧快照可能丢失已确认鉴权记录；本设计不声称数据库备份天然防回滚，也不以 authority 单行存在证明所有记录完整。任何候选 PG 备份恢复、丢卷、状态完整性不确定，均须停止所有签发/鉴权实例，保全现状，然后由 B 通过具体恢复步骤更换 authority 与签名密钥、拒绝所有恢复前 token，重新准入。不得恢复旧配置并继续接受旧 token。运行身份无删除权限用于防止应用删除；恶意 owner 选择性篡改不在本机制可自动检测保证内。

回退业务版本前保全候选新增数据与鉴权记录；禁止切回旧 Redis 鉴权代码并继续使用当前签名密钥。若需回退，形成单独可审阅步骤及全体重新登录边界，不执行破坏性 down 迁移。自动“修复空表”与自动重建 authority 均禁止。

## 5. 验收合同

必须有真实隔离 PG 与非 owner 运行角色的集成测试，测试库与人工候选库分开：

1. 未配置存储、缺 authority/表、错误权限、PG 不可达、提交结果不确定均不放行、不签发、不返回撤销成功。
2. 两个独立 API 实例：一个撤销，另一个拒绝；正常 API/PG 重启后继续拒绝；Redis 失效及清空不改变结果。
3. 同一 refresh 多连接并发争抢，恰好一次消费成功；签发失败后旧 token 不能再消费；重启后仍拒绝。
4. token 类型/签名/expiry/tenant/actor 验证、tenant 上下文不匹配、RLS 拒绝、伪造身份、重复撤销及绝对到期边界。
5. 旧源 access/refresh 无论旧 Redis 状态是否存在均拒绝；新候选登录与正常受限业务通过；无日志/API/数据库原文 token 泄漏。
6. 在独立测试目标演练 PG 旧备份恢复后停用与密钥/authority 更新，证明旧 token 被拒绝；不在共享源故障注入。
7. 对已撤销 access 和已消费 refresh 构造签名字节相同的非规范 base64url 变体、空白与填充变体，全部在摘要/状态查询前拒绝；规范 token 的正常签发和验证保留。

构建与受影响 authentication/bootstrap/middleware/common handler 测试均通过；现有 Redis 专属测试改为 PG 对应行为测试，不删除单次消费、跨进程、安全失败等原有断言。迁移注册/checksum/真实角色验证独立审阅。只跑 mock 不计 B3 关闭。

## 6. 交付与职责

A 在独立安全修复分支/worktree 实现，先提供失败断言，完成受影响验证与独立审阅；与 B2 依次集成，冲突以鉴权最早就绪检查和无副作用构造为准，固定新 CandidateSHA 和测试证据。修改架构安全摘要时同步 AGENTS.md/CLAUDE.md，操作步骤只放开发/运行文档。

B 基于新 SHA 重审全部待执行迁移、权限及启动配置，负责隔离实例持久化、显式 authority/secret 准备和故障演练。恢复/迁移的实际操作仍受 T3 准入；新增模型的批准不是共享源写入许可。

详细书面设计尚待审阅，未创建迁移、未运行新鉴权逻辑，T3/T4/T5 仍未放行。A/B 前置均完成后再提出实际备份窗口，旧拟议时间不自动执行。

## 7. 独立设计审阅

未继承主会话历史的独立 reviewer 对两份前置设计、T2 revision-2 和固定 CandidateSHA 源码只读复核。首轮发现一项 Important：原 token 字符串摘要未约束签名的等价编码；主审核对本地 jwt v5.3.1 源码后补充第 2 节规范编码与第 5 节变体断言。独立复审确认该项在设计层关闭，无剩余 Critical/Important/Minor，可提交书面审阅。没有执行鉴权测试、迁移或运行验证，不能据此关闭 B2/B3。
