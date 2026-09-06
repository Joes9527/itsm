package service

import (
	"encoding/json"
	"fmt"
	"itsm-backend/common"
)

// SLA declarations use the shared exact JSON number parser and domain bounds.
func slaConfigurationInteger(value interface{}, minimum, maximum int) (int, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("invalid SLA integer")
	}
	number, err := common.ParseExactJSONNumber(json.Number(raw))
	if err != nil || !number.IsInt() || !number.Num().IsInt64() {
		return 0, fmt.Errorf("SLA value must be an exact integer")
	}
	n := number.Num().Int64()
	if n < int64(minimum) || n > int64(maximum) {
		return 0, fmt.Errorf("SLA integer is out of range")
	}
	return int(n), nil
}
