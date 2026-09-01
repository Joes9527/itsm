package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/tenant"
	"itsm-backend/ent/user"
)

// AzureConfig holds Azure AD OIDC configuration.
type AzureConfig struct {
	TenantID              string
	ClientID              string
	ClientSecret          string
	RedirectURI           string
	ITSMTenantCode        string
	AllowUserProvisioning bool
}

func LoadAzureConfig() AzureConfig {
	return AzureConfig{
		TenantID:              os.Getenv("AZURE_TENANT_ID"),
		ClientID:              os.Getenv("AZURE_CLIENT_ID"),
		ClientSecret:          os.Getenv("AZURE_CLIENT_SECRET"),
		RedirectURI:           os.Getenv("AZURE_REDIRECT_URI"),
		ITSMTenantCode:        strings.TrimSpace(os.Getenv("AZURE_ITSM_TENANT_CODE")),
		AllowUserProvisioning: strings.EqualFold(os.Getenv("AZURE_ALLOW_USER_PROVISIONING"), "true"),
	}
}

func (c AzureConfig) IsConfigured() bool {
	return c.TenantID != "" && c.ClientID != "" && c.ClientSecret != "" && c.RedirectURI != ""
}

func (c AzureConfig) AuthorizeURL(state string) string {
	return fmt.Sprintf(
		"https://login.microsoftonline.com/%s/oauth2/v2.0/authorize?"+
			"client_id=%s&response_type=code&redirect_uri=%s&"+
			"response_mode=query&scope=%s&state=%s",
		c.TenantID, c.ClientID, url.QueryEscape(c.RedirectURI),
		url.QueryEscape("openid profile email User.Read"), state,
	)
}

func (c AzureConfig) TokenURL() string {
	return fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", c.TenantID)
}

type azureTokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

type azureUserInfo struct {
	OID           string `json:"id"`
	DisplayName   string `json:"displayName"`
	UserPrincipal string `json:"userPrincipalName"`
	Mail          string `json:"mail"`
}

type azureIdentityProvider interface {
	Exchange(context.Context, AzureConfig, string) (*azureTokenResponse, error)
	UserInfo(context.Context, string) (*azureUserInfo, error)
}

type microsoftAzureProvider struct{}

func (microsoftAzureProvider) Exchange(ctx context.Context, cfg AzureConfig, code string) (*azureTokenResponse, error) {
	return exchangeCode(ctx, cfg, code)
}

func (microsoftAzureProvider) UserInfo(ctx context.Context, accessToken string) (*azureUserInfo, error) {
	return getUserInfo(ctx, accessToken)
}

// exchangeCode exchanges authorization code for tokens.
func exchangeCode(ctx context.Context, cfg AzureConfig, code string) (*azureTokenResponse, error) {
	data := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"grant_type":    {"authorization_code"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var token azureTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, err
	}
	if token.Error != "" {
		return nil, fmt.Errorf("azure token error: %s - %s", token.Error, token.ErrorDesc)
	}
	return &token, nil
}

// getUserInfo fetches user profile from Microsoft Graph.
func getUserInfo(ctx context.Context, accessToken string) (*azureUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://graph.microsoft.com/v1.0/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info azureUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type azureOAuthState struct {
	Nonce      string `json:"nonce"`
	TenantCode string `json:"tenantCode"`
}

func generateTenantBoundState(tenantCode string) (string, error) {
	nonce, err := generateState()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(azureOAuthState{Nonce: nonce, TenantCode: strings.TrimSpace(tenantCode)})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func parseTenantBoundState(value string) (*azureOAuthState, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid state encoding")
	}
	var state azureOAuthState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, fmt.Errorf("invalid state payload")
	}
	state.TenantCode = strings.TrimSpace(state.TenantCode)
	if state.Nonce == "" || state.TenantCode == "" {
		return nil, fmt.Errorf("state is not tenant bound")
	}
	return &state, nil
}

// AzureLoginHandler redirects to Azure AD for authentication.
func AzureLoginHandler(cfg AzureConfig, logger *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.IsConfigured() {
			common.Fail(c, common.InternalErrorCode, "Azure AD not configured")
			return
		}
		tenantCode := strings.TrimSpace(c.Query("tenantCode"))
		if tenantCode == "" {
			tenantCode = cfg.ITSMTenantCode
		}
		if tenantCode == "" {
			common.Fail(c, common.AuthFailedCode, "Azure tenant context is required")
			return
		}
		state, err := generateTenantBoundState(tenantCode)
		if err != nil {
			common.Fail(c, common.InternalErrorCode, "failed to generate state")
			return
		}
		// Store state in a short-lived cookie under the same transport policy as
		// the resulting session cookies.
		authentication.WriteOAuthStateCookie(c.Writer, c.Request, state, 600)
		c.Redirect(http.StatusTemporaryRedirect, cfg.AuthorizeURL(state))
	}
}

// AzureCallbackHandler handles the Azure AD OIDC callback.
func AzureCallbackHandler(cfg AzureConfig, client *ent.Client, jwtSecret string, logger *zap.SugaredLogger) gin.HandlerFunc {
	return azureCallbackHandler(cfg, client, jwtSecret, logger, microsoftAzureProvider{}, time.Now)
}

func azureCallbackHandler(cfg AzureConfig, client *ent.Client, jwtSecret string, logger *zap.SugaredLogger, provider azureIdentityProvider, now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.IsConfigured() {
			common.Fail(c, common.InternalErrorCode, "Azure AD not configured")
			return
		}

		// Validate state
		state := c.Query("state")
		cookieState, _ := c.Cookie("azure_oauth_state")
		if state == "" || state != cookieState {
			common.Fail(c, common.AuthFailedCode, "invalid state")
			return
		}
		boundState, err := parseTenantBoundState(state)
		if err != nil {
			common.Fail(c, common.AuthFailedCode, "invalid tenant-bound state")
			return
		}
		authentication.WriteOAuthStateCookie(c.Writer, c.Request, "", -1)

		code := c.Query("code")
		if code == "" {
			common.Fail(c, common.AuthFailedCode, "missing authorization code")
			return
		}

		// Exchange code for tokens
		token, err := provider.Exchange(c.Request.Context(), cfg, code)
		if err != nil {
			logger.Errorw("azure token exchange failed", "error", err)
			common.Fail(c, common.AuthFailedCode, "Azure authentication failed")
			return
		}

		// Get user info from Microsoft Graph
		info, err := provider.UserInfo(c.Request.Context(), token.AccessToken)
		if err != nil {
			logger.Errorw("azure user info fetch failed", "error", err)
			common.Fail(c, common.AuthFailedCode, "failed to get user info")
			return
		}

		email := info.Mail
		if email == "" {
			email = info.UserPrincipal
		}

		// Resolve the explicitly selected ITSM tenant before looking up an actor.
		// Azure identity email is never used to select or guess a tenant.
		ctx := c.Request.Context()
		tenantEntity, err := client.Tenant.Query().Where(tenant.CodeEQ(boundState.TenantCode)).Only(ctx)
		if err != nil {
			logger.Warnw("azure tenant selection rejected", "tenant_code", boundState.TenantCode, "error", err)
			common.AuthFailed(c, "selected tenant is unavailable")
			return
		}
		// Email is globally unique in the authoritative User schema. Resolve one
		// exact actor, then let the shared tenant-session policy authorize the
		// selected tenant (native, super-admin, or allocated MSP). Never guess a
		// tenant from the identity or provision over an existing actor.
		u, err := client.User.Query().Where(user.EmailEQ(email)).Only(ctx)
		if ent.IsNotFound(err) {
			if !cfg.AllowUserProvisioning {
				common.AuthFailed(c, "Azure user is not provisioned for the selected tenant")
				return
			}
			u, err = client.User.Create().
				SetUsername(email).
				SetEmail(email).
				SetName(info.DisplayName).
				SetRole("end_user").
				SetPasswordHash("azure_oidc_no_password").
				SetActive(true).
				SetTenantID(tenantEntity.ID).
				Save(ctx)
			if err != nil {
				logger.Errorw("create azure user failed", "error", err, "email", email)
				common.Fail(c, common.InternalErrorCode, "failed to create user")
				return
			}
			logger.Infow("azure user created", "email", email, "user_id", u.ID)
		} else if err != nil {
			logger.Errorw("query user failed", "error", err)
			common.Fail(c, common.InternalErrorCode, "failed to lookup user")
			return
		}
		if !u.Active {
			common.AuthFailed(c, "user account is inactive")
			return
		}

		tenantEntity, err = authorization.AuthorizeTenantSession(ctx, client, u, tenantEntity.ID, now())
		if err != nil {
			logger.Warnw("azure tenant session denied", "user_id", u.ID, "tenant_id", u.TenantID, "error", err)
			common.AuthFailed(c, "tenant session is unavailable")
			return
		}
		role := string(u.Role)
		if u.MspRole != "" {
			if mappedRole := authorization.GetMSPRBACRole(string(u.MspRole)); mappedRole != "" {
				role = mappedRole
			}
		}

		session, err := authentication.IssueSessionTokens(authentication.SessionIdentity{
			UserID: u.ID, Username: u.Username, Role: role, TenantID: tenantEntity.ID,
		}, jwtSecret)
		if err != nil {
			logger.Errorw("session generation failed", "error", err)
			common.Fail(c, common.InternalErrorCode, "failed to generate session")
			return
		}

		authentication.WriteSessionCookies(c.Writer, c.Request, session)

		// Role-based redirect
		target := "/dashboard"
		switch role {
		case "end_user":
			target = "/tickets/create"
		case "agent", "technician":
			target = "/tickets"
		case "manager", "security":
			target = "/approvals"
		}
		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			frontendURL = "http://localhost:3000"
		}
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+target)
	}
}
