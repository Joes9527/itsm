package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"itsm-backend/connector"
)

func TestFeishuDeliveryIdentityOwnsInitializedConfiguration(t *testing.T) {
	var sent atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["app_id"] != "original-app" {
				http.Error(w, "wrong identity", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"local-only-token","expire":3600}`)
			return
		}
		sent.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"task":{"guid":"local-task"}}}`)
	}))
	defer receiver.Close()
	cfg := connector.Config{Credentials: map[string]string{"app_id": "original-app", "app_secret": "local-only-secret"}, Settings: map[string]interface{}{"base_url": receiver.URL, "callbackInstanceId": "original-callback"}}
	target := New()
	require.NoError(t, target.Init(context.Background(), cfg))
	identity := target.TaskDestinationIdentity()
	cfg.Credentials["app_id"] = "mutated-app"
	cfg.Settings["callbackInstanceId"] = "mutated-callback"
	cfg.Settings["base_url"] = "https://unreachable.example.invalid"
	assert.Equal(t, identity, target.TaskDestinationIdentity())
	assert.Equal(t, "original-callback", target.CallbackInstanceID())
	task, err := target.CreateTask(context.Background(), &FeishuTask{Name: "local task"})
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, "local-task", task.GUID)
	assert.Equal(t, int32(1), sent.Load())
}

func TestFeishuInitializedInstanceRejectsRebinding(t *testing.T) {
	target := New()
	cfg := connector.Config{Credentials: map[string]string{"app_id": "first-app", "app_secret": "local-only"}, Settings: map[string]interface{}{"base_url": "http://127.0.0.1:1"}}
	require.NoError(t, target.Init(context.Background(), cfg))
	original := target.TaskDestinationIdentity()
	cfg.Credentials["app_id"] = "replacement-app"
	err := target.Init(context.Background(), cfg)
	require.Error(t, err)
	assert.Equal(t, original, target.TaskDestinationIdentity())
}

func TestFeishuTokenDoesNotFollowRedirect(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		fmt.Fprint(w, `{"code":0,"tenant_access_token":"wrong-target-token","expire":3600}`)
	}))
	defer other.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := NewClient(source.URL, "local-app", "local-only-secret", "", "")
	token, err := client.Token(context.Background())
	assert.Error(t, err)
	assert.Empty(t, token)
	assert.Zero(t, redirected.Load(), "credentials must not be forwarded to another target")
}

func TestFeishuDescribesTargetWithoutActivationOrSecret(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer receiver.Close()
	cfg := connector.Config{Credentials: map[string]string{"app_id": "local-app"}, Settings: map[string]interface{}{"base_url": receiver.URL}}
	target := New()
	describer, ok := any(target).(connector.DeliveryDestinationDescriber)
	require.True(t, ok, "durable producers need a pure target description")
	digest, err := describer.DescribeDeliveryDestination(cfg)
	require.NoError(t, err)
	require.Len(t, digest, 64)
	require.Error(t, target.Init(context.Background(), cfg), "activation still requires a credential")
	cfg.Credentials["app_secret"] = "local-only"
	require.NoError(t, target.Init(context.Background(), cfg))
	bound, ok := any(target).(connector.DeliveryDestination)
	require.True(t, ok)
	assert.Equal(t, digest, bound.DeliveryDestinationIdentity())
	assert.Equal(t, digest, target.TaskDestinationIdentity())
	assert.Zero(t, calls.Load(), "description and initialization must not perform network IO")
}

func TestFeishuTaskDoesNotFollowRedirect(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"task":{"guid":"wrong-target-task"}}}`)
	}))
	defer other.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"local-token","expire":3600}`)
			return
		}
		w.Header().Set("Location", other.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		fmt.Fprint(w, `{"code":0,"data":{"task":{"guid":"redirect-is-not-acceptance"}}}`)
	}))
	defer source.Close()
	client := NewClient(source.URL, "local-app", "local-only-secret", "", "")
	task, err := client.CreateTask(context.Background(), &FeishuTask{Name: "local task"})
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Zero(t, redirected.Load())
}
