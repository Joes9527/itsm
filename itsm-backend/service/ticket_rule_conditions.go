package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"itsm-backend/ent"
)

// Both existing ticket rule owners use the same operational field vocabulary.
func evaluateTicketRuleConditions(conditions []map[string]interface{}, item *ent.Ticket) (bool, error) {
	matched := true
	for _, condition := range conditions {
		field, ok := condition["field"].(string)
		if !ok {
			return false, fmt.Errorf("ticket rule condition field is required")
		}
		operator, ok := condition["operator"].(string)
		if !ok {
			return false, fmt.Errorf("ticket rule condition operator is required")
		}
		value, ok := condition["value"]
		if !ok || value == nil {
			return false, fmt.Errorf("ticket rule condition value is required")
		}
		var actual interface{}
		switch field {
		case "status":
			actual = item.Status
		case "priority":
			actual = item.Priority
		case "category_id":
			actual = item.CategoryID
		case "department_id":
			actual = item.DepartmentID
		case "requester_id":
			actual = item.RequesterID
		case "assignee_id":
			actual = item.AssigneeID
		default:
			return false, fmt.Errorf("unsupported ticket rule condition field: %s", field)
		}
		one := false
		switch operator {
		case "equals", "not_equals":
			one = fmt.Sprint(actual) == fmt.Sprint(value)
			if operator == "not_equals" {
				one = !one
			}
		case "contains":
			needle, ok := value.(string)
			if !ok {
				return false, fmt.Errorf("ticket rule contains requires a string")
			}
			one = strings.Contains(fmt.Sprint(actual), needle)
		case "in", "not_in":
			values, ok := value.([]interface{})
			if !ok {
				return false, fmt.Errorf("ticket rule membership requires an array")
			}
			for _, v := range values {
				if fmt.Sprint(v) == fmt.Sprint(actual) {
					one = true
				}
			}
			if operator == "not_in" {
				one = !one
			}
		case "greater_than", "less_than":
			left, e1 := strconv.ParseFloat(fmt.Sprint(actual), 64)
			right, e2 := strconv.ParseFloat(fmt.Sprint(value), 64)
			if e1 != nil || e2 != nil || math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
				return false, fmt.Errorf("ticket rule ordering requires finite numbers")
			}
			if operator == "greater_than" {
				one = left > right
			} else {
				one = left < right
			}
		default:
			return false, fmt.Errorf("unsupported ticket rule operator: %s", operator)
		}
		matched = matched && one
	}
	return matched, nil
}
func ticketRulePositiveID(value interface{}) (int, error) {
	var raw string
	switch v := value.(type) {
	case int:
		raw = strconv.Itoa(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) {
			return 0, fmt.Errorf("rule reference must be a positive integer")
		}
		raw = strconv.FormatFloat(v, 'f', 0, 64)
	case json.Number:
		raw = string(v)
	default:
		return 0, fmt.Errorf("rule reference must be a positive integer")
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("rule reference must be a positive integer")
	}
	return id, nil
}
