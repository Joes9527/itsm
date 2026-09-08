package authentication

import (
	"context"
	"encoding/json"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

type loginAuditRequestKey struct{}

type LoginAuditRequest struct{ IP, UserAgent string }

func WithLoginAuditRequest(ctx context.Context, ip, userAgent string) context.Context {
	return context.WithValue(ctx, loginAuditRequestKey{}, LoginAuditRequest{IP: ip, UserAgent: userAgent})
}

func RecordLoginAudit(ctx context.Context, client *ent.Client, userID, tenantID int, username, action, failureReason string) {
	if client == nil {
		return
	}
	req, _ := ctx.Value(loginAuditRequestKey{}).(LoginAuditRequest)
	payload, _ := json.Marshal(map[string]string{"username": username, "userAgent": req.UserAgent, "failureReason": failureReason})
	auditCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if tenantID > 0 {
		auditCtx = tenantctx.WithTenantID(auditCtx, tenantID)
	} else {
		auditCtx = tenantctx.SystemContext(auditCtx, "auth:audit", "record failed authentication without a resolved tenant")
	}
	err := client.AuditLog.Create().SetCreatedAt(time.Now()).SetTenantID(tenantID).SetUserID(userID).
		SetIP(req.IP).SetResource("auth").SetAction(action).SetPath("/api/v1/auth/login").
		SetMethod("POST").SetStatusCode(map[bool]int{true: 200, false: 401}[action == "LOGIN_SUCCESS"]).
		SetRequestBody(string(payload)).Exec(auditCtx)
	if err != nil {
		zap.S().Errorw("failed to save login audit", "error", err, "action", action)
	}
}
