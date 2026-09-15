package controller

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"
	"github.com/stretchr/testify/require"
)

func TestIncidentMutationSerializationConflictIsRetryableAndSanitized(t *testing.T) {
	for _, code := range []pqerror.Code{"40001"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		respondIncidentMutationError(ctx, fmt.Errorf("assign: %w", &pq.Error{Code: code, Message: "private database detail"}))
		require.Equal(t, 409, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "private database detail")
		require.Contains(t, recorder.Body.String(), `"retryable":true`)
	}
}
