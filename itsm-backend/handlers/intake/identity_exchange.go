package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/externalidentity"
	"itsm-backend/ent/user"
	"itsm-backend/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const maxIdentityAssertionBodyBytes int64 = 32 * 1024

type IdentityAssertion struct {
	Provider  string `json:"provider"`
	Subject   string `json:"subject"`
	Channel   string `json:"channel"`
	Workspace string `json:"workspace"`
	EventID   string `json:"eventId"`
	IssuedAt  int64  `json:"issuedAt"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type IdentityExchangeResponse struct {
	Token     string   `json:"token"`
	TokenType string   `json:"tokenType"`
	ExpiresIn int64    `json:"expiresIn"`
	Scope     []string `json:"scope"`
}

type NonceStore interface {
	Claim(context.Context, string, time.Duration) (bool, error)
}

type RedisNonceStore struct{ client *redis.Client }

func NewRedisNonceStore(client *redis.Client) *RedisNonceStore {
	return &RedisNonceStore{client: client}
}

func (s *RedisNonceStore) Claim(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("redis nonce store is unavailable")
	}
	digest := sha256.Sum256([]byte(nonce))
	return s.client.SetNX(ctx, "intake:identity-exchange:nonce:"+hex.EncodeToString(digest[:]), "1", ttl).Result()
}

type IdentityExchangeHandler struct {
	client          *ent.Client
	nonces          NonceStore
	exchangeSecret  string
	jwtSecret       string
	assertionMaxAge time.Duration
	tokenTTL        time.Duration
	now             func() time.Time
}

func NewIdentityExchangeHandler(client *ent.Client, nonces NonceStore, exchangeSecret, jwtSecret string, assertionMaxAge, tokenTTL time.Duration) *IdentityExchangeHandler {
	return &IdentityExchangeHandler{
		client: client, nonces: nonces, exchangeSecret: exchangeSecret, jwtSecret: jwtSecret,
		assertionMaxAge: assertionMaxAge, tokenTTL: tokenTTL, now: time.Now,
	}
}

func canonicalIdentityAssertion(assertion IdentityAssertion) string {
	return strings.Join([]string{
		strings.TrimSpace(assertion.Provider), strings.TrimSpace(assertion.Workspace),
		strings.TrimSpace(assertion.Subject), strings.TrimSpace(assertion.Channel),
		strings.TrimSpace(assertion.EventID), strconv.FormatInt(assertion.IssuedAt, 10),
		strings.TrimSpace(assertion.Nonce),
	}, "\n")
}

func signIdentityAssertion(assertion IdentityAssertion, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonicalIdentityAssertion(assertion)))
	return hex.EncodeToString(mac.Sum(nil))
}

func validAssertionFields(assertion IdentityAssertion) bool {
	fields := []string{assertion.Provider, assertion.Workspace, assertion.Subject, assertion.Channel, assertion.EventID, assertion.Nonce, assertion.Signature}
	for _, value := range fields {
		length := len(strings.TrimSpace(value))
		if length == 0 || length > 512 {
			return false
		}
	}
	return assertion.IssuedAt > 0
}

func (h *IdentityExchangeHandler) audit(ctx context.Context, tenantID, userID, status int, action, code, tokenID, requestID, ip string) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("identity exchange audit repository is unavailable")
	}
	if tenantID > 0 {
		ctx = tenantctx.WithTenantID(ctx, tenantID)
	}
	details := map[string]string{"code": code}
	if tokenID != "" {
		details["tokenId"] = tokenID
	}
	body, _ := json.Marshal(details)
	create := h.client.AuditLog.Create().
		SetRequestID(requestID).
		SetIP(ip).
		SetResource("intake_identity_exchange").
		SetAction(action).
		SetPath("/api/v1/intake/identity-exchange").
		SetMethod(http.MethodPost).
		SetStatusCode(status).
		SetRequestBody(string(body))
	if tenantID > 0 {
		create.SetTenantID(tenantID)
	}
	if userID > 0 {
		create.SetUserID(userID)
	}
	_, err := create.Save(ctx)
	return err
}

func exchangeFailure(c *gin.Context, status int, code, message string, retryable bool) {
	common.TypedFail(c, status, code, message, retryable, nil)
}

func (h *IdentityExchangeHandler) deny(c *gin.Context, tenantID, userID, status int, code, message string, retryable bool) {
	if h != nil {
		_ = h.audit(c.Request.Context(), tenantID, userID, status, "intake.identity_exchange.denied", code, "", c.GetString("request_id"), c.ClientIP())
	}
	exchangeFailure(c, status, code, message, retryable)
}

func (h *IdentityExchangeHandler) Exchange(c *gin.Context) {
	if h == nil || h.client == nil || strings.TrimSpace(h.exchangeSecret) == "" || strings.TrimSpace(h.jwtSecret) == "" {
		exchangeFailure(c, http.StatusServiceUnavailable, "IDENTITY_EXCHANGE_UNAVAILABLE", "identity exchange is unavailable", true)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxIdentityAssertionBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var assertion IdentityAssertion
	if err := decoder.Decode(&assertion); err != nil || !validAssertionFields(assertion) {
		h.deny(c, 0, 0, http.StatusBadRequest, "INVALID_ASSERTION", "connector assertion is invalid", false)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		h.deny(c, 0, 0, http.StatusBadRequest, "INVALID_ASSERTION", "connector assertion is invalid", false)
		return
	}

	now := h.now().UTC()
	issuedAt := time.Unix(assertion.IssuedAt, 0).UTC()
	if issuedAt.After(now.Add(5*time.Second)) || now.Sub(issuedAt) > h.assertionMaxAge {
		h.deny(c, 0, 0, http.StatusUnauthorized, "ASSERTION_EXPIRED", "connector assertion has expired", false)
		return
	}
	expected, decodeErr := hex.DecodeString(signIdentityAssertion(assertion, h.exchangeSecret))
	provided, signatureErr := hex.DecodeString(strings.TrimSpace(assertion.Signature))
	if decodeErr != nil || signatureErr != nil || len(expected) != len(provided) || subtle.ConstantTimeCompare(expected, provided) != 1 {
		h.deny(c, 0, 0, http.StatusUnauthorized, "INVALID_SIGNATURE", "connector assertion signature is invalid", false)
		return
	}
	if h.nonces == nil {
		h.deny(c, 0, 0, http.StatusServiceUnavailable, "REPLAY_PROTECTION_UNAVAILABLE", "identity exchange is temporarily unavailable", true)
		return
	}
	claimed, err := h.nonces.Claim(c.Request.Context(), strings.TrimSpace(assertion.Nonce), h.assertionMaxAge)
	if err != nil {
		h.deny(c, 0, 0, http.StatusServiceUnavailable, "REPLAY_PROTECTION_UNAVAILABLE", "identity exchange is temporarily unavailable", true)
		return
	}
	if !claimed {
		h.deny(c, 0, 0, http.StatusUnauthorized, "ASSERTION_REPLAYED", "connector assertion was already used", false)
		return
	}

	// The signed provider/workspace/subject tuple is the identity root and has
	// no tenant supplied by the caller. Resolve that globally through the
	// audited connector bootstrap boundary, then immediately return to a
	// tenant-scoped context for every subsequent read/write.
	lookupCtx := tenantctx.SystemContext(c.Request.Context(), "intake:identity_exchange", "resolve a verified external identity to its configured tenant")
	mapping, err := h.client.ExternalIdentity.Query().Where(
		externalidentity.ProviderEQ(strings.TrimSpace(assertion.Provider)),
		externalidentity.WorkspaceEQ(strings.TrimSpace(assertion.Workspace)),
		externalidentity.SubjectEQ(strings.TrimSpace(assertion.Subject)),
		externalidentity.ActiveEQ(true),
	).Only(lookupCtx)
	if err != nil {
		h.deny(c, 0, 0, http.StatusUnauthorized, "IDENTITY_NOT_MAPPED", "external identity is not mapped", false)
		return
	}
	tenantContext := tenantctx.WithTenantID(c.Request.Context(), mapping.TenantID)
	mappedUser, err := h.client.User.Query().Where(
		user.IDEQ(mapping.UserID), user.TenantIDEQ(mapping.TenantID), user.ActiveEQ(true),
	).Only(tenantContext)
	if err != nil {
		h.deny(c, mapping.TenantID, mapping.UserID, http.StatusUnauthorized, "MAPPED_USER_INACTIVE", "mapped identity is unavailable", false)
		return
	}
	if !middleware.HasResourcePermission(h.client, mappedUser.Role, "intake", "create", mapping.TenantID) {
		h.deny(c, mapping.TenantID, mappedUser.ID, http.StatusForbidden, "INTAKE_PERMISSION_DENIED", "mapped identity cannot create work items", false)
		return
	}
	token, tokenClaims, err := middleware.GenerateIntakeToken(middleware.IntakeTokenIdentity{
		UserID: mappedUser.ID, Username: mappedUser.Username, Role: mappedUser.Role, TenantID: mapping.TenantID,
		Channel: strings.TrimSpace(assertion.Channel), Provider: strings.TrimSpace(assertion.Provider),
	}, h.jwtSecret, h.tokenTTL)
	if err != nil {
		h.deny(c, mapping.TenantID, mappedUser.ID, http.StatusServiceUnavailable, "TOKEN_ISSUE_FAILED", "identity exchange is temporarily unavailable", true)
		return
	}
	if err := h.audit(c.Request.Context(), mapping.TenantID, mappedUser.ID, http.StatusOK, "intake.identity_exchange.succeeded", "TOKEN_ISSUED", tokenClaims.ID, c.GetString("request_id"), c.ClientIP()); err != nil {
		exchangeFailure(c, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE", "identity exchange is temporarily unavailable", true)
		return
	}
	common.Success(c, IdentityExchangeResponse{
		Token: token, TokenType: middleware.IntakeTokenType, ExpiresIn: int64(h.tokenTTL.Seconds()), Scope: []string{middleware.IntakeCreateScope},
	})
}
