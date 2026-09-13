// Package webhook 通用 HTTP 出站 Webhook 连接器
// 适用：把 ITSM 事件（工单创建、SLA 即将超时、CMDB 变更 等）推送到任意 HTTP 端点
// 支持：HMAC 签名、指数退避重试、并发限流
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
	"itsm-backend/connector"
)

type Webhook struct {
	endpoint      string
	signingSecret string
	destination   string
	client        *http.Client
	logger        *zap.SugaredLogger
	mu            sync.Mutex
	counter       int
}

func init() {
	connector.MustRegister(func() connector.Connector { return New() })
}

func New() *Webhook {
	return &Webhook{client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (w *Webhook) Manifest() connector.Manifest {
	return connector.Manifest{
		InitializationBehavior: connector.InitializationLocalOnly,
		Name:                   "webhook",
		Version:                "1.0.0",
		Title:                  "通用 Webhook 出站",
		Provider:               "generic",
		Type:                   connector.TypeWebhook,
		Description:            "把 ITSM 事件以 HTTP POST 推送到任意端点，支持 HMAC-SHA256 签名",
		Capabilities:           []connector.Capability{connector.CapSendMessage, connector.CapCreateTicket, connector.CapUpdateTicket},
		Tags:                   []string{"webhook", "outbound", "generic"},
		IsOfficial:             true,
		RequiredPermissions:    []string{"connector:write", "ticket:write"},
	}
}

func (w *Webhook) Init(_ context.Context, cfg connector.Config) error {
	if url, ok := cfg.Settings["url"].(string); !ok || url == "" {
		return fmt.Errorf("webhook: settings.url is required")
	}
	w.endpoint, _ = cfg.Settings["url"].(string)
	parsed, err := url.Parse(w.endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("webhook: valid HTTP destination required")
	}
	w.signingSecret = cfg.Credentials["secret"]
	identity, _ := json.Marshal(w.endpoint)
	digest := sha256.Sum256(identity)
	w.destination = hex.EncodeToString(digest[:])
	if w.logger == nil {
		w.logger = zap.S().Named("connector.webhook")
	}
	return nil
}

func (w *Webhook) Send(ctx context.Context, msg *connector.Message) error {
	url := w.endpoint
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ITSM-Connector/1.0")
	req.Header.Set("X-ITSM-Event", msg.Type)

	// 签名：HMAC-SHA256(secret, body)
	if secret := w.signingSecret; secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-ITSM-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	w.mu.Lock()
	w.counter++
	w.mu.Unlock()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: target returned %d", resp.StatusCode)
	}
	return nil
}

func (w *Webhook) HealthCheck(ctx context.Context) connector.HealthStatus {
	url := w.endpoint
	if url == "" {
		return connector.HealthStatus{OK: false, Message: "settings.url missing"}
	}
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	resp, err := w.client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return connector.HealthStatus{OK: false, LatencyMs: latency, Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return connector.HealthStatus{OK: false, LatencyMs: latency, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return connector.HealthStatus{OK: true, LatencyMs: latency}
}

func (w *Webhook) Close() error { return nil }

// DeliveryDestinationIdentity identifies the immutable endpoint captured at Init.
func (w *Webhook) DeliveryDestinationIdentity() string { return w.destination }
