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
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/user"
	"itsm-backend/middleware"
	"go.uber.org/zap"
)

// AzureConfig holds Azure AD OIDC configuration.
type AzureConfig struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

func LoadAzureConfig() AzureConfig {
	return AzureConfig{
		TenantID:     os.Getenv("AZURE_TENANT_ID"),
		ClientID:     os.Getenv("AZURE_CLIENT_ID"),
		ClientSecret: os.Getenv("AZURE_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("AZURE_REDIRECT_URI"),
	}
}

func (c AzureConfig) IsConfigured() bool {
	return c.TenantID != "" && c.ClientID != "" && c.ClientSecret != ""
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

// AzureLoginHandler redirects to Azure AD for authentication.
func AzureLoginHandler(cfg AzureConfig, logger *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.IsConfigured() {
			common.Fail(c, common.InternalErrorCode, "Azure AD not configured")
			return
		}
		state, err := generateState()
		if err != nil {
			common.Fail(c, common.InternalErrorCode, "failed to generate state")
			return
		}
		// Store state in a short-lived cookie
		c.SetCookie("azure_oauth_state", state, 600, "/", "", false, true)
		c.Redirect(http.StatusTemporaryRedirect, cfg.AuthorizeURL(state))
	}
}

// AzureCallbackHandler handles the Azure AD OIDC callback.
func AzureCallbackHandler(cfg AzureConfig, client *ent.Client, jwtSecret string, logger *zap.SugaredLogger) gin.HandlerFunc {
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
		c.SetCookie("azure_oauth_state", "", -1, "/", "", false, true)

		code := c.Query("code")
		if code == "" {
			common.Fail(c, common.AuthFailedCode, "missing authorization code")
			return
		}

		// Exchange code for tokens
		token, err := exchangeCode(c.Request.Context(), cfg, code)
		if err != nil {
			logger.Errorw("azure token exchange failed", "error", err)
			common.Fail(c, common.AuthFailedCode, "Azure authentication failed")
			return
		}

		// Get user info from Microsoft Graph
		info, err := getUserInfo(c.Request.Context(), token.AccessToken)
		if err != nil {
			logger.Errorw("azure user info fetch failed", "error", err)
			common.Fail(c, common.AuthFailedCode, "failed to get user info")
			return
		}

		email := info.Mail
		if email == "" {
			email = info.UserPrincipal
		}

		// Find or create user
		ctx := c.Request.Context()
		tenantID := 1 // default tenant
		u, err := client.User.Query().Where(user.EmailEQ(email), user.TenantIDEQ(tenantID)).First(ctx)
		if ent.IsNotFound(err) {
			u, err = client.User.Create().
				SetUsername(email).
				SetEmail(email).
				SetName(info.DisplayName).
				SetRole(user.RoleEndUser).
				SetPasswordHash("azure_oidc_no_password").
				SetActive(true).
				SetTenantID(tenantID).
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

		// Issue JWT (access token valid 24h)
		tokenStr, err := middleware.GenerateAccessToken(u.ID, u.Username, string(u.Role), tenantID, jwtSecret, 24*time.Hour)
		if err != nil {
			logger.Errorw("jwt generation failed", "error", err)
			common.Fail(c, common.InternalErrorCode, "failed to generate token")
			return
		}

		// Set token as httpOnly cookie (matching regular login flow)
		c.SetCookie(
			"access_token", tokenStr,
			86400, "/", "", false, true, // 24h, httpOnly, Secure=false (dev)
		)

		// Role-based redirect
		target := "/dashboard"
		switch string(u.Role) {
		case "end_user":
			target = "/service-catalog"
		case "agent", "technician":
			target = "/tickets"
		case "manager", "security":
			target = "/my-approvals"
		}
		frontendURL := os.Getenv("FRONTEND_URL")
		if frontendURL == "" {
			frontendURL = "http://localhost:3000"
		}
		c.Redirect(http.StatusTemporaryRedirect, frontendURL+target)
	}
}
