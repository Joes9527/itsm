package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"io"
	"itsm-backend/common"
	"itsm-backend/connector"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/dto"
	"itsm-backend/service"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func webhookFixture(t *testing.T) (*FeishuController, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	manager := connector.NewManager(nil, zap.NewNop().Sugar())
	require.NoError(t, manager.Provision(context.Background(), connector.Config{Name: "feishu", TenantID: 17, Enabled: true, Credentials: map[string]string{"app_id": "test-app", "app_secret": "test-secret", "encrypt_key": "test-key", "verification_token": "test-token"}, Settings: map[string]interface{}{"callbackInstanceId": "c83503e86cc5468aaab482cd204f30fa"}}))
	c := NewFeishuController(manager, nil, nil, zap.NewNop().Sugar())
	r := gin.New()
	r.POST("/api/v1/feishu/webhook/:instance_id", c.Webhook)
	return c, r
}
func signedWebhook(body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/v1/feishu/webhook/c83503e86cc5468aaab482cd204f30fa", strings.NewReader(body))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256([]byte("test-key" + ts + "test-nonce" + body))
	req.Header.Set("X-Lark-Request-Timestamp", ts)
	req.Header.Set("X-Lark-Request-Nonce", "test-nonce")
	req.Header.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
	return req
}
func TestFeishuWebhookRejectsAmbiguousJSONBeforeChallenge(t *testing.T) {
	for name, body := range map[string]string{
		"type":         `{"type":"event_callback","type":"url_verification","token":"test-token","challenge":"x"}`,
		"token":        `{"type":"url_verification","token":"bad","token":"test-token","challenge":"x"}`,
		"header":       `{"type":"url_verification","token":"test-token","challenge":"x","header":{},"header":{}}`,
		"event":        `{"type":"url_verification","token":"test-token","challenge":"x","event":{},"event":{}}`,
		"nested array": `{"type":"url_verification","token":"test-token","challenge":"x","event":[{"id":1,"id":2}]}`,
		"malformed":    `{"type":`, "root array": `[]`, "two roots": `{} {}`,
		"oversized": `{"type":"url_verification","token":"test-token","challenge":"` + strings.Repeat("a", 1<<20) + `"}`,
		"overdeep":  `{"type":"url_verification","token":"test-token","extra":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, r := webhookFixture(t)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, signedWebhook(body))
			require.Equal(t, 400, w.Code)
			require.Empty(t, c.replayed, "invalid structure must not consume nonce")
		})
	}
}
func TestFeishuWebhookUnknownEventFailsClosed(t *testing.T) {
	_, r := webhookFixture(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedWebhook(`{"header":{"event_type":"unknown.required"},"event":{}}`))
	require.Equal(t, 400, w.Code)
}

type failedWebhookBody struct{ io.Reader }

func (failedWebhookBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failedWebhookBody) Close() error             { return nil }
func TestFeishuWebhookBodyReadFailure(t *testing.T) {
	c, r := webhookFixture(t)
	req := signedWebhook(`{}`)
	req.Body = failedWebhookBody{}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
	require.Empty(t, c.replayed)
}
func TestFeishuWebhookVerifiedChallengeAndOriginalSignature(t *testing.T) {
	body := "{ \n  \"type\": \"url_verification\", \"token\": \"test-token\", \"challenge\": \"quote\\\"value\", \"vendor_extra\": 9007199254740993 }"
	_, r := webhookFixture(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedWebhook(body))
	require.Equal(t, 200, w.Code)
	var result map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, "quote\"value", result["challenge"])
	for _, invalid := range []string{"token", "signature", "instance"} {
		t.Run(invalid, func(t *testing.T) {
			_, r := webhookFixture(t)
			req := signedWebhook(body)
			switch invalid {
			case "token":
				req = signedWebhook(strings.ReplaceAll(body, "test-token", "wrong-token"))
			case "signature":
				req.Header.Set("X-Lark-Signature", "wrong")
			case "instance":
				req.URL.Path = "/api/v1/feishu/webhook/17"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.NotEqual(t, 200, w.Code)
		})
	}
}

// Only the application side-effect boundary is replaced; real connector lookup,
// signature, replay, parsing and controller dispatch run for these requests.
type webhookTaskService struct {
	calls     int
	tenantID  int
	eventType string
	taskData  map[string]interface{}
	failure   error
}

func (s *webhookTaskService) SyncTicketToFeishu(context.Context, service.ActionActor, int, *feishu.Feishu) (*dto.FeishuTicketSyncResponse, error) {
	return nil, errors.New("unexpected outbound sync")
}
func (s *webhookTaskService) HandleTaskEvent(_ context.Context, tenantID int, fc *feishu.Feishu, eventType string, data map[string]interface{}) (*dto.FeishuWebhookResponse, error) {
	s.calls++
	s.tenantID = tenantID
	s.eventType = eventType
	s.taskData = data
	return &dto.FeishuWebhookResponse{EventType: eventType, Action: "created"}, s.failure
}
func TestFeishuWebhookSignedTaskDispatchAndReplay(t *testing.T) {
	c, r := webhookFixture(t)
	sideEffects := &webhookTaskService{}
	c.syncService = sideEffects
	body := `{ "header": {"event_type":"task.created","vendor_extension":true}, "event":{"task_guid":"task-17","creator_id":"open-23"} }`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedWebhook(body))
	require.Equal(t, 200, w.Code)
	var response common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, common.SuccessCode, response.Code)
	require.Equal(t, 1, sideEffects.calls)
	require.Equal(t, 17, sideEffects.tenantID)
	require.Equal(t, "task.created", sideEffects.eventType)
	require.Equal(t, "task-17", sideEffects.taskData["task_guid"])
	require.Equal(t, "open-23", sideEffects.taskData["creator_id"])
	w = httptest.NewRecorder()
	r.ServeHTTP(w, signedWebhook(body))
	require.Equal(t, 403, w.Code)
	require.Equal(t, 1, sideEffects.calls)
}
func TestFeishuWebhookDispatchFailureIsVisible(t *testing.T) {
	c, r := webhookFixture(t)
	sideEffects := &webhookTaskService{failure: errors.New("application rejected event")}
	c.syncService = sideEffects
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedWebhook(`{"header":{"event_type":"task.created"},"event":{"task_guid":"task-17"}}`))
	require.Equal(t, 500, w.Code)
	require.Equal(t, 1, sideEffects.calls)
}
func TestFeishuWebhookAmbiguousTaskNeverDispatches(t *testing.T) {
	c, r := webhookFixture(t)
	sideEffects := &webhookTaskService{}
	c.syncService = sideEffects
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedWebhook(`{"header":{"event_type":"task.created"},"event":{"task_guid":"first","task_guid":"second"}}`))
	require.Equal(t, 400, w.Code)
	require.Zero(t, sideEffects.calls)
}
