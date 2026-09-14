package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"itsm-backend/common/tenantctx"
)

// VerifiedCredential is an immutable token identity. Only successful signature,
// purpose, expiry and canonical-encoding validation can populate its fields.
// A zero value is invalid. No raw token or signing key is retained here.
type VerifiedCredential struct {
	digest    string
	tenantID  int
	actorID   int
	expiresAt time.Time
	purpose   string
}

func verifiedCredential(raw string, claims *Claims) *VerifiedCredential {
	digest := sha256.Sum256([]byte(raw))
	return &VerifiedCredential{digest: hex.EncodeToString(digest[:]), tenantID: claims.TenantID, actorID: claims.UserID, expiresAt: claims.ExpiresAt.Time, purpose: claims.TokenType}
}

// Credential returns the opaque identity attached during signature validation.
// Caller-created or decoded Claims have no credential and cannot authorize state writes.
func (c *Claims) Credential() *VerifiedCredential {
	if c == nil {
		return nil
	}
	return c.credential
}

func credentialContext(ctx context.Context, c *VerifiedCredential, purpose string) (context.Context, error) {
	if ctx == nil || c == nil || (purpose != "access" && purpose != "refresh") || c.purpose != purpose || c.tenantID <= 0 || c.actorID <= 0 || len(c.digest) != 64 || !time.Now().Before(c.expiresAt) {
		return nil, errors.New("invalid verified token identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != c.tenantID {
		return nil, errors.New("token tenant context mismatch")
	}
	return tenantctx.WithTenantID(ctx, c.tenantID), nil
}
