package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"itsm-backend/ent"
)

// TicketRuleMatch 是条件求值所需的工单上下文。
//
// CategoryPath 是工单分类的完整根路径（含最深节点），由拥有者在**自己的事务内**解析；
// 分类条件可能要求子树匹配，因此求值器必须拿到完整路径而不是单个最深节点 ID。
type TicketRuleMatch struct {
	Item         *ent.Ticket
	CategoryPath []CTINode
}

// Both existing ticket rule owners use the same operational field vocabulary.
func EvaluateTicketRuleConditions(match TicketRuleMatch, conditions []map[string]interface{}) (bool, error) {
	item := match.Item
	if item == nil {
		return false, fmt.Errorf("ticket rule evaluation requires a work item")
	}
	if len(match.CategoryPath) == 0 && item.CategoryID > 0 {
		// 有分类却没有解析出路径：这是解析失败而不是"未分类"，必须失败关闭，
		// 否则子树条件会静默不命中。
		return false, fmt.Errorf("ticket rule evaluation requires the classification path for category %d", item.CategoryID)
	}
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
		if field == "category_id" {
			one, err := evaluateCTICondition(match.CategoryPath, operator, value, condition["scope"])
			if err != nil {
				return false, err
			}
			if !one {
				matched = false
			}
			continue
		}
		var actual interface{}
		switch field {
		case "status":
			actual = item.Status
		case "priority":
			actual = item.Priority
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

// ctiConditionScope 解析分类条件的作用域；缺省为 exact（与既有行为一致），
// 未知取值必须失败，不得静默按精确处理。
func ctiConditionScope(raw interface{}) (CTIMatchScope, error) {
	if raw == nil {
		return CTIExact, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", ErrCTIUnknownMatchScope
	}
	switch strings.TrimSpace(value) {
	case "", string(CTIExact):
		return CTIExact, nil
	case string(CTISubtree):
		return CTISubtree, nil
	default:
		return "", ErrCTIUnknownMatchScope
	}
}

// ctiConditionIDs 归一化分类条件的取值，接受数字、字符串或数组。
func ctiConditionIDs(value interface{}) ([]int, error) {
	parse := func(raw interface{}) (int, error) {
		switch typed := raw.(type) {
		case float64:
			return int(typed), nil
		case int:
			return typed, nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err != nil {
				return 0, fmt.Errorf("ticket rule category condition requires numeric ids")
			}
			return parsed, nil
		default:
			return 0, fmt.Errorf("ticket rule category condition requires numeric ids")
		}
	}
	if list, ok := value.([]interface{}); ok {
		ids := make([]int, 0, len(list))
		for _, raw := range list {
			id, err := parse(raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	id, err := parse(value)
	if err != nil {
		return nil, err
	}
	return []int{id}, nil
}

// evaluateCTICondition 用共享的 MatchCTI 语义评估分类条件：
// exact 只命中工单的最深节点，subtree 命中完整路径上的任一祖先。
// 未分类（路径为空）视为不命中；其它算子对分类无定义，失败关闭。
func evaluateCTICondition(path []CTINode, operator string, value, rawScope interface{}) (bool, error) {
	scope, err := ctiConditionScope(rawScope)
	if err != nil {
		return false, err
	}
	switch operator {
	case "equals", "not_equals", "in", "not_in":
	default:
		return false, fmt.Errorf("unsupported category condition operator: %s", operator)
	}
	ids, err := ctiConditionIDs(value)
	if err != nil {
		return false, err
	}
	if len(path) == 0 {
		// 未分类工单不命中分类条件（与既有行为一致），且**不是**错误：
		// 若把空路径当作错误，任何带分类条件的规则都会让未分类工单的创建/分派整体失败。
		// 真正的解析失败由入口处 "有分类却没有路径" 的检查捕获。
		return operator == "not_equals" || operator == "not_in", nil
	}
	match := false
	for _, id := range ids {
		hit, err := MatchCTI(path, id, scope)
		if err != nil {
			return false, err
		}
		if hit {
			match = true
		}
	}
	if operator == "not_equals" || operator == "not_in" {
		return !match, nil
	}
	return match, nil
}
