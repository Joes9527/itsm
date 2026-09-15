package common

import (
	"errors"

	"github.com/gin-gonic/gin"
)

// RespondSerializationConflict exposes an aborted transaction as a retryable
// conflict, without inventing aggregate versions or exposing database detail.
// It recognizes driver SQLSTATE through wrapping, never error-message text.
func RespondSerializationConflict(ctx *gin.Context, err error) bool {
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "40001" {
		return false
	}
	Conflict(ctx, "数据已被并发修改，请刷新后重试", gin.H{"retryable": true})
	return true
}
