package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
)

// 工单编辑载荷是外部边界：已退役字段与拼错字段必须显式报错，不能被静默忽略。
// （与仓库既有的 intake / change / service catalog 边界一致，见 DisallowUnknownFields 用法。）
func TestTicketEditBindingRejectsRetiredAndUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/tickets/:id", func(c *gin.Context) {
		var req dto.UpdateTicketRequest
		if err := bindStrictTicketEditJSON(c, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "categoryId": req.CategoryID, "reason": req.ClassificationReason})
	})

	put := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/tickets/1", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	// 1) 退役字段（按显示名称解析分类）必须被拒绝 —— 而不是被静默忽略后"看起来成功"。
	retired := put(`{"category":"网络与远程访问服务","version":1,"operationId":"op-retired"}`)
	require.Equal(t, http.StatusBadRequest, retired.Code)
	require.Contains(t, retired.Body.String(), "category")

	// 2) 未知/拼错字段同样拒绝。
	unknown := put(`{"categroyId":12,"version":1,"operationId":"op-unknown"}`)
	require.Equal(t, http.StatusBadRequest, unknown.Code)

	// 3) 合法载荷（最深节点 ID + 原因）通过，并保留原因供服务层校验。
	valid := put(`{"categoryId":12,"classificationReason":"现场归类有误","version":1,"operationId":"op-valid"}`)
	require.Equal(t, http.StatusOK, valid.Code)
	require.Contains(t, valid.Body.String(), `"categoryId":12`)
	require.Contains(t, valid.Body.String(), "现场归类有误")

	// 4) 缺少 version/operationId 仍按既有校验失败（未放宽既有契约）。
	require.Equal(t, http.StatusBadRequest, put(`{"categoryId":12}`).Code)
}
