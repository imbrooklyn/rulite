package cel_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite/cel"
)

func FuzzNativeComparison(f *testing.F) {
	f.Add(int64(12000), int64(10000), true)
	f.Add(int64(-1), int64(0), false)
	f.Add(int64(0), int64(0), true)
	c := compiler[price](f, cel.WithCostLimit(100))
	f.Fuzz(func(t *testing.T, total, threshold int64, vip bool) {
		total, threshold = total%1000000, threshold%1000000
		source := fmt.Sprintf("input.Total >= %d && input.VIP", threshold)
		check := condition(t, c, source)
		input := price{Total: total, VIP: vip}
		before := input
		got, err := check(context.Background(), &input)
		if err != nil || got != (total >= threshold && vip) {
			t.Fatal("native comparison differs from Go oracle")
		}
		if !reflect.DeepEqual(input, before) {
			t.Fatal("CEL mutated input")
		}
	})
}

func FuzzBoundedCompile(f *testing.F) {
	for _, seed := range []string{
		"true", "false", "input.Total > 0", "input.Items.all(x, x > 0)",
		"1 / input.Total > 0", "input.Items[100] == 0", "input.Missing > 0", "(",
		"input.Country.matches('[')", "input.Country.matches('a{2}')", "dyn(input.Total)",
	} {
		f.Add(seed)
	}
	c := compiler[price](f, cel.WithCostLimit(100))
	f.Fuzz(func(t *testing.T, source string) {
		// Bound both source bytes and aggregate delimiter count, hence nesting.
		if len(source) > 256 || strings.Count(source, "(")+strings.Count(source, "[")+strings.Count(source, "{") > 16 {
			t.Skip()
		}
		check, err := c.Compile(source)
		if err != nil {
			var ce *cel.CompileError
			if check != nil || !errors.As(err, &ce) || ce.Unwrap() == nil {
				t.Fatal("compile error contract changed")
			}
			return
		}
		input := price{Country: "aa", Items: []int64{1, 2}}
		before := price{Country: "aa", Items: []int64{1, 2}}
		first, firstErr := check(context.Background(), &input)
		second, secondErr := check(context.Background(), &input)
		// A pure bounded expression must repeat its outcome and failure class.
		if first != second || (firstErr == nil) != (secondErr == nil) || !reflect.DeepEqual(input, before) {
			t.Fatal("evaluation is not repeatable and read-only")
		}
		if firstErr != nil {
			var runtimeErr *cel.RuntimeError
			if first || !errors.As(firstErr, &runtimeErr) || firstErr.Error() != secondErr.Error() || errors.Is(firstErr, cel.ErrCostLimit) != errors.Is(secondErr, cel.ErrCostLimit) {
				t.Fatal("runtime error contract changed")
			}
		}
	})
}
