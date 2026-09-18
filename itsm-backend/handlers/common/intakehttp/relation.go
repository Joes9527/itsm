package intakehttp

import (
	"github.com/gin-gonic/gin"
	"itsm-backend/dto"
)

// BindRelation is the single strict HTTP wire boundary for shared and professional relation adapters.
func BindRelation(c *gin.Context) (*dto.WorkItemRelationRequest, bool) {
	var req dto.WorkItemRelationRequest
	if !Bind(c, &req) {
		return nil, false
	}
	value, _ := c.Get("intake.http.body")
	fields, _ := value.(map[string]any)
	for field, value := range fields {
		if value == nil {
			Fail(c, Invalid(field, "null is not supported"))
			return nil, false
		}
	}
	if metadata, ok := fields["metadata"].(map[string]any); ok {
		for field, value := range metadata {
			if value == nil {
				Fail(c, Invalid("metadata."+field, "null is not supported"))
				return nil, false
			}
		}
	}
	return &req, true
}
