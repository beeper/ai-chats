package calc

import (
	"math"
	"strings"
	"testing"
)

func TestEvalExpression(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		want      float64
		wantErr   bool
		errSubstr string
	}{
		{name: "addition", expr: "2+3", want: 5},
		{name: "whitespace", expr: " 2 + 3 * 4 ", want: 14},
		{name: "parentheses", expr: "(2+3)*4", want: 20},
		{name: "negative", expr: "-5+2", want: -3},
		{name: "division", expr: "8/4", want: 2},
		{name: "modulo", expr: "7%3", want: 1},
		{name: "empty", expr: "", wantErr: true, errSubstr: "empty expression"},
		{name: "div zero", expr: "1/0", wantErr: true, errSubstr: "division by zero"},
		{name: "mod zero", expr: "1%0", wantErr: true, errSubstr: "modulo by zero"},
		{name: "missing paren", expr: "(1+2", wantErr: true, errSubstr: "missing closing parenthesis"},
		{name: "expected number", expr: "+1", wantErr: true, errSubstr: "expected number"},
		{name: "invalid number", expr: "1..2", wantErr: true, errSubstr: "invalid number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalExpression(tc.expr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !almostEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func almostEqual(a, b float64) bool {
	return a == b || math.Abs(a-b) < 1e-9
}
