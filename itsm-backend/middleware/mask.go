package middleware

import (
	"encoding/json"
	"itsm-backend/common"
	"regexp"

	"github.com/gin-gonic/gin"
)

// Credential field names share one complete-key policy with input validation.
// Personal-data masking remains specific to request logging.
var maskRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`"(` + common.CredentialKeyPattern + `)"\s*:\s*"[^"\\]*(?:\\.[^"\\]*)*"`), `"$1":"***"`},
	{regexp.MustCompile(`(?i)"(credit[_-]?card|card[_-]?number|cc[_-]?num|cvv|cvc)"\s*:\s*"[^"]*"`), `"$1":"***"`},
	{regexp.MustCompile(`(?i)"(bank[_-]?account|routing[_-]?number|iban|swift|bic)"\s*:\s*"[^"]*"`), `"$1":"***"`},
	{regexp.MustCompile(`(?i)"(ssn|social[_-]?security|tax[_-]?id|national[_-]?id|id[_-]?number|passport[_-]?number)"\s*:\s*"[^"]*"`), `"$1":"***"`},
	{regexp.MustCompile(`(?i)"(phone|mobile|tel|telephone)"\s*:\s*"[^"]*"`), `"$1":"***"`},
	{regexp.MustCompile(`(?i)"(email|e-mail)"\s*:\s*"[^"]*"`), `"$1":"***"`},
	{regexp.MustCompile(`(?i)"(address|street|postal[_-]?code|zip[_-]?code)"\s*:\s*"[^"]*"`), `"$1":"***"`},
}

// MaskSensitiveFields masks sensitive fields in JSON request bodies for logging/audit.
// Complete credential keys and the existing personal-data keys are masked.
func MaskSensitiveFields(body string) string {
	masked := body
	for _, r := range maskRules {
		masked = r.re.ReplaceAllString(masked, r.repl)
	}
	return masked
}

// MaskResponseMiddleware is optional if we later log responses
func MaskResponseMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		// placeholder for future response masking if needed
		_ = json.Marshal
	}
}
