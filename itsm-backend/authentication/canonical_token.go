package authentication

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode"
)

// A token's encoded form is part of its durable revocation identity. Reject
// alternate encodings rather than normalizing them into an accepted token.
func validateCanonicalCompact(raw string) error {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return errors.New("invalid compact token encoding")
	}
	for _, part := range parts {
		if part == "" {
			return errors.New("empty compact token segment")
		}
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(part)
		if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != part {
			return errors.New("noncanonical compact token encoding")
		}
	}
	return nil
}
