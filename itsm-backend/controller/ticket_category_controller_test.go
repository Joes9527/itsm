package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/common"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// respondCategoryError is the single mapping from CTI structural failures to
// stable API codes. It must not collapse business rejections into a generic
// 500, and it must not leak names/counts of cross-tenant or referencing objects.
func TestRespondCategoryErrorMapsStructuralFailuresToStableCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		name string
		err  error
		code int
	}{
		{"not_found", service.ErrCTICategoryNotFound, common.NotFoundCode},
		{"has_children", service.ErrCTICategoryHasChildren, common.ConflictCode},
		{"referenced", service.ErrCTICategoryReferenced, common.ConflictCode},
		{"published_catalog", service.ErrCTICategoryPublishedCatalog, common.ConflictCode},
		{"code_immutable", service.ErrCTICategoryCodeImmutable, common.ParamErrorCode},
		{"path_incomplete", service.ErrCTIPathIncomplete, common.ParamErrorCode},
		{"path_too_deep", service.ErrCTIPathTooDeep, common.ParamErrorCode},
		{"path_inactive", service.ErrCTIPathInactive, common.ParamErrorCode},
		{"path_hierarchy", service.ErrCTIPathHierarchy, common.ParamErrorCode},
		{"path_outside_tenant", service.ErrCTIPathOutsideTenant, common.ParamErrorCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)

			respondCategoryError(c, test.err)

			var response common.Response
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, test.code, response.Code, "body=%s", recorder.Body.String())
		})
	}
}

// An unrecognised failure stays an internal error: the mapping must not invent a
// business reason that the caller could act on incorrectly.
func TestRespondCategoryErrorKeepsUnknownFailuresInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	respondCategoryError(c, assert.AnError)

	var response common.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, common.InternalErrorCode, response.Code)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}
