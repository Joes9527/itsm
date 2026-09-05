package service

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/ast"
	exprruntime "github.com/expr-lang/expr/vm/runtime"
	"itsm-backend/common"
)

// The evaluation view owns numeric adaptation; frozen inputs and their digests
// remain untouched. Small integral values retain native indexing semantics.
func exactExpressionValue(value any) (any, bool, error) {
	switch v := value.(type) {
	case json.Number:
		n, err := common.ParseExactJSONNumber(v)
		if err != nil {
			return nil, true, err
		}
		return expressionNumberResult(n), true, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		found := false
		for key, item := range v {
			mapped, has, err := exactExpressionValue(item)
			if err != nil {
				return nil, has, err
			}
			out[key] = mapped
			found = found || has
		}
		return out, found, nil
	case []any:
		out := make([]any, len(v))
		found := false
		for i, item := range v {
			mapped, has, err := exactExpressionValue(item)
			if err != nil {
				return nil, has, err
			}
			out[i] = mapped
			found = found || has
		}
		return out, found, nil
	default:
		return value, false, nil
	}
}

// Patch the existing expr AST, retaining expr parsing, type checks, control flow,
// native operations and VM. Decimal literal text is read before float rounding.
type exactNumberPatcher struct {
	source []rune
	err    error
}

func (p *exactNumberPatcher) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.FloatNode:
		loc := n.Location()
		text := strings.ReplaceAll(string(p.source[loc.From:loc.To]), "_", "")
		value, err := common.ParseExactJSONNumber(json.Number(text))
		if err != nil {
			p.err = err
			return
		}
		ast.Patch(node, &ast.ConstantNode{Value: value})
		(*node).SetType(reflect.TypeOf(value))
	case *ast.BinaryNode:
		switch n.Operator {
		case "+", "-", "*", "/", "%", "==", "!=", "<", "<=", ">", ">=":
			ast.Patch(node, &ast.CallNode{Callee: &ast.IdentifierNode{Value: "__itsm_exact_binary"}, Arguments: []ast.Node{&ast.StringNode{Value: n.Operator}, n.Left, n.Right}})
		}
	case *ast.UnaryNode:
		if n.Operator == "-" || n.Operator == "+" {
			ast.Patch(node, &ast.CallNode{Callee: &ast.IdentifierNode{Value: "__itsm_exact_unary"}, Arguments: []ast.Node{&ast.StringNode{Value: n.Operator}, n.Node}})
		}
	}
}

func expressionRational(value any) (*big.Rat, error) {
	if n, ok := value.(*big.Rat); ok {
		return n, nil
	}
	var text string
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return nil, fmt.Errorf("decimal operation requires numeric operands")
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		text = strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		text = strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		text = strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits())
	default:
		return nil, fmt.Errorf("decimal operation requires numeric operands")
	}
	return common.ParseExactJSONNumber(json.Number(text))
}

func exactExpressionUnary(op string, value any) (any, error) {
	if n, ok := value.(*big.Rat); ok {
		if op == "-" {
			return new(big.Rat).Neg(n), nil
		}
		return new(big.Rat).Set(n), nil
	}
	if op == "-" {
		return exprruntime.Negate(value), nil
	}
	return value, nil
}

func expressionNumberResult(n *big.Rat) any {
	if n.IsInt() && n.Num().IsInt64() && n.Num().BitLen() < strconv.IntSize && n.Num().BitLen() <= 53 {
		return int(n.Num().Int64())
	}
	return n
}

func exactExpressionBinary(op string, left, right any) (any, error) {
	if (op == "==" || op == "!=") && (left == nil || right == nil) {
		equal := exprruntime.Equal(left, right)
		if op == "!=" {
			equal = !equal
		}
		return equal, nil
	}
	nativeArithmetic := false
	switch op {
	case "+", "-", "*", "/":
		_, leftErr := expressionRational(left)
		_, rightErr := expressionRational(right)
		nativeArithmetic = leftErr == nil && rightErr == nil
	}
	_, leftExact := left.(*big.Rat)
	_, rightExact := right.(*big.Rat)
	if !leftExact && !rightExact && !nativeArithmetic {
		switch op {
		case "+":
			return exprruntime.Add(left, right), nil
		case "-":
			return exprruntime.Subtract(left, right), nil
		case "*":
			return exprruntime.Multiply(left, right), nil
		case "/":
			return exprruntime.Divide(left, right), nil
		case "%":
			return exprruntime.Modulo(left, right), nil
		case "==":
			return exprruntime.Equal(left, right), nil
		case "!=":
			return !exprruntime.Equal(left, right), nil
		case "<":
			return exprruntime.Less(left, right), nil
		case "<=":
			return exprruntime.LessOrEqual(left, right), nil
		case ">":
			return exprruntime.More(left, right), nil
		case ">=":
			return exprruntime.MoreOrEqual(left, right), nil
		}
	}
	a, err := expressionRational(left)
	if err != nil {
		return nil, err
	}
	b, err := expressionRational(right)
	if err != nil {
		return nil, err
	}
	switch op {
	case "+":
		return expressionNumberResult(new(big.Rat).Add(a, b)), nil
	case "-":
		return expressionNumberResult(new(big.Rat).Sub(a, b)), nil
	case "*":
		return expressionNumberResult(new(big.Rat).Mul(a, b)), nil
	case "/":
		if b.Sign() == 0 {
			return nil, fmt.Errorf("decimal division by zero")
		}
		return expressionNumberResult(new(big.Rat).Quo(a, b)), nil
	case "==":
		return a.Cmp(b) == 0, nil
	case "!=":
		return a.Cmp(b) != 0, nil
	case "<":
		return a.Cmp(b) < 0, nil
	case "<=":
		return a.Cmp(b) <= 0, nil
	case ">":
		return a.Cmp(b) > 0, nil
	case ">=":
		return a.Cmp(b) >= 0, nil
	}
	return nil, fmt.Errorf("unsupported exact decimal operator %q", op)
}
