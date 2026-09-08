package workitemcreation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ConfigurationRevision hashes the exact declared configuration supplied by its
// owner. It never strips arbitrary user-defined configuration keys.
func ConfigurationRevision(prefix string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", NewInternalFailure("could not encode configuration revision", err)
	}
	hash := sha256.Sum256(raw)
	return prefix + ":" + hex.EncodeToString(hash[:]), nil
}
