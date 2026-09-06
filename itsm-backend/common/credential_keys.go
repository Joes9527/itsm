package common

import (
	"regexp"
	"strings"
)

// CredentialKeyPattern is shared by request masking and credential rejection.
// Match complete field names, never business labels such as tokenCount.
const CredentialKeyPattern = `(?i:password|passwd|pwd|(?:current|new|confirm|db|database|redis|smtp)[ _-]*password|(?:x[ _-]*)?api[ _-]*key|token|(?:access|refresh|id|auth|bearer|session|csrf|oauth|feishu|wecom|dingtalk|verification)[ _-]*token|jwt|secret|(?:client|app|access[ _-]*key)[ _-]*secret|secret[ _-]*(?:access[ _-]*)?key|(?:private|signing|hmac|encrypt)[ _-]*key|authorization|proxy[ _-]*authorization|cookie|set[ _-]*cookie)`

var credentialKey = regexp.MustCompile(`^(?:` + CredentialKeyPattern + `)$`)

func IsCredentialKey(key string) bool {
	return credentialKey.MatchString(strings.TrimSpace(key))
}
