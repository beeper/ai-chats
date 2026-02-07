package calc

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// EvalExpression evaluates a simple arithmetic expression.
// Supports: +, -, *, /, %, and parentheses.
func EvalExpression(expr string) (float64, error) {
	expr = strings.ReplaceAll(expr, " ", "")
	if expr == "" {
		return 0, errors.New("empty expression")
	}

	// Simple recursive descent parser for basic arithmetic.
	pos := 0
	return parseExpression(expr, &pos)
}

func parseExpression(expr string, pos *int) (float64, error) {
	result, err := parseTerm(expr, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(expr) {
		op := expr[*pos]
		if op != '+' && op != '-' {
			break
		}
		*pos++
		right, err := parseTerm(expr, pos)
		if err != nil {
			return 0, err
		}
		if op == '+' {
			result += right
		} else {
			result -= right
		}
	}
	return result, nil
}

func parseTerm(expr string, pos *int) (float64, error) {
	result, err := parseFactor(expr, pos)
	if err != nil {
		return 0, err
	}

	for *pos < len(expr) {
		op := expr[*pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		*pos++
		right, err := parseFactor(expr, pos)
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			result *= right
		case '/':
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			result /= right
		case '%':
			if right == 0 {
				return 0, errors.New("modulo by zero")
			}
			result = math.Mod(result, right)
		}
	}
	return result, nil
}

func parseFactor(expr string, pos *int) (float64, error) {
	if *pos >= len(expr) {
		return 0, errors.New("unexpected end of expression")
	}

	// Handle parentheses.
	if expr[*pos] == '(' {
		*pos++
		result, err := parseExpression(expr, pos)
		if err != nil {
			return 0, err
		}
		if *pos >= len(expr) || expr[*pos] != ')' {
			return 0, errors.New("missing closing parenthesis")
		}
		*pos++
		return result, nil
	}

	// Handle negative numbers.
	negative := false
	if expr[*pos] == '-' {
		negative = true
		*pos++
	}

	// Parse number.
	start := *pos
	for *pos < len(expr) && (isDigit(expr[*pos]) || expr[*pos] == '.') {
		*pos++
	}

	if start == *pos {
		return 0, fmt.Errorf("expected number at position %d", start)
	}

	num, err := strconv.ParseFloat(expr[start:*pos], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", expr[start:*pos])
	}

	if negative {
		num = -num
	}
	return num, nil
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
