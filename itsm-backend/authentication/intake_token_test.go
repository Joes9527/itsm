package authentication

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestIntakeAudienceScopeAndTokenTypeStaySeparate(t *testing.T) {
	claims := IntakeClaims{UserID: 1, TenantID: 1, Role: "requester", Provider: "kaf", Channel: "kaf_web", EventID: "submission", MappingID: 1, MappingVersion: 1, TokenType: "intake", Scope: []string{"intake:create"}}
	raw, err := GenerateIntakeToken(claims, "test-jwt", time.Minute)
	require.NoError(t, err)
	got, err := ValidateIntakeToken(raw, "test-jwt")
	require.NoError(t, err)
	require.Equal(t, []string{"intake:create"}, got.Scope)
	_, err = ValidateAccessToken(context.Background(), raw, "test-jwt")
	require.Error(t, err)
	for _, change := range []func(*IntakeClaims){func(c *IntakeClaims) { c.Audience = jwt.ClaimStrings{"other"} }, func(c *IntakeClaims) { c.Audience = nil }, func(c *IntakeClaims) { c.Audience = jwt.ClaimStrings{"itsm-intake", "other"} }, func(c *IntakeClaims) { c.TokenType = "access" }, func(c *IntakeClaims) { c.MappingVersion = 0 }, func(c *IntakeClaims) { c.ExpiresAt = nil }} {
		c := *got
		change(&c)
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte("test-jwt"))
		require.NoError(t, err)
		_, err = ValidateIntakeToken(raw, "test-jwt")
		require.Error(t, err)
	}
	raw, err = jwt.NewWithClaims(jwt.SigningMethodHS384, got).SignedString([]byte("test-jwt"))
	require.NoError(t, err)
	_, err = ValidateIntakeToken(raw, "test-jwt")
	require.Error(t, err)
}
