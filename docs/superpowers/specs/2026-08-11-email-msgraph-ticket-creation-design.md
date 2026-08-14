# 邮件建单（MS Graph）设计文档

## 背景与问题

ITSM 计划提供一个共用支持邮箱（如 `support@kerrylogistics.com`），用户发邮件到这个邮箱应自动创建工单。仓库里已经有一版邮件连接器代码（`connector/builtin/email/email.go` + `service.go`），基于 IMAP 拉信 + SMTP 发信，实现完整（解析、正文清洗、关键词分类匹配），但**从未接入运行时**：`internal/bootstrap/app.go` 没有 blank-import 这个包，`init()` 从未执行；它依赖的 `TicketCreator` 接口在全代码库里零实现；`MatchTemplate()` 算出结果后也没被用上。这是一次单独会话（本次之前）审计连接器子系统时发现的。

用户明确要求：**不用这版 IMAP 实现，改用 MS Graph API**，原因（讨论中达成一致）：

1. Exchange Online 自 2022 年起默认禁用 IMAP/SMTP 的 Basic Auth，很多企业租户 IMAP 根本连不通，需要 IT 专门开例外
2. Graph 的应用权限（Application permission）可以限定在具体某个邮箱上（Application Access Policy），IMAP 账号密码是"全权限"，泄露风险更大
3. Graph 调用记录在 Azure AD 审计日志里，IMAP 协议层面没有可观测性，更符合 CLAUDE.md"高风险面必须可追踪"的要求

本设计只覆盖"邮件建单"（收信→建工单）。"邮件通知"（工单状态变更/SLA 告警/密码重置邮件的发送）是另一个已单独定过范围、工作量小得多的独立任务（`internal/bootstrap/app.go` 里补上 `cfg.SMTP` → `service.NewEmailService` → `SetEmailService` 的接线），不在本次改动范围内，但本设计第二部分末尾会做一次"自动确认回信"，与那个任务的发信路径不是同一套（见"范围与取舍"里的说明）。

## 现状核实

- **连接器插件契约**：`connector/connector.go` 定义 `Connector` 接口（`Manifest/Init/Send/HealthCheck/Close`），`connector/registry.go` 是工厂注册表，内置连接器在 `init()` 里 `MustRegister`。`connector/manager.go`（`Manager`）按 `tenantID/name/provider` 管理运行时实例，**纯内存，无 DB 持久化**——所有现有连接器（feishu/wecom/dingtalk/webhook/console）重启后都需要管理员重新在 UI 上填一次凭据，这是系统性的既有限制，不是本次改动引入的新问题。
- **两套连接器生命周期路径并存**：`ConnectorController`（`/api/v1/connectors/configs`）直接走 `Manager.Provision/Revoke`，是目前唯一"配置了就真的能跑起来"的路径；`service/marketplace/service.go` 的 `InstallItem/UninstallItem` 落库到 `TenantInstallation` 表，但两处都有 TODO，从未真正调用 `connector.Manager`。**本设计选择接入前者（`ConnectorController`/`Manager`）**，与 feishu/wecom/dingtalk/webhook 保持一致；不解决双轨制这个架构债，那是独立的、更大范围的后续工作。
- **凭据脱敏已经做对**：`connector_controller.go` 的 `maskConfig()` 会在返回前端前把所有凭据值替换成 `"******"`，新连接器的 `Settings`/`Credentials` 会自动享受这个保护，不需要额外开发。
- **既有的"连接器特有逻辑走专属 controller"先例**：通用生命周期在 `connector_controller.go`，飞书特有的回调签名/nonce 校验在 `feishu_controller.go`。本设计的邮件轮询协调器沿用同一约定。
- **Azure AD 集成已存在但不能直接复用**：`handlers/auth/azure.go` 是用户登录 SSO 用的 `authorization_code` delegated 流程（换取代表某个登录用户的 token，用于 `/me` 相关调用）。读一个共享邮箱需要 `client_credentials`（app-only）流程，grant_type 不同、场景不同，两者只是共用同一套 `AZURE_TENANT_ID/CLIENT_ID/CLIENT_SECRET` 环境变量命名习惯，代码不复用。
- **AI 分派能力已存在且带内置 fallback**：`service/triage_service.go` 的 `SuggestForTenant(ctx, title, description, tenantID) TriageResult`（`TriageResult{Category, Priority, AssigneeID, Confidence, Explanation, SuggestedFix}`）内部已经处理"LLM 失败→关键词兜底"（`keywordBasedSuggest`），符合 CLAUDE.md"AI 失败必须有确定性兜底"的要求，不需要在邮件这边重新写一套。它目前的调用方（`handlers/ai/service.go`）都是把结果当"建议"展示给人工看，不做静默写回；本设计是全代码库第一处在无人工介入的情况下把 AI 结果的一部分（`Priority`）直接写入工单字段的地方，具体应用范围（哪些字段应用、哪些只作为建议）见下方"决策记录"第 6 条。
- **去重/幂等所需字段不存在**：`ent/schema/ticket.go` 目前只有一个通用 `source` 字符串字段（如 `manual`/`service_catalog`），没有可用于"这封邮件是否已经建过工单"判断的外部消息 ID 字段。
- **建单接口**：`service/ticket_service.go` 的 `CreateTicket(ctx, req *dto.CreateTicketRequest, tenantID int) (*ticket.Ticket, error)`；`dto.CreateTicketRequest.RequesterID` 是**必填的、指向真实 `User` 记录的整数外键**，不支持"外部邮箱字符串"这种松散身份。用户确认 ITSM 是企业内部系统，邮件发件人必然是系统内已注册用户，按邮箱查不到时属于不应发生的边界情况，只需要防御性地记日志并跳过，不需要设计"自动建外部账号"之类的分支。
- **HTTP 客户端可测试性约定**：`connector/builtin/dingtalk/client.go` 的 `NewClient(appKey, appSecret, agentID, baseURL string)` 把 `baseURL` 做成可覆盖参数（默认指向真实 API），测试用 `httptest.NewServer` 打桩。本设计的 Graph 客户端沿用同一约定。

## 决策记录

以下是设计讨论过程中逐项确认的决定，附理由，供实现阶段和未来读者对照：

1. **认证方式：app-only（`client_credentials` + 应用权限）**，不用某个真人账号的 delegated 流程。管理员在 Azure AD 后台注册一个应用、授予 `Mail.Read`/`Mail.Send` 应用权限并走一次管理员同意（一次性运维工作），服务端凭 client secret 自行换 token，不依赖任何人登录状态、不会因为某个真人的 refresh token 过期而中断。
2. **收信方式：定时轮询 `/messages/delta`**，不用 Graph Webhook 订阅推送。理由：Webhook 需要暴露公网回调地址、处理订阅到期续订（邮件资源订阅最长约 3 天），运维复杂度明显更高；轮询延迟最多一个轮询间隔（默认 60 秒），换取不需要公网入口和续订任务，且与现有连接器"定时轮询"的整体风格一致。如果后续对实时性有硬性要求，可以在不改变整体架构的前提下换成 Webhook。
3. **`deltaLink` 不持久化**，只存内存。服务重启后下一次轮询会重新做一次全量 delta 扫描，可能重新"看到"已处理过的邮件，但第 5 条的去重字段能保证不会因此产生重复工单，代价只是一次性的多余 API 调用。这与现有连接器"重启丢配置"的既有限制保持一致，本设计不解决持久化问题。
4. **接入路径：走 `ConnectorController`/`Manager`**，不碰 `service/marketplace/service.go` 的 TODO，不新建独立配置表/独立后台任务。管理员在现有 `/admin/connectors` 页面配置，无需新前端页面（该页是根据 registry 动态渲染的市场列表 + 供给表单）。
5. **新增 `Ticket.external_message_id`（nullable string）字段**，用于按 `tenant_id + external_message_id` 判断某封邮件是否已经建过工单，是本设计**唯一的 schema 改动**。选择加字段而不是复用 `source`/`tags`/评论等已有机制,因为这是一个需要唯一性查询的标识符,塞进免费文本字段（如描述或评论）会让去重查询变成低效的字符串匹配,而 tags 本身语义是给人看的分类标签,不适合承载系统内部去重键这种职责。配套地，`dto.CreateTicketRequest` 需要新增对应的可选字段 `ExternalMessageID`，`TicketService.CreateTicket` 在创建 `ent.Ticket` 时需要把它透传写入新字段（目前该请求结构体和创建逻辑里都没有这个字段，属于本设计新增的一部分，不是已有能力）。
6. **AI 分派结果里，`Priority` 直接应用到工单，`Category` 不直接应用**。`TriageResult.Priority` 是与 `ticket.Priority` 同一枚举集合、已校验过的值，映射清晰可靠，直接写入 `CreateTicketRequest.Priority`。`TriageResult.Category` 则不然——自检发现 `TicketService.CreateTicket` 实际只读取 `req.CategoryID`（`*int`，指向 `TicketCategory` 表记录），完全不使用 `req.Category`（string）字段；而 `TriageResult.Category` 是 LLM 输出的通用字符串（如 `network`/`database`/`security`，来自 `service/triage_service.go` 里一个跟 `TicketCategory` 表无关的固定小词表），两者之间没有现成的名称/ID 映射，建一个可靠的映射本身是需要额外设计判断的独立小任务（精确匹配？管理员可配置映射表？），不在本次讨论范围内，不能顺手假设它存在。因此：`Category`/`Priority`/`Confidence`/`Explanation` 都写入建单后追加的系统评论供人工参考，但只有 `Priority` 真正写入工单字段，`CategoryID` 留空（走 `CreateTicket` 已有的默认分类兜底逻辑）。这也更贴合 CLAUDE.md"AI 建议是决策支持、不是静默权威"的原则——分类这块因为没有可靠映射，本来就应该是"建议、人工确认"而不是"静默写入一个可能文不对题的分类"。
7. **建单成功后自动回一封确认信**（通过 Graph `sendMail`，从共用邮箱回复原发件人，告知工单号），复用 `connector/builtin/email/email.go` 里已经写好的 `defaultReplyTemplate` 文案风格，但改用 Graph API 发送。这是"邮件建单"闭环的一部分，跟后续"邮件通知"任务里 `service/email_service.go` 的通用状态变更通知是两条独立的发信路径（不同触发时机、不同收件人来源：一个是回复原始发件人确认收到，一个是工单状态变化时按用户偏好通知），彼此不冲突、不重复。
8. **新建独立连接器包 `connector/builtin/msgraph/`**，不修改/不复用现有 `connector/builtin/email/`。旧的 IMAP 实现保留在原地但不注册（不在 bootstrap 里 blank-import），作为闲置代码保留、不删除（其单元测试 `email_test.go` 也保持不变），以防未来需要参考或切回。

## 目标架构

### 部分一：MS Graph 连接器（收发信基础能力）

新增 `connector/builtin/msgraph/`：

- `Manifest()`：`Name: "msgraph-email"`，`Type: connector.TypeEmail`，`Capabilities: [CapSendMessage, CapReceiveMessage, CapCreateTicket]`，`RequiredPermissions: ["connector:write", "ticket:write"]`（与旧 email 连接器一致）。
- `Config.Settings` 新增字段：`azure_tenant_id`、`azure_client_id`、`azure_client_secret`（走连接器通用的 `Credentials`/`Settings` 脱敏机制）、`mailbox`（共享邮箱地址）、`poll_interval_seconds`（默认 60）。
- Token 获取：`POST https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`，`grant_type=client_credentials`，`scope=https://graph.microsoft.com/.default`；token 在内存里按 `expires_in` 缓存，过期前自动重取。AAD 端点 base URL 可覆盖（测试用）。
- 收信：`GET https://graph.microsoft.com/v1.0/users/{mailbox}/mailFolders('inbox')/messages/delta`；首次调用保存返回的 `@odata.deltaLink`（内存），后续轮询直接请求该 `deltaLink`，只处理增量。Graph API base URL 同样可覆盖。
- 发信：`POST /users/{mailbox}/sendMail`，用于第二部分的自动确认回信。
- `HealthCheck()`：尝试获取一次 token，成功即视为健康。

### 部分二：建单流程 + AI 分派 + 去重

新增 `Ticket.external_message_id`（nullable string，ent schema 迁移，需要 `go run -tags migrate main.go` 或等效的 ent generate + 迁移步骤）。

新增 `EmailPollingCoordinator`（`connector/builtin/msgraph/coordinator.go`），构造时注入 `*ent.Client`、`*service.TriageService`、以及一个建单适配器（对 `TicketService.CreateTicket` 的薄封装）。对每个轮询到的新邮件执行：

1. 按 `tenant_id + LOWER(From)` 查 `User`；查不到记 warning 日志、跳过（不建单，不视为错误重试）。
2. 按 `tenant_id + external_message_id`（Graph 消息的 `internetMessageId`）查是否已存在对应工单；存在则跳过（幂等保护，覆盖"重启后重新全量扫描"的场景）。
3. 调用 `triageService.SuggestForTenant(ctx, subject, cleanedBody, tenantID)` 得到 `TriageResult`。
4. 组装 `dto.CreateTicketRequest{Title: subject, Description: cleanedBody, Priority: result.Priority, RequesterID: user.ID, Source: "email", ExternalMessageID: msg.InternetMessageID}`（不设置 `CategoryID`，原因见决策记录第 6 条），调用 `TicketService.CreateTicket`。
5. 建单成功后，用现有工单评论接口追加一条系统评论：`"AI 分派参考：建议分类=%s（未自动应用，请人工确认），优先级=%s（已应用），置信度=%.0f%%，理由=%s"`。
6. 通过 Graph `sendMail` 从共用邮箱回复原发件人，套用 `defaultReplyTemplate` 风格的确认信（工单号、标题、状态）。

正文清洗（去除引用回复、签名等）复用 `connector/builtin/email/service.go` 里已经写好的 `cleanEmailBody` 逻辑，回信文案复用 `connector/builtin/email/email.go` 里的 `defaultReplyTemplate` 文案——两者都是不依赖旧包私有状态的纯函数/常量，按同样的方式复制一份到 `msgraph` 包内（不导入旧包，避免两个连接器产生编译期耦合；旧包保留但不注册，见决策记录第 8 条）。

### 部分三：接线与生命周期

- `ConnectorController`（`controller/connector_controller.go`）新增一个可选的 `*msgraph.EmailPollingCoordinator` 依赖（构造函数参数或 setter，沿用项目里 `Set*Service` 的既有模式）。`Provision` 在 `manager.Provision(...)` 成功后，若 `cfg.Name == "msgraph-email"`，调用 `coordinator.Start(ctx, tenantID, cfg)`（内部先 `Stop` 同租户已有的轮询 goroutine 再起新的，处理"改配置"场景）；`Revoke`/禁用同理调用 `coordinator.Stop(tenantID)`。
- `internal/bootstrap/app.go`：blank-import `connector/builtin/msgraph`（注册到 registry，自动出现在 `/admin/connectors` 市场列表里）；构造 `EmailPollingCoordinator` 并传给 `NewConnectorController`。**不修改** `service/marketplace/service.go`。
- 前端：无需改动，`/admin/connectors` 是根据 registry 动态渲染的通用市场+配置表单页面。

## 测试计划

- `connector/builtin/msgraph/*_test.go`（参照 `dingtalk_test.go`/`feishu_test.go` 的组织方式，用 `httptest.NewServer` 打桩 AAD token 端点和 Graph API 端点，不做真实网络调用）：
  - token 获取与过期缓存
  - delta 响应解析、`deltaLink` 增量轮询
  - 消息→`EmailMessage` 的字段映射
  - `sendMail` 请求体构造
- `EmailPollingCoordinator` 的建单逻辑（可注入 mock 的 `TicketCreator`/`TriageService` 接口）：
  - 正常建单路径（用户存在、triage 成功、字段映射正确）
  - 按 `external_message_id` 去重跳过
  - 按邮箱查不到用户时跳过且不报错
  - triage 失败时的行为（依赖 `TriageService` 自身的 fallback，协调器只需确认不会因 triage 报错而丢失邮件/panic）
- ent schema 迁移：新增字段后跑 `go generate ./ent` 确认生成代码无误，`go test ./ent/...` 通过。
- 按 CLAUDE.md 验证预期：先跑 `connector/builtin/msgraph` 与相关 service 包的窄范围测试，再跑 `cd itsm-backend && go test ./...`。

## 范围与取舍（明确排除的内容）

- 不解决 marketplace 与 `ConnectorController` 的双轨制生命周期架构债。
- 不做连接器配置的 DB 持久化（重启后需要管理员重新配置，与现有连接器一致）。
- 不实现"邮件通知"（工单状态变更/SLA 告警/密码重置邮件的通用发送链路）——独立任务，见背景部分说明。
- 不处理"发件人不是系统内已注册用户"的复杂分支（自动建外部账号等）——用户确认企业内部系统场景下不会发生，只做防御性日志+跳过。
- 不引入 Graph Webhook 订阅推送。
- 不新增 AI 建议的独立审计表（复用工单评论机制）。
