package tools

import (
	"context"
	"fmt"

	"github.com/beeper/agentremote/pkg/shared/calc"
	"github.com/beeper/agentremote/pkg/shared/toolspec"
)

// Calculator is the calculator tool definition.
var Calculator = newBuiltinTool(
	toolspec.CalculatorName,
	toolspec.CalculatorDescription,
	"Calculator",
	toolspec.CalculatorSchema(),
	GroupCalc,
	executeCalculator,
)

// executeCalculator evaluates a simple arithmetic expression.
func executeCalculator(ctx context.Context, args map[string]any) (*Result, error) {
	expr, err := ReadString(args, "expression", true)
	if err != nil {
		return ErrorResult("calculator", err.Error()), nil
	}

	result, err := calc.EvalExpression(expr)
	if err != nil {
		return ErrorResult("calculator", fmt.Sprintf("calculation error: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"expression": expr,
		"result":     result,
		"formatted":  fmt.Sprintf("%.6g", result),
	}), nil
}
