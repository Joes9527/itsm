package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector"
	msgraphpkg "itsm-backend/connector/builtin/msgraph"
	"itsm-backend/connector/marketplace"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/connectorconfig"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// emailPollingCoordinator is the subset of *msgraph.EmailPollingCoordinator
// this controller depends on. Kept as a small interface (rather than the
// concrete type) purely so tests can substitute a fake without spinning up
// real polling goroutines.
type emailPollingCoordinator interface {
	Start(ctx context.Context, tenantID int, conn *msgraphpkg.GraphConnector)
	Stop(tenantID int)
}

// ConnectorController 连接器 HTTP API
// 路由：
//
//	GET    /api/v1/connectors              -> 列出已注册连接器（市场视图）
//	GET    /api/v1/connectors/configs      -> 列出当前租户已配置的连接器实例
//	POST   /api/v1/connectors/configs      -> 创建/更新一个连接器配置（provision）
//	DELETE /api/v1/connectors/configs/:name -> 停用并移除一个连接器实例
//	POST   /api/v1/connectors/:name/send   -> 通过指定连接器发消息
//	POST   /api/v1/connectors/:name/test   -> 发送一条测试消息
//	GET    /api/v1/connectors/health       -> 所有实例的健康检查
type ConnectorController struct {
	manager          *connector.Manager
	market           *marketplace.Market // optional
	registry         *connector.Registry
	logger           *zap.SugaredLogger
	restoreClient    *ent.Client             // Read-only cross-tenant startup capability.
	client           *ent.Client             // 持久化连接器配置（nil 时跳过，测试场景）
	emailCoordinator emailPollingCoordinator // optional; nil unless SetEmailCoordinator is called
}

func NewConnectorController(mgr *connector.Manager, reg *connector.Registry, mkt *marketplace.Market, logger *zap.SugaredLogger, client, restoreClient *ent.Client) *ConnectorController {
	return &ConnectorController{manager: mgr, market: mkt, registry: reg, logger: logger, client: client, restoreClient: restoreClient}
}

// SetEmailCoordinator wires in the MS Graph email polling coordinator.
// Optional — if never called, provisioning "msgraph-email" still succeeds
// (config is stored via Manager like any other connector) but no polling
// starts. Called once from bootstrap after the coordinator's dependencies
// (TicketService, TriageService) are constructed.
func (c *ConnectorController) SetEmailCoordinator(coord emailPollingCoordinator) {
	c.emailCoordinator = coord
}

// ListMarket 列出市场中所有可用连接器
func (c *ConnectorController) ListMarket(ctx *gin.Context) {
	reg := c.registry
	if reg == nil {
		reg = connector.Default()
	}
	mfs := reg.List()
	tenantID := ctx.GetInt("tenant_id")
	configs := c.manager.ListByTenant(tenantID)
	installed := make(map[string]bool, len(configs))
	enabled := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		installed[cfg.Name] = true
		enabled[cfg.Name] = cfg.Enabled
	}
	health := c.manager.HealthCheckAll(ctx.Request.Context())
	out := make([]dto.ConnectorManifestDTO, 0, len(mfs))
	for _, m := range mfs {
		healthy, checkedAt, lastErr := healthForManifest(health, tenantID, m.Name)
		out = append(out, dto.ConnectorManifestDTO{
			Name:                m.Name,
			Version:             m.Version,
			Title:               m.Title,
			Provider:            m.Provider,
			Type:                string(m.Type),
			Description:         m.Description,
			Author:              m.Author,
			Homepage:            m.Homepage,
			IconURL:             m.IconURL,
			Capabilities:        capToString(m.Capabilities),
			Tags:                m.Tags,
			MinITSMVer:          m.MinITSMVer,
			Local:               true,
			Installed:           installed[m.Name],
			Enabled:             enabled[m.Name],
			Healthy:             healthy,
			LastCheckedAt:       checkedAt,
			LastError:           lastErr,
			Lifecycle:           connectorLifecycle(installed[m.Name], enabled[m.Name], healthy, lastErr),
			Category:            string(m.Type),
			IsOfficial:          m.IsOfficial,
			RequiredPermissions: m.RequiredPermissions,
			Checksum:            m.Checksum,
		})
	}
	common.Success(ctx, gin.H{"items": out, "total": len(out)})
}

// ListConfigs 列出当前租户已配置的连接器实例（凭据脱敏）
func (c *ConnectorController) ListConfigs(ctx *gin.Context) {
	tenantID := ctx.GetInt("tenant_id")
	cfgs := c.manager.ListByTenant(tenantID)
	health := c.manager.HealthCheckAll(ctx.Request.Context())
	out := make([]dto.ConnectorConfigDTO, 0, len(cfgs))
	for _, cfg := range cfgs {
		out = append(out, maskConfig(cfg, health))
	}
	common.Success(ctx, gin.H{"items": out, "total": len(out)})
}

// Provision 创建/更新一个连接器实例
func (c *ConnectorController) Provision(ctx *gin.Context) {
	var req dto.ProvisionConnectorRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, err.Error())
		return
	}
	tenantID := ctx.GetInt("tenant_id")
	if req.Settings == nil {
		req.Settings = make(map[string]interface{})
	}
	// Never trust a client-provided callback identifier. Preserve the existing
	// server-generated value on updates, otherwise generate 192 bits of entropy.
	callbackInstanceID := ""
	for _, existing := range c.manager.ListByTenant(tenantID) {
		if existing.Name == req.Name {
			callbackInstanceID, _ = existing.Settings["callbackInstanceId"].(string)
			break
		}
	}
	if callbackInstanceID == "" {
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			common.Fail(ctx, common.InternalErrorCode, "无法生成回调实例标识")
			return
		}
		callbackInstanceID = hex.EncodeToString(buf)
	}
	req.Settings["callbackInstanceId"] = callbackInstanceID
	cfg := connector.Config{
		TenantID:    tenantID,
		Name:        req.Name,
		Provider:    req.Provider,
		Enabled:     req.Enabled,
		Credentials: req.Credentials,
		Settings:    req.Settings,
		Labels:      req.Labels,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := c.manager.Provision(ctx.Request.Context(), cfg); err != nil {
		common.Fail(ctx, common.InternalErrorCode, err.Error())
		return
	}
	// 持久化到数据库，供后端重启后自动恢复
	if err := c.persistConfig(ctx.Request.Context(), cfg); err != nil {
		c.logger.Warnw("Failed to persist connector config", "error", err, "tenant", tenantID, "name", cfg.Name)
	}
	if req.Name == "msgraph-email" && c.emailCoordinator != nil {
		if !cfg.Enabled {
			c.emailCoordinator.Stop(tenantID)
		} else if conn, ok := c.manager.Get(tenantID, "msgraph-email"); ok {
			if gc, ok := conn.(*msgraphpkg.GraphConnector); ok {
				// The HTTP request's context is cancelled the instant this
				// handler returns its response — but the polling goroutine
				// started by Start must keep running long after that.
				// context.WithoutCancel preserves any request-scoped values
				// while detaching from the request's cancellation signal, so
				// the poller isn't killed within microseconds of starting.
				c.emailCoordinator.Start(context.WithoutCancel(ctx.Request.Context()), tenantID, gc)
			}
		}
	}
	common.Success(ctx, maskConfig(cfg, c.manager.HealthCheckAll(ctx.Request.Context())))
}

// Revoke 停用并移除一个连接器实例
func (c *ConnectorController) Revoke(ctx *gin.Context) {
	name := ctx.Param("name")
	tenantID := ctx.GetInt("tenant_id")
	c.manager.Revoke(connector.Config{TenantID: tenantID, Name: name})
	// 从数据库删除配置
	if err := c.deleteConfig(ctx.Request.Context(), tenantID, name); err != nil {
		c.logger.Warnw("Failed to delete connector config", "error", err, "tenant", tenantID, "name", name)
	}
	if name == "msgraph-email" && c.emailCoordinator != nil {
		c.emailCoordinator.Stop(tenantID)
	}
	common.Success(ctx, gin.H{"name": name, "revoked": true})
}

// Send 通过指定连接器发消息
func (c *ConnectorController) Send(ctx *gin.Context) {
	name := ctx.Param("name")
	var req dto.SendConnectorMessageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		common.Fail(ctx, common.ParamErrorCode, err.Error())
		return
	}
	tenantID := ctx.GetInt("tenant_id")
	msg := &connector.Message{
		Channel:  req.Channel,
		Type:     req.Type,
		Title:    req.Title,
		Content:  req.Content,
		Mentions: convertMentions(req.Mentions),
		Actions:  convertActions(req.Actions),
		Metadata: req.Metadata,
	}
	if req.Card != nil {
		msg.Card = convertCard(req.Card)
	}
	if err := c.manager.Send(ctx.Request.Context(), tenantID, name, msg); err != nil {
		common.Fail(ctx, common.InternalErrorCode, err.Error())
		return
	}
	common.Success(ctx, gin.H{"name": name, "channel": req.Channel, "sent": true})
}

// Test 发送一条简单测试消息（向配置里 debug_channel 投递）
func (c *ConnectorController) Test(ctx *gin.Context) {
	name := ctx.Param("name")
	tenantID := ctx.GetInt("tenant_id")
	// 找该连接器的配置
	var channel string
	for _, cfg := range c.manager.ListByTenant(tenantID) {
		if cfg.Name == name {
			if ch, ok := cfg.Settings["debug_channel"].(string); ok {
				channel = ch
			}
		}
	}
	if channel == "" {
		common.Fail(ctx, common.ParamErrorCode, "settings.debug_channel not configured for "+name)
		return
	}
	if err := c.manager.Send(ctx.Request.Context(), tenantID, name, &connector.Message{
		Channel: channel,
		Type:    "text",
		Title:   "ITSM 连接器测试",
		Content: "这是一条来自 ITSM 的测试消息。\n时间: " + time.Now().Format(time.RFC3339),
	}); err != nil {
		common.Fail(ctx, common.InternalErrorCode, err.Error())
		return
	}
	common.Success(ctx, gin.H{"name": name, "channel": channel, "sent": true})
}

// Health 所有运行实例健康检查
func (c *ConnectorController) Health(ctx *gin.Context) {
	res := c.manager.HealthCheckAll(context.Background())
	out := make(map[string]dto.ConnectorHealthDTO, len(res))
	for k, v := range res {
		out[k] = dto.ConnectorHealthDTO{
			OK:        v.OK,
			LatencyMs: v.LatencyMs,
			Message:   v.Message,
			CheckedAt: v.CheckedAt,
			Extra:     v.Extra,
		}
	}
	common.Success(ctx, out)
}

// Lifecycle returns a tenant-scoped connector lifecycle view for GA readiness checks.
func (c *ConnectorController) Lifecycle(ctx *gin.Context) {
	reg := c.registry
	if reg == nil {
		reg = connector.Default()
	}
	tenantID := ctx.GetInt("tenant_id")
	configs := c.manager.ListByTenant(tenantID)
	configByName := make(map[string]connector.Config, len(configs))
	for _, cfg := range configs {
		configByName[cfg.Name] = cfg
	}
	health := c.manager.HealthCheckAll(ctx.Request.Context())
	manifests := reg.List()
	out := make([]dto.ConnectorLifecycleDTO, 0, len(manifests))
	for _, m := range manifests {
		cfg, installed := configByName[m.Name]
		enabled := installed && cfg.Enabled
		healthy, checkedAt, lastErr := healthForManifest(health, tenantID, m.Name)
		out = append(out, dto.ConnectorLifecycleDTO{
			Name:          m.Name,
			Provider:      m.Provider,
			Type:          string(m.Type),
			Installed:     installed,
			Enabled:       enabled,
			Healthy:       healthy,
			Lifecycle:     connectorLifecycle(installed, enabled, healthy, lastErr),
			LastCheckedAt: checkedAt,
			LastError:     lastErr,
			Capabilities:  capToString(m.Capabilities),
		})
	}
	common.Success(ctx, gin.H{"items": out, "total": len(out)})
}

// helpers

func maskConfig(cfg connector.Config, health map[string]connector.HealthStatus) dto.ConnectorConfigDTO {
	masked := make(map[string]string, len(cfg.Credentials))
	for k := range cfg.Credentials {
		masked[k] = "******"
	}
	healthy, checkedAt, lastErr := healthForConfig(health, cfg)
	return dto.ConnectorConfigDTO{
		Name:          cfg.Name,
		Provider:      cfg.Provider,
		Type:          string(cfg.Type),
		Enabled:       cfg.Enabled,
		Healthy:       healthy,
		Lifecycle:     connectorLifecycle(true, cfg.Enabled, healthy, lastErr),
		LastCheckedAt: checkedAt,
		LastError:     lastErr,
		CreatedAt:     cfg.CreatedAt,
		UpdatedAt:     cfg.UpdatedAt,
		Credentials:   masked,
		Settings:      cfg.Settings,
		Labels:        cfg.Labels,
	}
}

func healthForConfig(health map[string]connector.HealthStatus, cfg connector.Config) (bool, *time.Time, string) {
	key := fmt.Sprintf("%d/%s/%s", cfg.TenantID, cfg.Name, cfg.Provider)
	if h, ok := health[key]; ok {
		checkedAt := h.CheckedAt
		if h.OK {
			return true, &checkedAt, ""
		}
		return false, &checkedAt, h.Message
	}
	return false, nil, ""
}

func healthForManifest(health map[string]connector.HealthStatus, tenantID int, name string) (bool, *time.Time, string) {
	for key, h := range health {
		prefix := fmt.Sprintf("%d/%s/", tenantID, name)
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			checkedAt := h.CheckedAt
			if h.OK {
				return true, &checkedAt, ""
			}
			return false, &checkedAt, h.Message
		}
	}
	return false, nil, ""
}

func connectorLifecycle(installed, enabled, healthy bool, lastErr string) string {
	switch {
	case healthy:
		return "healthy"
	case enabled && lastErr != "":
		return "unhealthy"
	case enabled:
		return "enabled"
	case installed:
		return "installed"
	default:
		return "available"
	}
}

func capToString(caps []connector.Capability) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, string(c))
	}
	return out
}

func convertMentions(in []dto.MentionDTO) []connector.Mention {
	out := make([]connector.Mention, 0, len(in))
	for _, m := range in {
		out = append(out, connector.Mention{Type: m.Type, ID: m.ID, Name: m.Name})
	}
	return out
}

func convertActions(in []dto.ActionDTO) []connector.Action {
	out := make([]connector.Action, 0, len(in))
	for _, a := range in {
		out = append(out, connector.Action{Type: a.Type, Text: a.Text, URL: a.URL, Value: a.Value})
	}
	return out
}

func convertCard(in *dto.CardPayloadDTO) *connector.Card {
	card := &connector.Card{Variables: in.Variables}
	if in.Header != nil {
		card.Header = &connector.CardHeader{Title: in.Header.Title, Subtitle: in.Header.Subtitle, Color: in.Header.Color}
	}
	for _, s := range in.Sections {
		sec := connector.CardSection{Title: s.Title}
		for _, e := range s.Content {
			sec.Content = append(sec.Content, convertElement(e))
		}
		card.Sections = append(card.Sections, sec)
	}
	for _, e := range in.Elements {
		card.Elements = append(card.Elements, convertElement(e))
	}
	return card
}

func convertElement(e dto.CardElementDTO) connector.CardElement {
	el := connector.CardElement{Type: e.Type, Text: e.Text, ImageURL: e.ImageURL, Extras: e.Extras}
	for _, kv := range e.Fields {
		el.Fields = append(el.Fields, connector.KV{Key: kv.Key, Value: kv.Value, Short: kv.Short})
	}
	if e.Action != nil {
		el.Action = &connector.Action{Type: e.Action.Type, Text: e.Action.Text, URL: e.Action.URL, Value: e.Action.Value}
	}
	return el
}

// persistConfig 持久化连接器配置到数据库，供后端重启后自动恢复。
func (c *ConnectorController) persistConfig(ctx context.Context, cfg connector.Config) error {
	if c.client == nil {
		return nil
	}
	credJSON, _ := json.Marshal(cfg.Credentials)
	settingsJSON, _ := json.Marshal(cfg.Settings)
	labelsJSON, _ := json.Marshal(cfg.Labels)

	existing, err := c.client.ConnectorConfig.Query().
		Where(connectorconfig.TenantIDEQ(cfg.TenantID), connectorconfig.NameEQ(cfg.Name)).
		Only(ctx)
	if ent.IsNotFound(err) {
		_, err = c.client.ConnectorConfig.Create().
			SetTenantID(cfg.TenantID).
			SetName(cfg.Name).
			SetProvider(cfg.Provider).
			SetEnabled(cfg.Enabled).
			SetCredentials(string(credJSON)).
			SetSettings(string(settingsJSON)).
			SetLabels(string(labelsJSON)).
			Save(ctx)
		return err
	}
	if err != nil {
		return err
	}
	_, err = existing.Update().
		SetProvider(cfg.Provider).
		SetEnabled(cfg.Enabled).
		SetCredentials(string(credJSON)).
		SetSettings(string(settingsJSON)).
		SetLabels(string(labelsJSON)).
		Save(ctx)
	return err
}

// deleteConfig 从数据库删除连接器配置。
func (c *ConnectorController) deleteConfig(ctx context.Context, tenantID int, name string) error {
	if c.client == nil {
		return nil
	}
	_, err := c.client.ConnectorConfig.Delete().
		Where(connectorconfig.TenantIDEQ(tenantID), connectorconfig.NameEQ(name)).
		Exec(ctx)
	return err
}

// LoadAll 从数据库加载所有已启用的连接器配置并自动 provision。
// 供 bootstrap 在启动时调用，恢复因进程重启而丢失的连接器实例。
func (c *ConnectorController) LoadAll(ctx context.Context) error {
	if c.restoreClient == nil {
		return fmt.Errorf("connector restore database capability is required")
	}
	configs, err := c.restoreClient.ConnectorConfig.Query().
		Where(connectorconfig.EnabledEQ(true)).
		All(ctx)
	if err != nil {
		return err
	}
	for _, cfg := range configs {
		tenantCtx := tenantctx.WithTenantID(ctx, cfg.TenantID)
		var credentials map[string]string
		var settings map[string]interface{}
		var labels map[string]string
		_ = json.Unmarshal([]byte(cfg.Credentials), &credentials)
		_ = json.Unmarshal([]byte(cfg.Settings), &settings)
		_ = json.Unmarshal([]byte(cfg.Labels), &labels)
		if settings == nil {
			settings = make(map[string]interface{})
		}
		if err := c.manager.Provision(tenantCtx, connector.Config{
			TenantID:    cfg.TenantID,
			Name:        cfg.Name,
			Provider:    cfg.Provider,
			Enabled:     cfg.Enabled,
			Credentials: credentials,
			Settings:    settings,
			Labels:      labels,
			CreatedAt:   cfg.CreatedAt,
			UpdatedAt:   cfg.UpdatedAt,
		}); err != nil {
			c.logger.Warnw("Failed to restore connector from DB", "error", err, "tenant", cfg.TenantID, "name", cfg.Name)
			continue
		}
		// 恢复 msgraph-email 的邮件轮询
		if cfg.Name == "msgraph-email" && cfg.Enabled && c.emailCoordinator != nil {
			if conn, ok := c.manager.Get(cfg.TenantID, "msgraph-email"); ok {
				if gc, ok := conn.(*msgraphpkg.GraphConnector); ok {
					c.emailCoordinator.Start(tenantCtx, cfg.TenantID, gc)
				}
			}
		}
	}
	return nil
}
