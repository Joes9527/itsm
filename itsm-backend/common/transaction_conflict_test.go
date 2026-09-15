package common

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestSerializationConflictUsesSQLStateAndPreservesOtherErrors(t *testing.T) {
	for _, err := range []error{nil, errors.New("40001 serialization_failure"), &pq.Error{Code: "23505"}, NewVersionConflictError("ticket", 1, 3, 4)} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		require.False(t, RespondSerializationConflict(ctx, err))
		require.Empty(t, recorder.Body.String())
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	require.True(t, RespondSerializationConflict(ctx, fmt.Errorf("transaction: %w", &pq.Error{Code: "40001", Detail: "hidden"})))
	require.Equal(t, 409, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "hidden")
}
