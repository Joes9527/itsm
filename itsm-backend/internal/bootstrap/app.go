package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	intakecreation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	_ "itsm-backend/connector/builtin/console"
	_ "itsm-backend/connector/builtin/dingtalk"
	_ "itsm-backend/connector/builtin/feishu"
	msgraph "itsm-backend/connector/builtin/msgraph"
	_ "itsm-backend/connector/builtin/webhook"
	_ "itsm-backend/connector/builtin/wecom"
	"itsm-backend/connector/marketplace"
	"itsm-backend/controller"
	marketplaceController "itsm-backend/controller/marketplace"
	"itsm-backend/pkg/eventbus"
	marketplaceService "itsm-backend/service/marketplace"

	"itsm-backend/database"
	"itsm-backend/docs"
	"itsm-backend/ent"
	"itsm-backend/ent/tenant"
	"itsm-backend/ent/user"
	"itsm-backend/handlers"
	"itsm-backend/handlers/ai"
	"itsm-backend/handlers/change"
	"itsm-backend/handlers/cmdb"
	domainCommon "itsm-backend/handlers/common"
	"itsm-backend/handlers/delegated_execution"
	"itsm-backend/handlers/knowledge"
	"itsm-backend/handlers/known_error"
	"itsm-backend/handlers/problem"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/handlers/service_request"
	"itsm-backend/handlers/sla"
	"itsm-backend/handlers/standard_change"
	"itsm-backend/internal/initialization"
	"itsm-backend/middleware"
	"itsm-backend/migration"
	"itsm-backend/pkg/seeder"
	repository_ticket "itsm-backend/repository/ticket"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/router"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

type bpmnCallbackWorker interface {
	RunCallbackOutboxWorker(context.Context, string, time.Duration)
}

type ticketNotificationWorker interface {
	RunDeliveryWorker(context.Context, string, time.Duration)
}

type Application struct {
	Cfg                      *config.Config
	Logger                   *zap.SugaredLogger
	DBClient                 *ent.Client
	systemClient             *ent.Client
	Router                   *gin.Engine
	Embedder                 service.Embedder
	VectorStore              *service.VectorStore
	callbackWorker           bpmnCallbackWorker
	notificationWorker       ticketNotificationWorker
	outboxDeliveryWorker     kafOutboxRunner
	toolQueue                toolQueueCloser
	startBackgroundTasksFunc func(context.Context)
}

type toolQueueCloser interface {
	Close()
}

// prepareTicketCCIndexMigration removes the pre-partial-index definition.
// Ent reuses this index name and additive schema creation cannot replace an
// existing unconditional index with its partial equivalent.
func prepareTicketCCIndexMigration(ctx context.Context, db *sql.DB, logger *zap.SugaredLogger) error {
	if db == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS ticketcc_tenant_id_ticket_id_user_id`); err != nil {
		return fmt.Errorf("drop legacy TicketCC natural unique index: %w", err)
	}
	if logger != nil {
		logger.Debugw("legacy TicketCC natural index removed when present")
	}
	return nil
}

type kafOutboxRunner interface {
	Run(context.Context)
}

// prepareRolePermissionTenantMigration upgrades installations created before
// role_permissions became tenant-scoped. Ent cannot add a required column to a
// populated table directly, so the compatibility step adds it as nullable and
// derives each value from the authoritative roles table first. Ent then applies
// the final NOT NULL contract in Schema.Create.
func prepareRolePermissionTenantMigration(
	ctx context.Context,
	db *sql.DB,
	logger *zap.SugaredLogger,
) error {
	if db == nil {
		return nil
	}

	var tableExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = 'role_permissions'
		)
	`).Scan(&tableExists); err != nil {
		return fmt.Errorf("inspect role_permissions table: %w", err)
	}
	if !tableExists {
		return nil
	}

	var columnExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'role_permissions'
			  AND column_name = 'tenant_id'
		)
	`).Scan(&columnExists); err != nil {
		return fmt.Errorf("inspect role_permissions.tenant_id: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin role_permissions tenant migration: %w", err)
	}
	defer tx.Rollback()

	if !columnExists {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE role_permissions ADD COLUMN tenant_id BIGINT`); err != nil {
			return fmt.Errorf("add role_permissions.tenant_id: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE role_permissions AS rp
		SET tenant_id = r.tenant_id
		FROM roles AS r
		WHERE rp.role_id = r.id
		  AND rp.tenant_id IS NULL
	`); err != nil {
		return fmt.Errorf("backfill role_permissions.tenant_id: %w", err)
	}

	var unresolved int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM role_permissions WHERE tenant_id IS NULL`,
	).Scan(&unresolved); err != nil {
		return fmt.Errorf("verify role_permissions.tenant_id: %w", err)
	}
	if unresolved > 0 {
		return fmt.Errorf(
			"cannot enforce role_permissions.tenant_id: %d rows have no matching tenant-scoped role",
			unresolved,
		)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role_permissions tenant migration: %w", err)
	}
	logger.Infow("role permission tenant migration prepared", "column_existed", columnExists)
	return nil
}

func newTenantGraphProvider(manager *connector.Manager) service.GraphProvider {
	return func(tenantID int) (service.GraphMailSender, string, bool) {
		c, ok := manager.Get(tenantID, "msgraph-email")
		if !ok {
			return nil, "", false
		}
		gc, ok := c.(*msgraph.GraphConnector)
		if !ok {
			return nil, "", false
		}
		return gc.GraphClient(), gc.Mailbox(), true
	}
}

func newTenantGraphInboundProvider(manager *connector.Manager) service.GraphInboundProvider {
	return func(tenantID int) (service.GraphInboundClient, string, bool) {
		conn, ok := manager.Get(tenantID, "msgraph-email")
		if !ok {
			return nil, "", false
		}
		graph, ok := conn.(*msgraph.GraphConnector)
		if !ok {
			return nil, "", false
		}
		return graph.GraphClient(), graph.Mailbox(), true
	}
}

func NewApplication() *Application {
	// 1. 初始化配置
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. 初始化日志系统
	logger := initLogger(&cfg.Log)
	sugar := logger.Sugar()
	middleware.SetLogger(sugar)
	LogDefaultCredentialRisks(
		GuardRuntimeCredentials(cfg.Deployment.Mode, cfg.JWT.Secret, cfg.Database.Password),
		sugar,
	)

	// 3. 生产权限必须以数据库为唯一事实来源并在缺失时 fail closed。
	// 只有显式 development/test/local 环境允许使用开发期硬编码回退。
	configurePermissionMode(os.Getenv("ENV"))

	if err := ValidateWebStartupConfig(cfg); err != nil {
		log.Fatalf("Unsafe web startup configuration: %v", err)
	}

	// 3. 初始化数据库连接（带 RLS 装饰器，默认 off 模式=透明）
	clients, err := database.InitRuntimeDatabases(&cfg.Database, &cfg.RLS, sugar)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	client, systemClient := clients.Tenant, clients.System
	// 6. 初始化服务层 & 控制器
	// 这部分代码量较大，为了简化，我们先在这里进行组装，后续可以进一步拆分为 wires / container

	// WorkItem numbering has one process-wide allocator; PostgreSQL remains the
	// authority for every Ticket, Incident, Problem, Change, and Requested Item path.
	numberAllocator := workitemnumber.NewPostgreSQLAllocator()

	// 初始化业务服务层
	incidentService := service.NewIncidentService(client, sugar)
	incidentService.RuleEngine().SetActorDirectory(systemClient)

	// 初始化 EventBus 事件总线
	eventBus, err := eventbus.NewWatermillEventBus(&cfg.Redis, sugar)
	if err != nil {
		sugar.Fatalw("Failed to initialize event bus", "error", err)
	}
	eventbus.SetGlobalEventBus(eventBus)
	sugar.Infow("Event bus initialized successfully")

	// 事件驱动审计订阅方：sla.breached / ai.triage.completed 写入 AuditLog
	auditSubscriber := service.NewEventAuditSubscriber(client, sugar)
	for _, topic := range service.AuditedEventTopics() {
		if err := eventBus.Subscribe(topic, auditSubscriber); err != nil {
			sugar.Warnw("failed to subscribe audit subscriber", "error", err, "topic", topic)
		}
	}

	// BPMN 子服务（必须在 TicketService 之前创建）
	processBindingService := service.NewProcessBindingService(client)
	concreteProcessEngine := service.NewCustomProcessEngine(client, sugar).(*service.CustomProcessEngine)
	var processEngine service.ProcessEngine = concreteProcessEngine
	processTriggerService := service.NewProcessTriggerService(client, processEngine)
	bpmnVersionService := service.NewBPMNVersionService(client, sugar)

	// 工单仓储层（V2 Repository 模式）
	ticketRepoImpl := repository_ticket.NewEntRepository(client, sugar)

	// Connector Manager / Registry / Market —— 连接器/插件/技能市场基础设施
	connectorManager := connector.NewManager(connector.Default(), sugar)
	connectorMarket := marketplace.New()
	connectorController := controller.NewConnectorController(connectorManager, connector.Default(), connectorMarket, sugar, client, systemClient)

	// Webhook 事件推送订阅方：sla.breached 按租户推送到已配置的 webhook 端点
	webhookSubscriber := service.NewWebhookEventSubscriber(connectorManager, sugar)
	for _, topic := range service.WebhookEventTopics() {
		if err := eventBus.Subscribe(topic, webhookSubscriber); err != nil {
			sugar.Warnw("failed to subscribe webhook subscriber", "error", err, "topic", topic)
		}
	}

	// 通知 / 审批 / SLA / 自动化 / 序列服务（V2 子服务）
	ticketNotificationService := service.NewTicketNotificationService(client, sugar)
	ticketNotificationService.SetConnectorManager(connectorManager)
	ticketNotificationService.SetDeliveryQueueClient(systemClient)
	// 邮件通知（Graph sendMail 为主，SMTP fallback）
	emailService := service.NewEmailService(service.EmailConfig{
		Host:     cfg.SMTP.Host,
		Port:     cfg.SMTP.Port,
		Username: cfg.SMTP.Username,
		Password: cfg.SMTP.Password,
		From:     cfg.SMTP.FromEmail,
		FromName: cfg.SMTP.FromName,
	}, sugar)
	// 延迟绑定 Graph 发信：发信时只查询当前租户的 msgraph 连接器。
	emailService.SetGraphProvider(newTenantGraphProvider(connectorManager))
	ticketNotificationService.SetEmailService(emailService)
	ticketSLAService := service.NewTicketSLAService(client, sugar)
	ticketAutomationRuleService := service.NewTicketAutomationRuleService(client, sugar)

	// V2 工单服务（构造函数注入）
	ticketService := service.NewTicketService(&service.TicketServiceConfig{
		ProcessTriggerService: processTriggerService,
		Repository:            ticketRepoImpl,
		Client:                client,
		Logger:                sugar,
		NotificationService:   ticketNotificationService,
		AutomationRuleService: ticketAutomationRuleService,
		SLAService:            ticketSLAService,
		ConnectorManager:      connectorManager,
	})

	// MSP 服务初始化
	mspAllocationService := service.NewMSPAllocationService(client, sugar)
	mspController := controller.NewMSPController(mspAllocationService, ticketService, sugar)

	// problemService and changeService removed - using Handlers with domain services instead

	// Release & Asset Management Services
	releaseService := service.NewReleaseService(client, sugar)
	releaseService.SetProcessTriggerService(processTriggerService)
	// 发布命令必须复用这个已装配 CallbackRegistry 的唯一 processEngine；临时创建
	// engine 会缺少领域服务注入并使 UserTask callback 明确失败。
	releaseService.SetProcessEngine(processEngine)
	assetService := service.NewAssetService(client, sugar)
	assetLicenseService := service.NewAssetLicenseService(client, sugar)
	// CMDB Services
	ciTypeService := service.NewCITypeService(client, sugar)
	ciAttributeDefinitionService := service.NewCIAttributeDefinitionService(client, sugar)
	ciHistoryService := service.NewCIHistoryService(client, sugar)
	ciTagService := service.NewCITagService(client, sugar)
	configurationItemService := service.NewConfigurationItemService(client, sugar, ciHistoryService, ciTagService)
	ciRelationshipService := service.NewCIRelationshipService(client, sugar)
	importExportService := service.NewCMDBImportExportService(client, sugar, configurationItemService, ciTagService)
	savedViewService := service.NewCMDBSavedViewService(client, sugar)
	// LLM/Embedding/VectorStore
	var embedder service.Embedder
	if cfg.LLM.Provider == "openai" || cfg.LLM.Provider == "" {
		embedder = service.NewOpenAIEmbedderWithConfig(cfg.LLM.APIKey, cfg.LLM.Endpoint, cfg.LLM.Model)
	} else {
		embedder = service.NewOpenAIEmbedder()
	}

	// Create LLM Gateway for AI services
	llmConfig := service.LoadLLMConfig()
	// 阻断1 修复：启动期检测 LLM API Key 配置状态。
	// - 占位符/空值：在开发环境 Warn，在生产环境终止启动（生产硬约束见 memory）。
	// - 真实密钥：仅输出 MaskSecret 脱敏值，便于诊断配置是否生效，绝不输出明文。
	if common.IsPlaceholderSecret(llmConfig.APIKey) {
		if os.Getenv("ENV") == "production" || os.Getenv("GIN_MODE") == "release" {
			sugar.Errorw("LLM API Key 未配置或为占位符，生产环境禁止以此状态启动",
				"provider", llmConfig.Provider, "api_key", common.MaskSecret(llmConfig.APIKey))
			// NewApplication 返回 *Application（无 error），生产硬约束用 log.Fatalf 终止。
			log.Fatalf("LLM API Key 未配置：生产环境必须设置真实的 LLM_API_KEY (provider=%s, api_key=%s)",
				llmConfig.Provider, common.MaskSecret(llmConfig.APIKey))
		}
		sugar.Warnw("LLM API Key 未配置或为占位符，AI 功能将降级为禁用",
			"provider", llmConfig.Provider, "api_key", common.MaskSecret(llmConfig.APIKey))
	} else {
		sugar.Infow("LLM API Key 已配置",
			"provider", llmConfig.Provider, "api_key_masked", common.MaskSecret(llmConfig.APIKey))
	}
	llmProvider := service.NewProviderFromConfig(llmConfig)
	// Token limiter guards against runaway prompt cost. Default 4000 rune-tokens/request
	// (roughly matches most model context windows). Override via llm.token_cap.
	tokenCap := llmConfig.TokenCap
	if tokenCap <= 0 {
		tokenCap = 4000
	}
	llmLimiter := service.NewFixedWindowLimiter(tokenCap)
	sugar.Infow("LLM token limiter wired", "capacity_runes_per_request", tokenCap)
	llmGateway := service.NewLLMGateway(llmProvider, llmLimiter, nil, llmConfig.Provider)

	vectorStore := service.NewVectorStore(database.GetRawDB())
	ragService := service.NewRAGServiceWithAutoConfig(client, vectorStore, embedder, sugar)
	aiTelemetryService := service.NewAITelemetryService(database.GetRawDB())

	// 非阻塞初始化：向量扩展检测与 Embedding 管道预热
	// 如果 pgvector 扩展未就绪，RAG 功能自动降级为关键字搜索
	go func() {
		ctx := context.Background()
		if err := vectorStore.EnsureExtension(ctx); err != nil {
			sugar.Warnw("pgvector 扩展未就绪，RAG功能降级为关键字搜索", "error", err)
			return
		}
		sugar.Infow("pgvector 扩展初始化成功")
	}()

	// 控制器依赖
	incidentMonitoringService := service.NewIncidentMonitoringService(client, sugar)
	incidentAlertingService := service.NewIncidentAlertingService(client, sugar)
	incidentService.SetAlertCreator(incidentAlertingService)
	ticketDependencyService := service.NewTicketDependencyService(client, sugar)
	analyticsService := service.NewAnalyticsService(client, sugar)
	predictionService := service.NewPredictionService(client, sugar)
	slaForecastSkill := service.NewSLAForecastSkill(client, llmGateway, sugar)
	// 市场服务
	marketplaceSvc := marketplaceService.NewService(client, sugar)
	marketplaceCtrl := marketplaceController.NewController(marketplaceSvc, connectorManager)

	// Guidance sidecar for constrained JSON generation
	guidanceURL := os.Getenv("GUIDANCE_URL")
	if guidanceURL == "" {
		guidanceURL = "http://localhost:8091"
	}
	guidanceClient := service.NewGuidanceClient(guidanceURL, sugar)
	triageService := service.NewTriageServiceWithGuidanceAndSugaredLogger(llmGateway, guidanceClient, sugar)
	ticketAttachmentService := service.NewTicketAttachmentService(client, sugar)
	// 配置了 MinIO 则切换附件存储后端为对象存储；失败回退本地文件系统。
	if cfg.MinIO.Endpoint != "" {
		if minioStorage, err := service.NewMinioAttachmentStorage(
			cfg.MinIO.Endpoint, cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, cfg.MinIO.Bucket, cfg.MinIO.UseSSL,
		); err != nil {
			sugar.Warnw("failed to init MinIO storage, falling back to local filesystem", "error", err)
		} else {
			ticketAttachmentService.SetStorage(minioStorage)
			sugar.Infow("attachment storage backend: minio", "endpoint", cfg.MinIO.Endpoint, "bucket", cfg.MinIO.Bucket)
		}
	}

	// 从数据库恢复已配置的连接器（如 msgraph-email），避免进程重启后丢失
	if err := connectorController.LoadAll(tenantctx.SystemContext(context.Background(), "bootstrap:connectors", "load configured tenant connector registrations")); err != nil {
		sugar.Warnw("Failed to restore connectors from DB", "error", err)
	}

	rootCauseService := service.NewRootCauseService(client, sugar)
	// LLM/Embedding/VectorStore

	// AI Tools
	toolRegistry := service.NewToolRegistry(ragService, incidentService, configurationItemService, client)

	ticketController := controller.NewTicketController(ticketService, ticketDependencyService, database.GetRawDB(), client, sugar)
	ticketDependencyController := controller.NewTicketDependencyController(ticketDependencyService)

	ticketCommentService := service.NewTicketCommentService(client, sugar)
	ticketCommentController := controller.NewTicketCommentController(ticketCommentService, sugar)
	ticketAttachmentController := controller.NewTicketAttachmentController(ticketAttachmentService, sugar)
	ticketNotificationController := controller.NewTicketNotificationController(ticketNotificationService, sugar)
	// ticketNotificationService 已在 128 行创建并注入到 V2

	// General Notification Service & Controller
	notificationService := service.NewNotificationService(client)
	notificationController := controller.NewNotificationController(notificationService)

	// Notification Preference Service & Controller
	notificationPreferenceService := service.NewNotificationPreferenceService(client, sugar)
	notificationPreferenceController := controller.NewNotificationPreferenceController(notificationPreferenceService, sugar)
	ticketNotificationService.SetNotificationPreferenceService(notificationPreferenceService)

	ticketRatingService := service.NewTicketRatingService(client, sugar)
	ticketRatingController := controller.NewTicketRatingController(ticketRatingService, sugar)
	ticketViewService := service.NewTicketViewService(client, sugar)
	ticketViewController := controller.NewTicketViewController(ticketViewService, sugar)

	ticketAssignmentService := service.NewTicketAssignmentService(client, sugar)
	ticketAutomationRuleService.SetAssignmentService(ticketAssignmentService)
	ticketAutomationRuleService.SetNotificationService(ticketNotificationService)
	ticketAssignmentRuleService := service.NewTicketAssignmentRuleService(client, sugar)
	ticketAssignmentSmartService := service.NewTicketAssignmentSmartService(client, sugar, ticketAssignmentService, ticketAssignmentRuleService)
	ticketAssignmentSmartController := controller.NewTicketAssignmentSmartController(ticketAssignmentSmartService, ticketAssignmentRuleService, sugar)

	// Ticket Workflow Service & Controller
	ticketWorkflowService := service.NewTicketWorkflowService(client, sugar)
	ticketWorkflowController := controller.NewTicketWorkflowController(ticketWorkflowService, database.GetRawDB(), sugar)

	// Ticket Automation Rule Controller (service 已于 131 行预创建并注入 V2)
	ticketAutomationRuleController := controller.NewTicketAutomationRuleController(ticketAutomationRuleService, sugar)

	// Set notification service dependencies
	ticketService.SetNotificationService(ticketNotificationService)
	ticketCommentService.SetNotificationService(ticketNotificationService)
	ticketRatingService.SetNotificationService(ticketNotificationService)

	// 注入 TicketService 到 BPMN ticket_service_handler，
	// 让 ServiceTask 的状态更新走领域服务（状态机校验/通知/飞书同步），不再绕过直接改 Ent。
	// processEngine 的静态类型是 service.ProcessEngine 接口（未声明 CallbackRegistry()，
	// 该方法只加在具体实现 *service.CustomProcessEngine 上，避免影响接口的其他实现/测试假实现），
	// 所以这里先做一次类型断言。
	if cpe, ok := processEngine.(*service.CustomProcessEngine); ok {
		if h, ok := cpe.CallbackRegistry().GetHandler("ticket_service_handler").(*bpmn.TicketServiceTaskHandler); ok {
			h.SetTicketService(ticketService)
			h.SetNotificationService(ticketNotificationService)
		}
		// 同上，事件 ServiceTask 的 create/assign/status 写入也从裸 Ent 操作收回到
		// IncidentService，不再绕过领域校验（如报告人/处理人必须是租户内的活跃用户）。
		if h, ok := cpe.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler); ok {
			h.SetIncidentService(incidentService)
		}
		if h, ok := cpe.CallbackRegistry().GetHandler("release_service_handler").(*bpmn.ReleaseServiceTaskHandler); ok {
			h.SetReleaseService(releaseService)
		}
	}

	rootCauseAnalysisService := service.NewRootCauseAnalysisService(client)
	problemRepo := problem.NewEntRepository(client)
	problemServiceDomain := problem.NewService(problemRepo, sugar)
	problemHandler := problem.NewHandler(problemServiceDomain, client)
	incidentController := controller.NewIncidentController(incidentService, incidentService.RuleEngine(), incidentMonitoringService, incidentAlertingService, rootCauseAnalysisService, sugar)

	provisioningService := service.NewProvisioningService(client, sugar)
	provisioningController := controller.NewProvisioningController(provisioningService)

	// ProblemController and ChangeController removed - using Handlers instead
	// CMDB Controller
	cmdbController := controller.NewCMDBController(sugar, ciTypeService, ciAttributeDefinitionService, configurationItemService, ciRelationshipService, ciHistoryService, ciTagService, importExportService, savedViewService)

	// Release & Asset Management Controllers
	releaseController := controller.NewReleaseController(sugar, releaseService)
	assetController := controller.NewAssetController(sugar, assetService)
	assetLicenseController := controller.NewAssetLicenseController(sugar, assetLicenseService)

	projectController := controller.NewProjectController(client)
	applicationController := controller.NewApplicationController(client)

	ticketCategoryService := service.NewTicketCategoryService(client)
	ticketCategoryController := controller.NewTicketCategoryController(ticketCategoryService, sugar)
	ticketTagService := service.NewTicketTagService(client)
	ticketTagController := controller.NewTicketTagController(ticketTagService, sugar.Desugar())

	bpmnWorkflowController := controller.NewBPMNWorkflowController(processEngine, bpmnVersionService, client)
	bpmnTemplateService := service.NewBPMNTemplateService(client)

	// BPMN Process Trigger Controller (processBindingService/processTriggerService 已于 119-122 行预创建并注入 V2)
	configInheritanceService := service.NewConfigInheritanceService(client, sugar)
	bpmnProcessTriggerController := controller.NewBPMNProcessTriggerController(processTriggerService, processBindingService, configInheritanceService)

	// BPMN Dashboard Controller (监控仪表盘)
	bpmnMetricsService := service.NewBPMNMetricsService(client, sugar)
	bpmnAuditService := service.NewBPMNAuditService(client, sugar)
	bpmnTenantService := service.NewBPMNTenantService(client, sugar)
	bpmnSlaService := service.NewBPMNSLAService(client, sugar)
	bpmnDashboardController := controller.NewBPMNDashboardController(bpmnMetricsService, bpmnAuditService, bpmnTenantService, bpmnSlaService)

	// BPMN Monitoring Service & Controller（监控 + 完整执行轨迹时间线）
	bpmnMonitoringService := service.NewBPMNMonitoringService(client, bpmnAuditService, sugar)
	bpmnMonitoringController := controller.NewBPMNMonitoringController(bpmnMonitoringService)
	// BPMN AI Generator Service & Controller (AI驱动的流程生成)
	bpmnDeploymentService := service.NewBPMNDeploymentService(client)
	bpmnAIGeneratorService := service.NewBPMNAIGeneratorService(llmGateway, bpmnDeploymentService)
	bpmnAIGeneratorController := controller.NewBPMNAIGeneratorController(bpmnAIGeneratorService)

	// A2UI Ticket Controller (AI-driven UI表单)
	a2uiTicketService := service.NewA2UITicketService(nil)
	a2uiTicketController := controller.NewA2UITicketController(a2uiTicketService)

	// Global Search Controller (全局搜索)
	globalSearchController := controller.NewGlobalSearchController(client)

	// Known Error Handler (KEDB)
	knownErrorHandler := known_error.NewHandler(client, sugar)

	// Connector Manager / Registry / Market —— 连接器/插件/技能市场基础设施
	// Feishu 连接器控制器

	// Set process trigger service for workflow integration (after processTriggerService is declared)

	// 初始化模板并部署默认流程
	go func() {
		const defaultTenantID = 1
		ctx := tenantctx.WithTenantID(context.Background(), defaultTenantID)
		if _, err := bpmnTemplateService.LoadAndDeployTemplates(ctx, defaultTenantID); err != nil {
			sugar.Warnw("Failed to deploy BPMN templates", "error", err)
		}
		if err := processBindingService.InitDefaultBindings(ctx, defaultTenantID); err != nil {
			sugar.Warnw("Failed to init default process bindings", "error", err)
		}
	}()

	dashboardService := service.NewDashboardService(client, sugar)
	dashboardHandler := handlers.NewDashboardHandler(dashboardService, ticketService, incidentService, sugar)

	// Domain: Service Catalog (DDD)
	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, sugar, clients.IntakeDirectorySnapshot())
	scHandler := service_catalog.NewHandler(scService)

	// Domain: CMDB (DDD)
	cmdbRepo := cmdb.NewEntRepository(client)
	cmdbServiceDomain := cmdb.NewService(cmdbRepo, sugar)
	cmdbHandler := cmdb.NewHandler(cmdbServiceDomain)
	delegatedExecutionHandler := delegated_execution.NewHandler(delegated_execution.NewService(client))

	// Domain: Service Request (DDD)
	srRepo := service_request.NewEntRepository(client)
	chainResolver := service.NewApprovalChainResolver(client, sugar)
	srService := service_request.NewService(srRepo, client, sugar, chainResolver)
	srHandler := service_request.NewHandler(srService)

	// Domain: Change (DDD)
	changeRepo := change.NewEntRepository(client, database.GetRawDB())
	changeServiceDomain := change.NewService(changeRepo, client, sugar)
	// 提交变更审批后自动启动 change_normal_flow，见 change.Service.SetProcessTriggerService 注释；
	// CAB 审批决定/阶段流转完成 BPMN 任务需要 processEngine，见 SetProcessEngine 注释。
	changeServiceDomain.SetProcessTriggerService(processTriggerService)
	changeServiceDomain.SetProcessEngine(processEngine)
	changeHandler := change.NewHandler(changeServiceDomain)
	// Standard Change Handler reuses the authoritative Change creation service so
	// template instantiation creates WorkItem and extension atomically.
	standardChangeHandler := standard_change.NewHandler(client, sugar)
	// 注入 changeServiceDomain 到 BPMN change_service_handler，让 BPMN 自动创建的 Change
	// 走事务化建表逻辑（同步建好 WorkItem），不再绕过——同上面 incident_service_handler
	// 的注入方式（processEngine 的静态类型是 service.ProcessEngine 接口，未声明
	// CallbackRegistry()，需要先做一次类型断言）。
	if cpe, ok := processEngine.(*service.CustomProcessEngine); ok {
		if h, ok := cpe.CallbackRegistry().GetHandler("service_request_handler").(*bpmn.ServiceRequestServiceTaskHandler); ok {
			h.SetServiceRequestService(srService)
		}
		if h, ok := cpe.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler); ok {
			h.SetChangeService(changeServiceDomain)
		}
	}

	creationRegistry := intake.NewCreatorRegistry()
	for _, owner := range []intakecreation.ProfessionalCreator{ticketService, incidentService, problemServiceDomain, changeServiceDomain, srService} {
		if err := creationRegistry.Register(owner); err != nil {
			log.Fatalf("Invalid Intake creator registry: %v", err)
		}
	}
	intakeApplication := intake.NewService(client, intake.NewResolver(scService, processBindingService, configurationItemService, ticketCategoryService), creationRegistry, intake.NewWorkItemCreator(numberAllocator), clients.IntakeDirectorySnapshot())
	ticketController.SetCreationApplication(intakeApplication)
	incidentController.SetCreationApplication(intakeApplication)
	problemHandler.SetCreationApplication(intakeApplication)
	changeHandler.SetCreationApplication(intakeApplication)
	standardChangeHandler.SetCreationApplication(intakeApplication)
	srHandler.SetCreationApplication(intakeApplication)

	if cpe, ok := processEngine.(*service.CustomProcessEngine); ok {
		if h, ok := cpe.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler); ok {
			h.SetCreationApplication(intakeApplication, clients.System)
		}
		if h, ok := cpe.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler); ok {
			h.SetCreationApplication(intakeApplication, clients.System)
		}
	}
	toolQueue := service.NewToolQueue(client, toolRegistry, intakeApplication, ticketService, 100, sugar)
	feishuSyncService := service.NewFeishuSyncService(client, sugar, intakeApplication)
	outboxRegistry, err := service.NewOutboxEventTypeRegistry(
		[]service.OutboxDeliveryHandler{service.NewWorkflowStartOutboxHandler(client, concreteProcessEngine, systemClient), incidentService.RuleEngine(), service.NewFeishuCreationDeliveryHandler(feishuSyncService, func(tenantID int) (service.FeishuTaskCreator, bool) {
			conn, ok := connectorManager.Get(tenantID, "feishu")
			if !ok {
				return nil, false
			}
			tasks, ok := conn.(service.FeishuTaskCreator)
			return tasks, ok
		}), service.NewIncidentAlertDeliveryHandler(emailService), service.NewEmailAttachmentsDeliveryHandler(client, ticketAttachmentService, newTenantGraphInboundProvider(connectorManager)), service.NewEmailConfirmationDeliveryHandler(client, newTenantGraphInboundProvider(connectorManager))},
		service.KafDelegateRequestedEventType,
	)
	if err != nil {
		log.Fatalf("Invalid outbox event type registry: %v", err)
	}
	outboxDeliveryWorker, err := service.NewOutboxDeliveryWorker(
		service.NewOutboxEventRepository(systemClient),
		service.OutboxDeliveryWorkerConfig{
			BatchSize:      cfg.OutboxDelivery.BatchSize,
			PollInterval:   cfg.OutboxDelivery.PollInterval,
			HandlerTimeout: cfg.OutboxDelivery.HandlerTimeout,
			MaxAttempts:    cfg.OutboxDelivery.MaxAttempts,
		},
		sugar,
		outboxRegistry,
	)
	if err != nil {
		log.Fatalf("Invalid outbox delivery worker configuration: %v", err)
	}
	wireEmailMsgraphConnector(client, intakeApplication, triageService, connectorController, sugar)
	feishuController := controller.NewFeishuController(connectorManager, feishuSyncService, marketplaceSvc, sugar)

	// Analytics & Prediction Controllers
	analyticsController := controller.NewAnalyticsController(analyticsService)
	predictionController := controller.NewPredictionController(predictionService)

	// Domain: Knowledge (DDD)
	knowledgeRepo := knowledge.NewEntRepository(client)
	knowledgeServiceDomain := knowledge.NewService(knowledgeRepo, sugar)
	knowledgeHandler := knowledge.NewHandler(knowledgeServiceDomain)

	// Domain: SLA (DDD)
	slaRepo := sla.NewEntRepository(client)
	slaServiceDomain := sla.NewService(slaRepo, sugar)
	slaHandler := sla.NewHandler(slaServiceDomain)

	// SLA 模板服务（开箱即用）
	slaTemplateService := service.NewSLATemplateService(client, sugar)
	slaTemplateController := controller.NewSLATemplateController(slaTemplateService)

	// AI Domain
	aiRepo := ai.NewEntRepository(client)
	aiServiceDomain := ai.NewService(aiRepo, sugar, ragService, toolRegistry, toolQueue, analyticsService, predictionService, slaForecastSkill, triageService, rootCauseService, aiTelemetryService)
	aiServiceDomain.SetLLMGateway(llmGateway)
	// P2-6: 注入 ent client 供 AI 工具 RBAC 校验复用 hasResourcePermission
	aiServiceDomain.SetEntClient(client)
	aiHandler := ai.NewHandler(aiServiceDomain)

	// Common Domain
	commonRepo := domainCommon.NewEntRepository(client)
	var refreshTokenStore authentication.RefreshTokenStore
	// Refresh rotation depends on Redis for cross-instance, atomic one-time use.
	// Without a healthy store, the authentication consumer rejects refreshes.
	if cfg.Redis.Host != "" {
		commonRedis := redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := commonRedis.Ping(pingCtx).Err(); err != nil {
			sugar.Warnw("authentication redis ping failed; token refresh unavailable", "error", err)
			_ = commonRedis.Close()
		} else {
			refreshTokenStore = authentication.NewRedisRefreshTokenStore(commonRedis)
			authentication.ConfigureAccessTokenRevocationRedis(commonRedis)
			sugar.Info("authoritative refresh token consumption enabled via redis")
		}
		pingCancel()
	} else {
		sugar.Warn("REDIS_HOST is not configured; token refresh unavailable")
	}
	refreshTokenConsumer := authentication.NewRefreshTokenConsumer(cfg.JWT.Secret, refreshTokenStore)
	commonServiceDomain := domainCommon.NewService(commonRepo, cfg.JWT.Secret, sugar, systemClient, refreshTokenConsumer)
	commonHandler := domainCommon.NewHandler(commonServiceDomain)

	// Auth Controller（装配缺失的 register / forgot-password / reset-password / validate-reset-token / switch-tenant 路由）
	authService := service.NewAuthService(client, systemClient, cfg.JWT.Secret, sugar)
	authService.SetEmailService(emailService)
	if cfg.Server.FrontendURL != "" {
		authService.SetBaseURL(cfg.Server.FrontendURL)
	}
	authController := controller.NewAuthController(authService)

	// Role Handler (in-memory for now)
	roleHandler := common.NewRoleHandler(client, sugar)

	// User Controller
	userService := service.NewUserService(client, sugar)
	userController := controller.NewUserController(userService, sugar)

	// Group Controller
	groupService := service.NewGroupService(client)
	groupController := controller.NewGroupController(groupService, sugar)

	// Role & Permission Controllers (database-backed with tenant isolation)
	roleService := service.NewRoleService(client, sugar)
	roleController := controller.NewRoleController(roleService, sugar)
	permissionService := service.NewPermissionService(client, sugar)
	permissionController := controller.NewPermissionController(permissionService, sugar)

	// Menu Controller (database-backed with tenant isolation)
	menuService := service.NewMenuService(client, sugar)
	menuController := controller.NewMenuController(menuService)

	// Audit Log Controller (支持过滤/分页的审计日志查询)
	auditLogService := service.NewAuditLogService(client, sugar)
	auditLogController := controller.NewAuditLogController(auditLogService, sugar)

	// Tenant Controller（注入种子器：新租户上架时自动初始化默认配置）
	tenantService := service.NewTenantService(client, sugar)
	tenantService.SetSeeder(seeder.NewSeeder(client, sugar, cfg))
	tenantController := controller.NewTenantController(tenantService, sugar)

	// System Config Controller
	systemConfigService := service.NewSystemConfigService(client, sugar)
	systemConfigController := controller.NewSystemConfigController(systemConfigService, sugar)

	// Vendor Controller
	vendorService := service.NewVendorService(client, sugar)
	vendorController := controller.NewVendorController(vendorService)

	// Approval Chain Controller
	approvalChainService := service.NewApprovalChainService(client, sugar)
	approvalChainController := controller.NewApprovalChainController(approvalChainService, sugar)

	// SLA Monitor & Alert Services (legacy, for background tasks)
	slaMonitorService := service.NewSLAMonitorService(client, sugar)
	slaAlertService := service.NewSLAAlertService(client, sugar)
	escalationService := service.NewEscalationService(client, sugar)
	escalationMatrixService := service.NewEscalationMatrixService(sugar)
	escalationMatrixController := controller.NewEscalationMatrixController(sugar, escalationMatrixService)

	// Wire up notification service
	slaMonitorService.SetNotificationService(ticketNotificationService)
	slaAlertService.SetNotificationService(ticketNotificationService)
	escalationService.SetNotificationService(ticketNotificationService)

	// Survey Service & Controller
	surveyService := service.NewSurveyService(client, sugar)
	surveyController := controller.NewSurveyController(surveyService)

	// Cloud Service & Controller
	cloudService := service.NewCloudService(client, sugar)
	cloudController := controller.NewCloudController(cloudService, sugar)

	// WebSocket Service
	wsService := service.NewWebSocketService(sugar)
	ticketNotificationService.SetWebSocketService(wsService)

	// 7. 设置路由
	// 根据配置设置 Gin 运行模式
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else if cfg.Server.Mode == "test" {
		gin.SetMode(gin.TestMode)
	}
	r := gin.Default()
	if err := r.SetTrustedProxies([]string{"127.0.0.1"}); err != nil {
		sugar.Warnw("failed to set trusted proxies, falling back to default", "error", err)
	}

	// 初始化 Redis 限流器（分布式环境使用）
	var redisRateLimiter router.RateLimiterInterface
	if cfg.Redis.Host != "" {
		redisClient := redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		// 测试 Redis 连接
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			sugar.Warnw("Redis connection failed, rate limiter will use in-memory fallback", "error", err)
			redisRateLimiter = nil
		} else {
			sugar.Info("Redis connection established, using distributed rate limiter")
			// 默认每分钟 500 次请求
			redisRateLimiter = middleware.NewRedisRateLimiter(redisClient, 500, time.Minute)
		}
	} else {
		sugar.Warn("Redis not configured, rate limiter will use in-memory fallback (not suitable for distributed deployment)")
	}

	routerConfig := &router.RouterConfig{
		JWTSecret:                       cfg.JWT.Secret,
		Logger:                          sugar,
		Client:                          client,
		TenantDirectoryClient:           systemClient,
		RawDB:                           database.GetRawDB(),
		CSRFEnabled:                     cfg.Security.CSRFEnabled,
		RedisRateLimiter:                redisRateLimiter,
		TicketController:                ticketController,
		TicketDependencyController:      ticketDependencyController,
		TicketCommentController:         ticketCommentController,
		TicketAttachmentController:      ticketAttachmentController,
		TicketNotificationController:    ticketNotificationController,
		NotificationController:          notificationController,
		TicketRatingController:          ticketRatingController,
		TicketAssignmentSmartController: ticketAssignmentSmartController,
		TicketViewController:            ticketViewController,
		TicketWorkflowController:        ticketWorkflowController,
		TicketAutomationRuleController:  ticketAutomationRuleController,
		IncidentController:              incidentController,
		BPMNWorkflowController:          bpmnWorkflowController,
		BPMNProcessTriggerController:    bpmnProcessTriggerController,
		BPMNDashboardController:         bpmnDashboardController,
		BPMNMonitoringController:        bpmnMonitoringController,
		BPMNAIGeneratorController:       bpmnAIGeneratorController,
		A2UITicketController:            a2uiTicketController,
		CMDBController:                  cmdbController,

		DashboardHandler:          dashboardHandler,
		CMDBHandler:               cmdbHandler,
		DelegatedExecutionHandler: delegatedExecutionHandler,
		ProjectController:         projectController,
		ApplicationController:     applicationController,
		TicketCategoryController:  ticketCategoryController,
		TicketTagController:       ticketTagController,
		UserController:            userController,
		GroupController:           groupController,

		// Role & Permission Controllers
		RoleController:             roleController,
		PermissionController:       permissionController,
		MenuController:             menuController,
		TenantController:           tenantController,
		EscalationMatrixController: escalationMatrixController,
		AuditLogController:         auditLogController,

		MSPController:           mspController,
		SystemConfigController:  systemConfigController,
		ApprovalChainController: approvalChainController,

		// Notification Preference Controller
		NotificationPreferenceController: notificationPreferenceController,

		// Vendor Controller
		VendorController: vendorController,

		// Additional controllers
		ProvisioningController: provisioningController,
		AnalyticsController:    analyticsController,
		PredictionController:   predictionController,
		ReleaseController:      releaseController,
		AssetController:        assetController,
		AssetLicenseController: assetLicenseController,
		SurveyController:       surveyController,
		CloudController:        cloudController,

		// Domain Handlers
		ServiceCatalogHandler: scHandler,
		ServiceRequestHandler: srHandler,
		ProblemHandler:        problemHandler,
		ChangeHandler:         changeHandler,
		KnowledgeHandler:      knowledgeHandler,
		SLAHandler:            slaHandler,
		SLATemplateController: slaTemplateController,
		AIHandler:             aiHandler, // Added AI domain handler
		CommonHandler:         commonHandler,
		AuthController:        authController,
		RoleHandler:           roleHandler,

		// Global Search
		GlobalSearchController: globalSearchController,

		// Standard Change Handler
		StandardChangeHandler: standardChangeHandler,

		// Known Error Handler (KEDB)
		KnownErrorHandler: knownErrorHandler,

		// Connector Controller
		ConnectorController: connectorController,
		FeishuController:    feishuController,

		MarketplaceController: marketplaceCtrl,

		// WebSocket Service
		WebSocketService: wsService,
	}
	router.SetupRoutes(r, routerConfig)

	// Swagger - configure and register swagger docs
	docs.SwaggerInfo.Version = "1.0"
	docs.SwaggerInfo.Host = ""
	docs.SwaggerInfo.BasePath = "/"
	docs.SwaggerInfo.Schemes = []string{"http", "https"}
	docs.SwaggerInfo.Title = "ITSM API"
	docs.SwaggerInfo.Description = "IT Service Management System API Documentation"
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return &Application{
		Cfg:                  cfg,
		Logger:               sugar,
		DBClient:             client,
		systemClient:         systemClient,
		Router:               r,
		Embedder:             embedder,
		VectorStore:          vectorStore,
		callbackWorker:       concreteProcessEngine,
		notificationWorker:   ticketNotificationService,
		outboxDeliveryWorker: outboxDeliveryWorker,
		toolQueue:            toolQueue,
	}
}

func configurePermissionMode(environment string) {
	// 统一 DBOnly：数据库（seeder 初始化）为唯一运行时权限权威，开发/生产行为一致。
	// 硬编码 RolePermissions 仅保留 super_admin 代码级放行与 end_user 防御性兜底（DBOnly 下不生效）。
	_ = environment
	authorization.PermissionConfig.Mode = authorization.PermissionConfigModeDBOnly
}

// ValidateWebStartupConfig prevents schema or seed mutations from running in
// the long-lived HTTP process. Deployments must execute them through the
// explicit ITSM_BOOTSTRAP_ONLY job before starting application instances.
func ValidateWebStartupConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("configuration is required")
	}
	if cfg.Deployment.AutoMigrate || cfg.Deployment.AutoSeed {
		return fmt.Errorf(
			"ITSM_AUTO_MIGRATE and ITSM_AUTO_SEED are bootstrap-job options; run with ITSM_BOOTSTRAP_ONLY=true",
		)
	}
	return nil
}

func InitializeStorage(cfg *config.Config, client *ent.Client, sugar *zap.SugaredLogger) error {
	// RLS：schema 创建 / seed / DDL 属于跨租户操作，必须显式声明 system bypass
	ctx := tenantctx.SystemContext(context.Background(), "bootstrap:initialize_storage",
		"schema migration and default seed at process boot")

	if cfg.Deployment.AutoMigrate {
		migrator := migration.NewMigrator(database.GetRawDB(), sugar)
		bootstrap := migration.CanonicalBootstrap{
			Prepare: func(ctx context.Context) error {
				if err := database.PrepareBootstrapInfrastructure(ctx, database.GetRawDB()); err != nil {
					return fmt.Errorf("prepare canonical infrastructure: %w", err)
				}
				if err := prepareTicketCCIndexMigration(ctx, database.GetRawDB(), sugar); err != nil {
					return fmt.Errorf("prepare TicketCC index migration: %w", err)
				}
				if err := prepareTicketNotificationMigration(ctx, database.GetRawDB(), sugar); err != nil {
					return fmt.Errorf("prepare ticket notification migration: %w", err)
				}
				if err := prepareRolePermissionTenantMigration(ctx, database.GetRawDB(), sugar); err != nil {
					return fmt.Errorf("prepare role permission tenant migration: %w", err)
				}
				if err := prepareCMDBModelMigration(ctx, database.GetRawDB(), sugar); err != nil {
					return fmt.Errorf("prepare CMDB model migration: %w", err)
				}
				if err := prepareIncidentProblemRelationMigration(ctx, database.GetRawDB(), sugar); err != nil {
					return fmt.Errorf("prepare incident/problem relation migration: %w", err)
				}
				if err := prepareServiceRequestTicketMigration(ctx, database.GetRawDB(), sugar); err != nil {
					return fmt.Errorf("prepare service_request ticket migration: %w", err)
				}
				if err := migration.PrepareServiceRequestWorkItemAuthority(ctx, database.GetRawDB()); err != nil {
					return fmt.Errorf("ServiceRequest WorkItem authority preflight: %w", err)
				}
				return migration.PrepareIntakeActorProvenance(ctx, database.GetRawDB())
			},
			CreateSchema: func(ctx context.Context) error { return client.Schema.Create(ctx) },
			Migrator:     migrator,
		}
		if cfg.Deployment.AutoSeed {
			bootstrap.Seed = func(ctx context.Context) error {
				return runBootstrapSeed(ctx, cfg, client, sugar)
			}
		}
		if err := migration.RunCanonicalBootstrap(ctx, bootstrap); err != nil {
			return fmt.Errorf("run canonical schema bootstrap: %w", err)
		}
		sugar.Infow("database schema ensured", "deployment_mode", cfg.Deployment.Mode)
	}

	if cfg.Deployment.AutoSeed && !cfg.Deployment.AutoMigrate {
		if err := runBootstrapSeed(ctx, cfg, client, sugar); err != nil {
			return err
		}
	}

	return nil
}

func runBootstrapSeed(ctx context.Context, cfg *config.Config, client *ent.Client, sugar *zap.SugaredLogger) error {
	needsAdmin, err := needsBootstrapAdmin(ctx, client)
	if err != nil {
		return fmt.Errorf("check bootstrap administrator: %w", err)
	}
	if needsAdmin {
		for _, risk := range GuardBootstrapAdminCredentials(
			cfg.Deployment.Mode,
			os.Getenv("ADMIN_PASSWORD"),
		) {
			if risk.Severity == "fatal" {
				return fmt.Errorf("bootstrap credential rejected [%s]: %s", risk.Code, risk.Message)
			}
			sugar.Warnw("bootstrap credential risk detected", "code", risk.Code, "message", risk.Message)
		}
	}
	s := seeder.NewSeeder(client, sugar, cfg)
	components, err := seeder.ProductionInitializers(s)
	if err != nil {
		return fmt.Errorf("create production initializers: %w", err)
	}
	store, err := initialization.NewSQLStore(database.GetRawDB())
	if err != nil {
		return fmt.Errorf("create initialization store: %w", err)
	}
	engine, err := initialization.NewEngine(
		store,
		components,
		30*time.Second,
	)
	if err != nil {
		return fmt.Errorf("create initialization engine: %w", err)
	}
	executorID, err := os.Hostname()
	if err != nil {
		executorID = "bootstrap-job"
	}
	executorID, err = initialization.NewExecutorID(executorID)
	if err != nil {
		return fmt.Errorf("create initialization executor id: %w", err)
	}
	releaseVersion := strings.TrimSpace(os.Getenv("ITSM_RELEASE_VERSION"))
	if releaseVersion == "" {
		releaseVersion = "unversioned"
	}
	runID, err := engine.Apply(ctx, initialization.Request{
		Scope:          initialization.Scope{Type: "platform", ID: 0},
		TargetVersion:  seeder.CurrentTenantTemplateVersion,
		ReleaseVersion: releaseVersion,
		RequestedBy:    "bootstrap-job",
		ExecutorID:     executorID,
	})
	if err != nil {
		return fmt.Errorf("initialize production defaults (run %d): %w", runID, err)
	}
	sugar.Infow("seed completed", "deployment_mode", cfg.Deployment.Mode, "initialization_run_id", runID)
	return nil
}

type postSchemaMigrator interface {
	EnsureMigrationsTable(context.Context) error
	RunMigrations(context.Context, []migration.Migration) (int, error)
}

func runPostSchemaMigrations(ctx context.Context, migrator postSchemaMigrator) error {
	return migration.RunPostSchemaMigrations(ctx, migrator)
}

func RunInitialization() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger := initLogger(&cfg.Log)
	defer func() {
		_ = logger.Sync()
	}()

	sugar := logger.Sugar()
	LogDefaultCredentialRisks(
		GuardRuntimeCredentials(cfg.Deployment.Mode, cfg.JWT.Secret, cfg.Database.Password),
		sugar,
	)
	// This dedicated initialization job uses the deployment's migration credentials.
	// Atlas opens its inspector with a background context, so schema work must use
	// the migration client, never a tenant-scoped runtime decorator.
	client, err := database.InitDatabase(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer client.Close()

	if err := InitializeStorage(cfg, client, sugar); err != nil {
		log.Fatalf("Initialization failed: %v", err)
	}
}

func needsBootstrapAdmin(ctx context.Context, client *ent.Client) (bool, error) {
	rootTenant, err := client.Tenant.Query().Where(tenant.CodeEQ("default")).Only(ctx)
	if ent.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	exists, err := client.User.Query().
		Where(user.UsernameEQ("admin"), user.TenantIDEQ(rootTenant.ID)).
		Exist(ctx)
	if err != nil {
		return false, err
	}
	return !exists, nil
}

func (app *Application) Run() {
	defer app.Logger.Sync()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopAPIRuntime := app.startAPIRuntime(ctx)
	defer stopAPIRuntimeBeforeDependencies(stopAPIRuntime, func() {
		if app.systemClient != nil {
			app.systemClient.Close()
		}
		app.DBClient.Close()
	})

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", app.Cfg.Server.Port))
	if err != nil {
		log.Fatalf("Failed to listen on server port: %v", err)
	}
	server := &http.Server{Handler: app.Router}
	app.Logger.Infof("Server starting on port %d", app.Cfg.Server.Port)
	if err := serveUntilContextCancelled(ctx, server, listener); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func stopAPIRuntimeBeforeDependencies(stopRuntime func(), closeDependencies func()) {
	stopRuntime()
	closeDependencies()
}

// startAPIRuntime starts the background responsibilities that remain owned by
// the API process in this release. KAF delegation delivery is deliberately
// excluded: it has a single owner in the dedicated Worker process.
func (app *Application) startAPIRuntime(ctx context.Context) func() {
	waitForOutboxDelivery := app.startOutboxDeliveryWorker(ctx)
	backgroundCtx, cancelBackground := context.WithCancel(ctx)
	app.startBackground(backgroundCtx)
	return func() {
		cancelBackground()
		waitForOutboxDelivery()
		if app.toolQueue != nil {
			app.toolQueue.Close()
		}
	}
}

func (app *Application) startBackground(ctx context.Context) {
	if app.startBackgroundTasksFunc != nil {
		app.startBackgroundTasksFunc(ctx)
		return
	}
	app.startBackgroundTasks(ctx)
}

func (app *Application) startOutboxDeliveryWorker(ctx context.Context) func() {
	if app.outboxDeliveryWorker == nil {
		return func() {}
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		app.outboxDeliveryWorker.Run(ctx)
	}()
	return waitGroup.Wait
}

func serveUntilContextCancelled(ctx context.Context, server *http.Server, listener net.Listener) error {
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()

	select {
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-serveError
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (app *Application) startBackgroundTasks(lifecycleCtx context.Context) {
	app.startCallbackOutboxWorker(lifecycleCtx)
	app.startNotificationDeliveryWorker(lifecycleCtx)

	go func() {
		pipeline := service.NewEmbeddingPipeline(app.DBClient, app.Embedder, app.Logger, app.VectorStore)
		ctx := context.Background()
		// initial full-ish pass per tenant
		tenants, err := app.DBClient.Tenant.Query().All(ctx)
		if err == nil {
			for _, t := range tenants {
				if err := pipeline.RunOnce(ctx, t.ID, 200); err != nil {
					app.Logger.Warnw("embedding pipeline failed", "error", err, "tenant_id", t.ID)
				}
			}
		} else {
			// fallback default tenant 1
			if err := pipeline.RunOnce(ctx, 1, 200); err != nil {
				app.Logger.Warnw("embedding pipeline failed", "error", err, "tenant_id", 1)
			}
		}
		// periodic incremental per tenant
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			tenants, err := app.DBClient.Tenant.Query().All(ctx)
			if err != nil {
				continue
			}
			for _, t := range tenants {
				if err := pipeline.RunOnce(ctx, t.ID, 50); err != nil {
					app.Logger.Warnw("embedding pipeline failed", "error", err, "tenant_id", t.ID)
				}
			}
		}
	}()

	// SLA Monitoring and Escalation background tasks
	go func() {
		slaMonitorService := service.NewSLAMonitorService(app.DBClient, app.Logger)
		escalationService := service.NewEscalationService(app.DBClient, app.Logger)

		ctx := context.Background()
		// Run SLA check every 5 minutes
		slaTicker := time.NewTicker(5 * time.Minute)
		defer slaTicker.Stop()

		// Run escalation check every 15 minutes
		escalationTicker := time.NewTicker(15 * time.Minute)
		defer escalationTicker.Stop()

		for {
			select {
			case <-slaTicker.C:
				tenants, err := app.DBClient.Tenant.Query().All(ctx)
				if err != nil {
					continue
				}
				for _, t := range tenants {
					if _, err := slaMonitorService.CheckSLAViolations(ctx, t.ID); err != nil {
						app.Logger.Warnw("SLA violation check failed", "error", err, "tenant_id", t.ID)
					}
				}
			case <-escalationTicker.C:
				tenants, err := app.DBClient.Tenant.Query().All(ctx)
				if err != nil {
					continue
				}
				for _, t := range tenants {
					if err := escalationService.ProcessEscalations(ctx, t.ID); err != nil {
						app.Logger.Warnw("escalation processing failed", "error", err, "tenant_id", t.ID)
					}
				}
			}
		}
	}()
}

func (app *Application) startCallbackOutboxWorker(ctx context.Context) {
	if app.callbackWorker == nil {
		return
	}
	workerID := "bpmn-callback-" + uuid.NewString()
	go app.callbackWorker.RunCallbackOutboxWorker(ctx, workerID, 2*time.Second)
}

func (app *Application) startNotificationDeliveryWorker(ctx context.Context) {
	if app.notificationWorker == nil {
		return
	}
	workerID := "ticket-notification-" + uuid.NewString()
	go app.notificationWorker.RunDeliveryWorker(ctx, workerID, 2*time.Second)
}
