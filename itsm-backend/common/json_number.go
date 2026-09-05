package common

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ParseExactJSONNumber validates JSON decimal syntax before allocating an exact
// rational. Bounds limit parser work, not a domain's amount or routing policy.
func ParseExactJSONNumber(value json.Number) (*big.Rat, error) {
	text := string(value)
	if len(text) == 0 || len(text) > 1024 || strings.TrimSpace(text) != text || !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("invalid JSON number")
	}
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.Atoi(text[index+1:])
		if err != nil || exponent < -4096 || exponent > 4096 {
			return nil, fmt.Errorf("JSON number exponent exceeds parser limit")
		}
	}
	parsed, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("invalid JSON number")
	}
	return parsed, nil
}
