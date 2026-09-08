package bpmn

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"itsm-backend/common"
)

// CallbackInteger accepts integral values without truncation. Canonical strings
// preserve full integer identity through JSON persistence whose generic decoder
// otherwise rounds integers above 2^53. Ambiguous native floats fail closed.
func CallbackInteger(value any) (int, error) {
	switch v := value.(type) {
	case string:
		n, err := strconv.Atoi(v)
		if err != nil || strconv.Itoa(n) != v {
			return 0, fmt.Errorf("invalid integer")
		}
		return n, nil
	case json.Number:
		n, err := common.ParseExactJSONNumber(v)
		if err != nil || !n.IsInt() || !n.Num().IsInt64() {
			return 0, fmt.Errorf("invalid integer")
		}
		raw := n.Num().Int64()
		converted := int(raw)
		if int64(converted) != raw {
			return 0, fmt.Errorf("integer overflow")
		}
		return converted, nil
	case float32:
		if math.Abs(float64(v)) > 16777215 {
			return 0, fmt.Errorf("inexact integer")
		}
		return callbackFloatInteger(float64(v))
	case float64:
		return callbackFloatInteger(v)
	}
	v := reflect.ValueOf(value)
	if v.IsValid() {
		switch v.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n := v.Int()
			if int64(int(n)) == n {
				return int(n), nil
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n := v.Uint()
			if uint64(int(n)) == n && int(n) >= 0 {
				return int(n), nil
			}
		}
	}
	return 0, fmt.Errorf("invalid or overflowing integer")
}
func callbackFloatInteger(value float64) (int, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || math.Abs(value) > 9007199254740991 || value < float64(-int(^uint(0)>>1)-1) || value > float64(int(^uint(0)>>1)) {
		return 0, fmt.Errorf("nonintegral or inexact integer")
	}
	return int(value), nil
}
