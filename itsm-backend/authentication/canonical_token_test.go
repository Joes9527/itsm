package authentication

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestCanonicalTokenRejectsEquivalentSignatureEncoding(t *testing.T) {
	const secret = "private-canonical-token-test-secret"
	raw, err := GenerateAccessToken(7, "operator", "agent", 3, secret, time.Hour)
	require.NoError(t, err)
	_, err = validateToken(raw, secret, "access")
	require.NoError(t, err)
	parts := strings.Split(raw, ".")
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	index := strings.IndexByte(alphabet, parts[2][len(parts[2])-1])
	require.Zero(t, index&3)
	alternative := parts[2][:len(parts[2])-1] + string(alphabet[index+1])
	originalBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	alternateBytes, err := base64.RawURLEncoding.DecodeString(alternative)
	require.NoError(t, err)
	require.Equal(t, originalBytes, alternateBytes, "the variant has the same signature bytes")
	for name, signature := range map[string]string{"unused-tail-bits": alternative, "newline": parts[2][:5] + "\n" + parts[2][5:], "padding": parts[2] + "="} {
		t.Run(name, func(t *testing.T) {
			_, err := validateToken(parts[0]+"."+parts[1]+"."+signature, secret, "access")
			require.Error(t, err, "noncanonical encodings must be rejected before token-state lookup")
		})
	}
}

func TestCanonicalTokenRequiresCompleteIdentityAndHS256(t *testing.T) {
	const secret = "private-token-identity-test-secret"
	for _, purpose := range []string{"access", "refresh"} {
		for _, bad := range []string{"missing-expiry", "actor", "tenant", "role", "username", "algorithm"} {
			t.Run(purpose+"/"+bad, func(t *testing.T) {
				claims := Claims{UserID: 7, Username: "operator", Role: "agent", TenantID: 3, TokenType: purpose, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
				method := jwt.SigningMethodHS256
				switch bad {
				case "missing-expiry":
					claims.ExpiresAt = nil
				case "actor":
					claims.UserID = 0
				case "tenant":
					claims.TenantID = 0
				case "role":
					claims.Role = ""
				case "username":
					claims.Username = ""
				case "algorithm":
					method = jwt.SigningMethodHS384
				}
				raw, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
				require.NoError(t, err)
				_, err = validateToken(raw, secret, purpose)
				require.Error(t, err)
			})
		}
	}
}

func TestCanonicalCompactRejectsAlternativeEncoding(t *testing.T) {
	for _, raw := range []string{"e30.e30.AB", "e30.e30.AA=", "e30.e30.AA\n", "e30.e30", ".e30.AA", "e30..AA", "e30.e30.", "e30.e30.AA.extra"} {
		require.Error(t, validateCanonicalCompact(raw))
	}
}
