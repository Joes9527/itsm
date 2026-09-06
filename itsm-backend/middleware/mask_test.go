package middleware

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMaskSensitiveFieldsPreservesCredentialAndPrivacyCoverage(t *testing.T) {
	for _, key := range []string{"password", "passwd", "pwd", "current_password", "new_password", "confirm_password", "api_key", "api-key", "apikey", "x-api-key", "access_token", "refresh-token", "idToken", "auth_token", "bearer_token", "jwt", "session_token", "csrf_token", "secret", "client_secret", "app_secret", "private_key", "signing_key", "hmac_key", "authorization", "proxy-authorization", "cookie", "set-cookie", "oauth_token", "feishu_token", "wecom_token", "dingtalk_token", "db_password", "database_password", "redis_password", "smtp_password", "token", "credit_card", "card_number", "cc_num", "cvv", "cvc", "bank_account", "routing_number", "iban", "swift", "bic", "ssn", "social_security", "tax_id", "national_id", "id_number", "passport_number", "phone", "mobile", "tel", "telephone", "email", "e-mail", "address", "street", "postal_code", "zip_code"} {
		t.Run(key, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{key: "credential-sentinel"})
			require.NoError(t, err)
			masked := MaskSensitiveFields(string(raw))
			require.NotContains(t, masked, "credential-sentinel")
			var result map[string]string
			require.NoError(t, json.Unmarshal([]byte(masked), &result))
			require.Equal(t, "***", result[key])
		})
	}
	input := `{"title":"keep","description":"keep","tokenCount":12,"nested":{"apiKey":"escaped\"credential-sentinel"}}`
	masked := MaskSensitiveFields(input)
	require.NotContains(t, masked, "credential-sentinel")
	require.Contains(t, masked, `"tokenCount":12`)
	require.Contains(t, masked, `"title":"keep"`)
	require.Contains(t, masked, `"description":"keep"`)
}
