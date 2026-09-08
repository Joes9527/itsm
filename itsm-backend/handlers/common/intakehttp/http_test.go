package intakehttp

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
	"net/http/httptest"
	"strings"
	"testing"
)

type captureApplication struct {
	calls    int
	identity creation.Identity
	command  creation.CreateWorkItemCommand
	result   *creation.CreateWorkItemResult
	err      error
}

func (a *captureApplication) Create(_ context.Context, i creation.Identity, c creation.CreateWorkItemCommand) (*creation.CreateWorkItemResult, error) {
	a.calls++
	a.identity = i
	a.command = c
	return a.result, a.err
}
func contextFor(body, key string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Idempotency-Key", key)
	c.Set("user_id", 7)
	c.Set("role", "agent")
	return c, w
}
func TestExecuteHTTPIdentityAndResult(t *testing.T) {
	for _, replayed := range []bool{false, true} {
		a := &captureApplication{result: &creation.CreateWorkItemResult{WorkItemID: 12, Number: "WI-12", RecordClass: "generic", Replayed: replayed}}
		c, w := contextFor("", "client-key")
		Execute(c, a, 3, 9, creation.CreateWorkItemCommand{Title: "Request"})
		expected := 201
		if replayed {
			expected = 200
		}
		require.Equal(t, expected, w.Code)
		require.Equal(t, creation.Identity{TenantID: 3, ActorID: 7, RequesterID: 9, Role: "agent", Channel: "http"}, a.identity)
		require.Equal(t, "client-key", a.command.IdempotencyKey)
		require.Equal(t, "confirmed", a.command.Confirmation)
		require.Contains(t, w.Body.String(), `"code":0`)
		require.Contains(t, w.Body.String(), `"workItemId":12`)
	}
}
func TestExecuteMissingKeyAndClassifiedFailure(t *testing.T) {
	a := &captureApplication{}
	c, w := contextFor("", "")
	Execute(c, a, 3, 0, creation.CreateWorkItemCommand{})
	require.Equal(t, 400, w.Code)
	require.Zero(t, a.calls)
	require.Contains(t, w.Body.String(), `"errorCode":"InvalidCommand"`)
	a.err = creation.NewPermissionDenied("denied", nil)
	c, w = contextFor("", "key")
	Execute(c, a, 3, 0, creation.CreateWorkItemCommand{})
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), `"retryable":false`)
}
func TestBindPreservesNumbersAndRejectsAmbiguity(t *testing.T) {
	type body struct {
		Values map[string]any `json:"values"`
	}
	c, _ := contextFor(`{"values":{"amount":9007199254740993.125}}`, "")
	var got body
	require.True(t, Bind(c, &got))
	require.Equal(t, json.Number("9007199254740993.125"), got.Values["amount"])
	for _, raw := range []string{`{"Values":{}}`, `{"values":{},"extra":1}`, `{"values":{"amount":1,"amount":2}}`, `{"values":{}} {}`, `null`} {
		c, w := contextFor(raw, "")
		var got body
		require.False(t, Bind(c, &got), raw)
		require.Equal(t, 400, w.Code, raw)
	}
}
