package jsonutil

import "testing"

func TestToMap(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := ToMap(nil); got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})

	t.Run("passthrough map", func(t *testing.T) {
		src := map[string]any{"a": "b"}
		got := ToMap(src)
		if got == nil {
			t.Fatal("expected map")
		}
		if got["a"] != "b" {
			t.Fatalf("unexpected value: %#v", got)
		}
		got["a"] = "c"
		if src["a"] != "c" {
			t.Fatal("expected map input to be returned as-is")
		}
	})

	t.Run("struct", func(t *testing.T) {
		type payload struct {
			Name string `json:"name"`
		}
		got := ToMap(payload{Name: "test"})
		if got["name"] != "test" {
			t.Fatalf("unexpected map: %#v", got)
		}
	})
}

func TestDeepCloneAny(t *testing.T) {
	original := map[string]any{
		"nested": map[string]any{
			"list": []any{
				map[string]any{"value": "x"},
			},
		},
		"strings": []string{"a", "b"},
		"ints":    []int{1, 2},
		"int64s":  []int64{3, 4},
		"floats":  []float64{1.5, 2.5},
		"bools":   []bool{true, false},
	}

	cloned, ok := DeepCloneAny(original).(map[string]any)
	if !ok {
		t.Fatalf("expected map clone, got %#v", cloned)
	}
	clonedNested := cloned["nested"].(map[string]any)
	clonedList := clonedNested["list"].([]any)
	clonedList[0].(map[string]any)["value"] = "y"
	cloned["strings"].([]string)[0] = "c"
	cloned["ints"].([]int)[0] = 9
	cloned["int64s"].([]int64)[0] = 10
	cloned["floats"].([]float64)[0] = 9.5
	cloned["bools"].([]bool)[0] = false

	if original["nested"].(map[string]any)["list"].([]any)[0].(map[string]any)["value"] != "x" {
		t.Fatal("nested map was not deep cloned")
	}
	if original["strings"].([]string)[0] != "a" {
		t.Fatal("[]string was not cloned")
	}
	if original["ints"].([]int)[0] != 1 {
		t.Fatal("[]int was not cloned")
	}
	if original["int64s"].([]int64)[0] != 3 {
		t.Fatal("[]int64 was not cloned")
	}
	if original["floats"].([]float64)[0] != 1.5 {
		t.Fatal("[]float64 was not cloned")
	}
	if original["bools"].([]bool)[0] != true {
		t.Fatal("[]bool was not cloned")
	}
}

func TestMergeRecursive(t *testing.T) {
	base := map[string]any{
		"top": "base",
		"nested": map[string]any{
			"a": "1",
			"b": []any{"x"},
		},
	}
	update := map[string]any{
		"nested": map[string]any{
			"b": []any{"y"},
			"c": "2",
		},
		"extra": true,
	}

	merged := MergeRecursive(base, update)
	if merged["top"] != "base" || merged["extra"] != true {
		t.Fatalf("unexpected merge result: %#v", merged)
	}
	nested := merged["nested"].(map[string]any)
	if nested["a"] != "1" || nested["c"] != "2" {
		t.Fatalf("nested merge failed: %#v", nested)
	}
	nested["b"].([]any)[0] = "z"
	if base["nested"].(map[string]any)["b"].([]any)[0] != "x" {
		t.Fatal("base mutated through merged value")
	}
	if update["nested"].(map[string]any)["b"].([]any)[0] != "y" {
		t.Fatal("update mutated through merged value")
	}
}
