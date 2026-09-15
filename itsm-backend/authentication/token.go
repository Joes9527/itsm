package authentication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour
)

type SessionIdentity struct {
	UserID   int
	Username string
	Role     string
	TenantID int
}

type SessionTokens struct {
	AccessToken  string
	RefreshToken string
}

var (
	ErrAccessTokenRevoked         = errors.New("access token is revoked")
	ErrAccessTokenRevocationCheck = errors.New("access token revocation check failed")
)

type Claims struct {
	credential *VerifiedCredential
	UserID     int    `json:"userId"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	TenantID   int    `json:"tenantId"`
	TokenType  string `json:"tokenType"`
	jwt.RegisteredClaims
}

func validateToken(tokenString, jwtSecret, expectedType string) (*Claims, error) {
	if err := validateCanonicalCompact(tokenString); err != nil {
		return nil, err
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(jwtSecret), nil
	}, jwt.WithStrictDecoding(), jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if token == nil || !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims.TokenType != expectedType || (expectedType != "access" && expectedType != "refresh") || claims.ExpiresAt == nil || claims.UserID <= 0 || claims.TenantID <= 0 || claims.Username == "" || claims.Role == "" {
		return nil, jwt.ErrInvalidKey
	}
	claims.credential = verifiedCredential(tokenString, claims)
	return claims, nil
}

func ValidateAccessToken(ctx context.Context, tokenString, jwtSecret string) (*Claims, error) {
	claims, err := validateToken(tokenString, jwtSecret, "access")
	if err != nil {
		return nil, err
	}
	ctx, err = credentialContext(ctx, claims.credential, "access")
	if err != nil {
		return nil, err
	}
	revoked, err := IsAccessTokenRevoked(ctx, tokenString)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAccessTokenRevocationCheck, err)
	}
	if revoked {
		return nil, ErrAccessTokenRevoked
	}
	return claims, nil
}

func GenerateAccessToken(userID int, username, role string, tenantID int, jwtSecret string, expireTime time.Duration) (string, error) {
	claims := Claims{
		UserID: userID, Username: username, Role: role, TenantID: tenantID, TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expireTime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
}

func GenerateRefreshToken(userID int, username, role string, tenantID int, jwtSecret string, expireTime time.Duration) (string, error) {
	claims := Claims{
		UserID: userID, Username: username, Role: role, TenantID: tenantID, TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        fmt.Sprintf("rt-%d-%d-%d", userID, time.Now().UnixNano(), time.Now().UnixNano()%1000000),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expireTime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
}

func IssueSessionTokens(identity SessionIdentity, jwtSecret string) (*SessionTokens, error) {
	accessToken, err := GenerateAccessToken(identity.UserID, identity.Username, identity.Role, identity.TenantID, jwtSecret, AccessTokenTTL)
	if err != nil {
		return nil, err
	}
	refreshToken, err := GenerateRefreshToken(identity.UserID, identity.Username, identity.Role, identity.TenantID, jwtSecret, RefreshTokenTTL)
	if err != nil {
		return nil, err
	}
	return &SessionTokens{AccessToken: accessToken, RefreshToken: refreshToken}, nil
}
