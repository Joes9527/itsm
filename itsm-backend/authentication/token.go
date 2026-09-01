package authentication

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID    int    `json:"userId"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	TenantID  int    `json:"tenantId"`
	TokenType string `json:"tokenType"`
	jwt.RegisteredClaims
}

func ValidateAccessToken(tokenString, jwtSecret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims.TokenType != "access" {
		return nil, jwt.ErrInvalidKey
	}
	return claims, nil
}

func ValidateRefreshToken(tokenString, jwtSecret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(jwtSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims.TokenType != "refresh" {
		return nil, jwt.ErrInvalidKey
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

func GenerateRefreshToken(userID int, jwtSecret string, expireTime time.Duration) (string, error) {
	claims := Claims{
		UserID: userID, TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        fmt.Sprintf("rt-%d-%d-%d", userID, time.Now().UnixNano(), time.Now().UnixNano()%1000000),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expireTime)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
}
