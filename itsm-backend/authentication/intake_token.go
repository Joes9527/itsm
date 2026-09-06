package authentication

import (
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"time"
)

// IntakeClaims are accepted only at explicit Intake capability routes.
type IntakeClaims struct {
	UserID         int      `json:"userId"`
	TenantID       int      `json:"tenantId"`
	Role           string   `json:"role"`
	TokenType      string   `json:"tokenType"`
	Scope          []string `json:"scope"`
	Provider       string   `json:"provider"`
	Channel        string   `json:"channel"`
	EventID        string   `json:"eventId"`
	MappingID      int      `json:"mappingId"`
	MappingVersion int      `json:"mappingVersion"`
	jwt.RegisteredClaims
}

func GenerateIntakeToken(claims IntakeClaims, secret string, ttl time.Duration) (string, error) {
	if secret == "" || ttl < time.Second {
		return "", errors.New("intake signing unavailable")
	}
	claims.RegisteredClaims = jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"itsm-intake"}, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)), ID: uuid.NewString()}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}
func ValidateIntakeToken(raw, secret string) (*IntakeClaims, error) {
	if secret == "" {
		return nil, errors.New("intake signing unavailable")
	}
	claims := &IntakeClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(_ *jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("itsm-intake"), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !token.Valid || claims.TokenType != "intake" || claims.UserID <= 0 || claims.TenantID <= 0 || claims.MappingID <= 0 || claims.MappingVersion <= 0 || claims.Provider == "" || claims.Channel == "" || claims.EventID == "" || claims.Role == "" || claims.ID == "" || len(claims.Audience) != 1 {
		return nil, errors.New("invalid intake claims")
	}
	return claims, nil
}
