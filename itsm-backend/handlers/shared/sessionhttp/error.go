package sessionhttp

import (
	"errors"

	"itsm-backend/common"
	creation "itsm-backend/handlers/common/workitemcreation"

	"github.com/gin-gonic/gin"
)

// Fail returns sanitized session errors, never storage errors or native records.
func Fail(c *gin.Context, err error) {
	var failure *creation.IntakeError
	if errors.As(err, &failure) {
		codes := map[int]int{401: common.AuthFailedCode, 403: common.ForbiddenCode, 503: common.ServiceUnavailableCode}
		code, ok := codes[failure.HTTPStatus]
		if !ok {
			code = common.InternalErrorCode
		}
		common.FailWithData(c, code, failure.Message, gin.H{"errorCode": failure.Code, "retryable": failure.Retryable})
		return
	}
	common.InternalError(c, "会话服务暂不可用")
}
