# Candidate PostgreSQL Auth State Implementation Plan

> **2026-09-14 范围收口：** 当前剩余任务、优先级及完成状态唯一入口为[候选交付剩余清单](2026-09-14-itsm-candidate-remaining-delivery.md)。本文件保留技术合同与历史证据；旧未勾选复合项不能直接用于重新立项，后续待办不在此追加。

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（书面设计已确认；实施未开始）。

**Goal:** 关闭 T2 B3，以 PostgreSQL 单一权威持久化 access 撤销与 refresh 消费，并形成固定新 CandidateSHA。

**Architecture:** 既有 authentication 验证服务产生不可伪造的已验证主体，唯一 PostgreSQL repository 持久化两类安全事实。所有认证与签发入口依赖同一 authority 就绪检查；移除 Redis/内存生产回退，原 tenant/RBAC 服务继续负责授权。

**Tech Stack:** Go、golang-jwt v5.3.1、database/sql、PostgreSQL、既有 migration/Gin/Ent 测试与角色约束。

## Global Constraints

- 权威：[已接受设计](../specs/2026-09-12-itsm-candidate-auth-state-design.md)、AGENTS.md、工程治理及双 Agent 总计划。
- A 在 `codex/security/candidate-auth-state` 独立 worktree 实施；从 B2 已审阅交付 SHA 创建，实际 SHA 记录在交接，不能把文档 SHA 误作代码 SHA。B 继续拥有 WSL 运行配置/迁移操作。
- HMAC 默认禁用，不修改 nonce/租约/历史 Redis；无共享源故障注入、无 R(038)、无历史 backfill、无 .env 复制、无 main 推送/合并。
- 新候选 authority+JWT secret 必须与源不同；旧 source token 全部拒绝。运行 PG 身份非 owner、无 BYPASSRLS/DDL/TRUNCATE/DELETE，事务 synchronous_commit=on。失败不签发、不回退。
- 真实集成用独立测试库，不能使用人工候选库作可重置 fixture。缺少可用隔离实例时保持真实验证未完成，不准借用共享数据库。
- 顺序 A1→A2→A3→A4→A5。每项写可重现 RED，再做最小实现与 GREEN，独立提交。最终集成之后由 B 重核 T3，不自动预约备份时间。

## 文件与接口地图

- 修改 `authentication/{token.go,revocation.go,refresh_token.go}`：验证身份、严格编码、唯一存储调用；删除 Redis/内存生产实现。
- 新增 `authentication/{validated_credential.go,postgres_token_state.go,postgres_token_state_test.go,canonical_token_test.go}`；真实跨 package 测试在 `tests/integration/auth_token_state_postgres_test.go`。
- 新增 `migration/auth_token_state.go`，注册于 `migration/migrations.go`、`migration/migration_plan.go`；新增真实迁移角色测试 `tests/integration/auth_token_state_permissions_test.go`。
- 修改 `internal/bootstrap/app.go`、`config/config.go`、`handlers/common/{service.go,handler.go}`、`middleware/` 中既有 token 入口；`rg` 清单覆盖其余所有 token 签发/验证调用点。

统一接口（A1/A2 定义；不对包外开放 VerifiedCredential 字段或构造器）：
```go
type VerifiedCredential struct {
    digest string
    tenantID int
    actorID int
    expiresAt time.Time
    purpose string
}
type TokenStateStore interface {
    Ready(context.Context) error
    IsRevoked(context.Context, *VerifiedCredential) (bool, error)
    Revoke(context.Context, *VerifiedCredential) error
    ConsumeRefresh(context.Context, *VerifiedCredential) error
}
func NewPostgresTokenStateStore(db *sql.DB, authorityID string) (TokenStateStore, error)
func validateCanonicalCompact(raw string) error
```
`VerifiedCredential` 仅 authentication 内由严格验签函数创建；外部 handler 不能从 tenant/header/raw digest 构造。access/refresh 具体用途必须与方法匹配。已有 ValidateAccessToken/RefreshTokenConsumer 调整为使用该合同，不能保留旧 raw-token 回退接口。

## A1：唯一编码与已验证身份

**Files:** `authentication/token.go`、新 credential/canonical 文件、现有 token/refresh 测试。

- [ ] 写以下纯测试；还需生成真实 HS256 token 后改变 signature 最后一字符的未使用位，证明基线接受等价签名变体，修复后拒绝：
```go
func TestCanonicalCompactRejectsAlternativeEncoding(t *testing.T) {
    for _, raw := range []string{"e30.e30.AB", "e30.e30.AA=", "e30.e30.AA\n", "e30.e30"} {
        if err := validateCanonicalCompact(raw); err == nil { t.Fatalf("accepted %q", raw) }
    }
}
```
- [ ] 运行 `go test ./authentication -run 'TestCanonicalCompact|TestAccessToken|TestRefreshToken' -count=1` 记录 RED。
- [ ] 严格检查三段、无空白/填充、每段 decode/reencode 一致，再 `jwt.ParseWithClaims` 指定 `jwt.WithStrictDecoding()`、`jwt.WithValidMethods([]string{"HS256"})`。不将非规范 token 标准化后接受。
- [ ] 要求 expires_at、正数 actor/tenant、非空 role/username，已验证主体绑定原 token SHA256/期限/用途；tenant 上下文由 claims 设置并清除 system bypass，已有 tenant 冲突拒绝。拒绝空 VerifiedCredential 与方法用途错误。
- [ ] 补充合法新 token、过期/缺期限/错误类型/错误算法/其他租户用例，运行 GREEN；提交 `fix: enforce canonical verified token identities`。

## A2：唯一 PostgreSQL 记录与受限角色

**Files:** 新 postgres_token_state.go、migration/auth_token_state.go、migration 注册/规划、上述两份真实集成测试。

- [ ] 写 `TestPostgresRevocationAcrossConnections`：两独立连接共用 authority，一方撤销另一方拒绝；`TestRefreshConsumptionExactlyOnce`：32个独立连接/事务竞争同 credential，恰好1成功、31已消费；运行记录 RED。
- [ ] SQL 表固定 `auth_state_authorities` 与 `auth_token_states`，唯一键 `(authority_id,purpose,token_digest)`，purpose CHECK、绝对 expires_at、actor/tenant、recorded_at、authority FK；运行角色仅 SELECT/INSERT，无 UPDATE/DELETE。authority 只能维护者显式建立，缺失或不匹配 Ready 返回错误。
- [ ] 注册 `040_auth_token_state`，先重核未占用。放在039之后、手动038之前，不改 frozenMigrationVersions 和旧 checksum；写测试确认新迁移无需038而保持037/032–036/039前置，旧账本校验仍严格。
- [ ] Repository 建事务、校验 authority、由 credential 设置 tenant、核对会话设置；只用参数化 SQL。消费核心：
```sql
INSERT INTO auth_token_states
 (authority_id,purpose,token_digest,tenant_id,actor_id,expires_at,recorded_at)
VALUES ($1,'refresh_consumption',$2,$3,$4,$5,CURRENT_TIMESTAMP)
ON CONFLICT (authority_id,purpose,token_digest) DO NOTHING
RETURNING token_digest;
```
返回无行表示已消费；任何查询/提交错误表示不可用，不能混算为无行。撤销同键重复幂等，expires_at 不更新；撤销查询必须先通过同事务的 authority/tenant 校验。
- [ ] RLS 测试分别断言正确tenant、错误tenant、缺少tenant、权限拒绝和错误策略不能误判为“未撤销”。迁移维护只读可验证的策略定义/权限指纹；Ready 对策略/角色不匹配拒绝，运行角色无权改变它们。未知 DB 完整性不是空白新状态。
- [ ] 真实连接失败、缺表/authority、重复撤销、expiry、提交不确定测试 GREEN；`synchronous_commit=on` 纳入连接事务断言；不实现定时清理。提交 `feat: persist token security state in postgres`。

## A3：启动、认证、签发及注销统一接入

**Files:** authentication/revocation.go、refresh_token.go、token.go；bootstrap/app.go；config/config.go；handlers/common/service.go、handler.go；已有 middleware token 测试。

- [ ] 用 `rg -n 'IssueSessionTokens|GenerateAccessToken|GenerateRefreshToken|ValidateAccessToken|RevokeAccessToken|NewRedisRefreshTokenStore|ConfigureAccessTokenRevocationRedis'` 生成调用点清单，逐项确认是否生产入口；测试 helper 保留为测试，不作为 production fallback。
- [ ] 写 `TestAuthStartupFailsWithoutAuthority`、`TestLogoutDoesNotSucceedOnCommitFailure`、`TestRefreshDoesNotIssueAfterUncertainCommit`。以 unavailable store 开始，HTTP必须503/明确错误且不返回token、不清成“注销成功”；运行 RED。
- [ ] bootstrap 在任何运行 Start 之前注入唯一 PG store 并 Ready；移除 Redis Ping 决定撤销后端的分支，删除默认内存实例。缺 store 的默认值是不可用，不是空内存。
- [ ] 登录及所有 session 签发调用先检查 Ready；refresh 保持验证→当前会话授权→消费提交→签发，签发前复核expiry。消费成功而签发失败不恢复旧 refresh，客户端重新登录。注销持久化成功后才清 cookie并返回成功。
- [ ] 基础鉴权入口只接受 VerifiedCredential，不向底层传不可信 header tenant。Ready/存储失败返回既有基础设施错误，验证/已撤销返回既有鉴权错误；SQL/DSN/token 不得出现在对外响应，日志只记安全分类。
- [ ] 将 Redis 专属用例转换为 PG 同等断言，memory fake 放 *_test.go；不得删掉跨实例、单次消费或错误断言来修测试。运行 `go test ./authentication ./middleware ./handlers/common ./internal/bootstrap -count=1` GREEN，提交 `refactor: use one durable authentication state authority`。

## A4：故障、切换与恢复验收

**Files:** `tests/integration/auth_token_state_postgres_test.go`、`docs/DEVELOPMENT_GUIDE.md`；B 运行手册由 B 根据本合同更新，A 不修改其配置。

- [ ] 在独立测试目标运行两个API进程，撤销/消费后重启API与PG，继续拒绝；记录真实角色与持久化配置。Redis断连、清空及丢卷只发生在专属测试实例，PG状态不变，不能让业务依赖失败导致测试根本没走到鉴权却声称证明拒绝。
- [ ] 分别测试store本身与HTTP：store断言明确“已撤销/已消费”；HTTP可因依赖不就绪503，但不得200。保持正常新登录和新WorkItem路径成功的正向对照。
- [ ] 用全新candidate secret/authority证明源token拒绝。Redis旧记录保全，不导入PG、不延长TTL、不启用HMAC。
- [ ] 在测试实例演练旧PG快照恢复：停所有鉴权/签发进程→保全当前新增状态→恢复→更换authority及签名密钥→重新准入→旧token拒绝、新登录通过。缺少具体恢复步骤不能自动恢复旧配置。此测试不是对共享源的操作授权。
- [ ] 文档写明PG选择性恶意篡改不由空行检测保证；缺状态完整性必须停用并换密钥。记录不实现清理、不支持无重新登录的旧代码回退。运行新集成测试GREEN并提交 `test: verify durable token state failure and recovery`。

## A5：独立审查、候选集成与交接

**Files:** AGENTS.md/CLAUDE.md、开发指南、A T1交接；原设计状态仅在代码和相应门禁具备证据后更新，不因计划打勾自动implemented。

- [ ] 运行 `go build ./...`、`go test ./authentication ./middleware ./handlers/common ./internal/bootstrap ./migration -count=1` 及A2/A4真实PG测试；核对skip，不把缺基础设施计为通过。
- [ ] 更新两份Agent文档一致的安全摘要与签发/恢复规则；只改与本修复相关条目。无业务源码格式化清理、无历史报告删除。
- [ ] 请求独立reviewer核对严格编码、tenant/RLS空结果、并发提交、无Redis回退、新增迁移和切换语义，修正全部Critical/Important。提交 `docs: document durable authentication authority`。
- [ ] 在A的候选集成分支纳入B2/B3已审阅提交，保留原WorkItem/主题历史。逐项核对bootstrap/migration冲突，重跑受影响后端测试和构建；若前端未改，说明继承的前端证据及其代码指纹，不虚构新全量结果。
- [ ] 生成固定CandidateSHA，T1补充交接包括两个修复SHA、新增迁移SQL/checksum、scope/authority配置合同、Linux/真实目标未验证项。候选代码SHA与随后文档SHA分开。
- [ ] 按既有bundle协议封装分支与脱敏证据，校验本机包；需交给B时使用已有授权传输渠道，只写其handoff私有目录，不修改其checkout或环境。
- [ ] B消费新SHA后重做受影响T2准入，准备具体备份窗口和T3资源/角色验证。A收到T3 EnvironmentRevision后才执行T4；T5重启60分钟仍属独立后继门禁。

## 自审覆盖

设计§1/2→A1/A2/A3；§3→A1/A3；§4→A4/A5；§5→A1–A4；§6→A5。执行范围另有S1–S6计划，不用本计划替代。两份计划共享S2生命周期和唯一迁移目录，实施顺序固定，任何新SHA都使受影响旧准入证据需要重核。
