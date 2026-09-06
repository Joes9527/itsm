package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"itsm-backend/authentication"
	creation "itsm-backend/handlers/common/workitemcreation"
	"math"
	"strconv"
	"strings"
	"time"
)

type IdentityAssertion struct {
	Version   int    `json:"version"`
	Audience  string `json:"audience"`
	Purpose   string `json:"purpose"`
	Provider  string `json:"provider"`
	Workspace string `json:"workspace"`
	Subject   string `json:"subject"`
	Channel   string `json:"channel"`
	EventID   string `json:"eventId"`
	IssuedAt  int64  `json:"issuedAt"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}
type ExchangeResult struct {
	Token     string `json:"token"`
	TokenType string `json:"tokenType"`
	ExpiresIn int64  `json:"expiresIn"`
	Scope     string `json:"scope"`
}
type IdentityProvider struct {
	Secret   string
	Channels []string
	Purposes []string
}
type IdentityExchangeConfig struct {
	Providers                    map[string]IdentityProvider
	MaxAge, FutureSkew, TokenTTL time.Duration
}
type NonceStore interface {
	Claim(context.Context, string, time.Duration) (bool, error)
}
type IdentityExchangeService struct {
	config     IdentityExchangeConfig
	nonces     NonceStore
	now        func() time.Time
	repository IdentityRepository
	jwtSecret  string
}

func (s *IdentityExchangeService) verify(ctx context.Context, a IdentityAssertion, purpose string) error {
	if s == nil || s.nonces == nil || s.config.MaxAge <= 0 || s.config.TokenTTL <= 0 {
		return creation.NewInfrastructureUnavailable("identity exchange is unavailable", nil)
	}
	if a.Version != 2 || a.Audience != "itsm-intake" || a.Purpose != purpose || (purpose != "create" && purpose != "read") || a.IssuedAt <= 0 {
		return creation.NewAuthenticationRequired("invalid assertion protocol", nil)
	}
	fields := []string{a.Audience, a.Purpose, a.Provider, a.Workspace, a.Subject, a.Channel, a.EventID, a.Nonce, a.Signature}
	for _, f := range fields {
		if f == "" || len(f) > 512 || strings.TrimSpace(f) != f || strings.ContainsAny(f, "\r\n") {
			return creation.NewInvalidCommand("invalid assertion field", creation.FieldError{}, nil)
		}
	}
	p, ok := s.config.Providers[a.Provider]
	if !ok || !containsIdentityValue(p.Channels, a.Channel) || !containsIdentityValue(p.Purposes, purpose) {
		return creation.NewAuthenticationRequired("assertion provider or capability is unavailable", nil)
	}
	if p.Secret == "" {
		return creation.NewInfrastructureUnavailable("identity provider is unavailable", nil)
	}
	now := s.now()
	issued := time.Unix(a.IssuedAt, 0)
	deadline := issued.Add(s.config.MaxAge)
	if issued.After(now.Add(s.config.FutureSkew)) || !now.Before(deadline) {
		return creation.NewAuthenticationRequired("assertion expired", nil)
	}
	canonical := strings.Join([]string{"2", a.Audience, a.Purpose, a.Provider, a.Workspace, a.Subject, a.Channel, a.EventID, strconv.FormatInt(a.IssuedAt, 10), a.Nonce}, "\n")
	mac := hmac.New(sha256.New, []byte(p.Secret))
	mac.Write([]byte(canonical))
	signature, err := hex.DecodeString(a.Signature)
	if err != nil || len(signature) != sha256.Size || a.Signature != strings.ToLower(a.Signature) || !hmac.Equal(signature, mac.Sum(nil)) {
		return creation.NewAuthenticationRequired("invalid assertion signature", nil)
	}
	key, _ := json.Marshal([]string{a.Provider, a.Channel, a.Workspace, a.Nonce})
	ttl := time.Duration(math.Ceil(deadline.Sub(now).Seconds())) * time.Second
	claimed, err := s.nonces.Claim(ctx, string(key), ttl)
	if err != nil {
		return creation.NewInfrastructureUnavailable("identity nonce store unavailable", err)
	}
	if !claimed {
		return creation.NewAuthenticationRequired("assertion already consumed", nil)
	}
	return nil
}
func containsIdentityValue(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type RedisNonceStore struct{ client *redis.Client }

func NewRedisNonceStore(client *redis.Client) *RedisNonceStore {
	return &RedisNonceStore{client: client}
}
func (s *RedisNonceStore) Claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, errors.New("nonce store unavailable")
	}
	digest := sha256.Sum256([]byte(key))
	return s.client.SetNX(ctx, "intake:identity-exchange:nonce:"+hex.EncodeToString(digest[:]), "1", ttl).Result()
}
func NewIdentityExchangeService(cfg IdentityExchangeConfig, nonces NonceStore, repository IdentityRepository, jwtSecret string) *IdentityExchangeService {
	return &IdentityExchangeService{config: cfg, nonces: nonces, repository: repository, jwtSecret: jwtSecret, now: time.Now}
}
func (s *IdentityExchangeService) Exchange(ctx context.Context, a IdentityAssertion, purpose string) (*ExchangeResult, error) {
	if s == nil || s.repository == nil || s.jwtSecret == "" {
		return nil, creation.NewInfrastructureUnavailable("identity exchange unavailable", nil)
	}
	if err := s.verify(ctx, a, purpose); err != nil {
		return nil, err
	}
	mapping, identity, err := s.repository.Resolve(ctx, a.Provider, a.Workspace, a.Subject)
	if err != nil {
		return nil, err
	}
	scopes := []string{"intake:create"}
	if purpose == "read" {
		scopes = []string{"intake:catalog:read", "intake:workitem:read"}
	}
	claims := authentication.IntakeClaims{UserID: identity.ActorID, TenantID: identity.TenantID, Role: identity.Role, Provider: a.Provider, Channel: a.Channel, EventID: a.EventID, MappingID: mapping.ID, MappingVersion: mapping.Version, Scope: scopes, TokenType: "intake"}
	token, err := authentication.GenerateIntakeToken(claims, s.jwtSecret, s.config.TokenTTL)
	if err != nil {
		return nil, creation.NewInternalFailure("could not issue credential", err)
	}
	if err = s.repository.Audit(ctx, identity, "exchange."+purpose, mapping.ID); err != nil {
		return nil, err
	}
	return &ExchangeResult{Token: token, TokenType: "Bearer", ExpiresIn: int64(s.config.TokenTTL / time.Second), Scope: strings.Join(scopes, " ")}, nil
}

// ValidateCredential rechecks the current configured capability and mapped session.
func (s *IdentityExchangeService) ValidateCredential(ctx context.Context, c *authentication.IntakeClaims) (creation.Identity, error) {
	if s == nil || s.repository == nil {
		return creation.Identity{}, creation.NewInfrastructureUnavailable("identity exchange unavailable", nil)
	}
	p, ok := s.config.Providers[c.Provider]
	purpose := ""
	if len(c.Scope) == 1 && c.Scope[0] == "intake:create" {
		purpose = "create"
	}
	if len(c.Scope) == 2 && c.Scope[0] == "intake:catalog:read" && c.Scope[1] == "intake:workitem:read" {
		purpose = "read"
	}
	if !ok || p.Secret == "" || purpose == "" || !containsIdentityValue(p.Purposes, purpose) || !containsIdentityValue(p.Channels, c.Channel) {
		return creation.Identity{}, creation.NewAuthenticationRequired("identity provider capability revoked", nil)
	}
	return s.repository.Validate(ctx, c)
}
