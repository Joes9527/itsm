package bpmn

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/connectorconfig"

	"go.uber.org/zap"
)

// WebhookHandler Webhook服务任务处理器
type WebhookHandler struct {
	HandlerBase
	client     *ent.Client
	logger     *zap.SugaredLogger
	httpClient *http.Client
}

// NewWebhookHandler 创建Webhook处理器
func NewWebhookHandler(client *ent.Client, logger *zap.SugaredLogger) *WebhookHandler {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid webhook address: %w", err)
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve webhook host: %w", err)
			}
			for _, ip := range ips {
				if isPrivateWebhookIP(ip) {
					return nil, fmt.Errorf("webhook target resolves to a private or reserved address")
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("webhook host has no address")
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	return &WebhookHandler{
		client: client,
		logger: logger,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many webhook redirects")
				}
				return validateWebhookURL(req.URL.String())
			},
		},
	}
}

func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid webhook URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must use http or https")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("webhook target is not allowed")
	}
	if ip, err := netip.ParseAddr(host); err == nil && isPrivateWebhookIP(ip) {
		return fmt.Errorf("webhook target is not allowed")
	}
	return nil
}

func isPrivateWebhookIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		!ip.IsGlobalUnicast()
}

// GetTaskType 返回任务类型
func (h *WebhookHandler) GetTaskType() string {
	return "webhook_task"
}

// GetHandlerID 返回处理器标识
func (h *WebhookHandler) GetHandlerID() string {
	return "webhook_handler"
}

// Execute 执行Webhook服务任务
func (h *WebhookHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "call_webhook", "send_notification":
		return h.callTrustedWebhook(ctx, variables)
	default:
		return nil, fmt.Errorf("不支持的 Webhook 回调动作")
	}
}

// Validate 验证配置
func (h *WebhookHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

type trustedWebhookSettings struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type trustedWebhookCredentials struct {
	Secret string `json:"secret"`
}

type bpmnWebhookEnvelope struct {
	EventType    string `json:"eventType,omitempty"`
	Title        string `json:"title,omitempty"`
	Content      string `json:"content,omitempty"`
	BusinessType string `json:"businessType"`
	BusinessID   int    `json:"businessId"`
}

func (h *WebhookHandler) callTrustedWebhook(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	if h.client == nil {
		return nil, fmt.Errorf("Webhook 配置存储不可用")
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	configRef := GetStringFromVars(variables, "callback_config_ref")
	if configRef == "" {
		return nil, fmt.Errorf("Webhook 回调缺少可信配置引用")
	}
	config, err := h.client.ConnectorConfig.Query().Where(
		connectorconfig.TenantID(tenantID),
		connectorconfig.Name(configRef),
		connectorconfig.Enabled(true),
	).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("Webhook 可信配置不可用")
	}
	var settings trustedWebhookSettings
	if err := json.Unmarshal([]byte(config.Settings), &settings); err != nil || settings.URL == "" {
		return nil, fmt.Errorf("Webhook 可信配置无效")
	}
	if err := validateWebhookURL(settings.URL); err != nil {
		return nil, fmt.Errorf("Webhook 可信端点不允许")
	}
	var credentials trustedWebhookCredentials
	if config.Credentials != "" {
		if err := json.Unmarshal([]byte(config.Credentials), &credentials); err != nil {
			return nil, fmt.Errorf("Webhook 凭据配置无效")
		}
	}

	envelope := bpmnWebhookEnvelope{
		EventType:    GetStringFromVars(variables, "event_type"),
		Title:        GetStringFromVars(variables, "title"),
		Content:      GetStringFromVars(variables, "content"),
		BusinessType: GetStringFromVars(variables, "business_type"),
		BusinessID:   GetIntFromVars(variables, "business_id"),
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("创建 Webhook 载荷失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建 Webhook 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ITSM-BPMN-Webhook/1.0")
	req.Header.Set("X-ITSM-Event", envelope.EventType)
	if executionKey, ok := BPMNCallbackExecutionKey(ctx); ok {
		req.Header.Set("Idempotency-Key", executionKey)
	} else {
		return nil, fmt.Errorf("Webhook 回调缺少幂等执行键")
	}
	if credentials.Secret != "" {
		mac := hmac.New(sha256.New, []byte(credentials.Secret))
		_, _ = mac.Write(body)
		req.Header.Set("X-ITSM-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	timeoutSeconds := settings.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	if timeoutSeconds > 120 {
		timeoutSeconds = 120
	}
	requestCtx, cancel := context.WithTimeout(req.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	req = req.WithContext(requestCtx)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Errorw("Webhook call failed", "error_class", "handler_error")
		return nil, fmt.Errorf("调用 Webhook 失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		h.logger.Warnw("Webhook target rejected callback", "status_code", resp.StatusCode)
		return nil, fmt.Errorf("Webhook 目标返回非成功状态")
	}
	h.logger.Infow("Webhook called successfully", "status_code", resp.StatusCode)
	return &dto.ServiceTaskResult{
		Success:    true,
		Message:    fmt.Sprintf("Webhook调用成功，状态码: %d", resp.StatusCode),
		OutputVars: map[string]interface{}{"status_code": resp.StatusCode},
	}, nil
}

// 确保 WebhookHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*WebhookHandler)(nil)
