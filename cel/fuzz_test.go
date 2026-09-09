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

func FuzzTypedMapping(f *testing.F) {
	for _, source := range []string{
		"has(order.Child) && order.Child.Score >= 0", "order.Lookup['missing'].Score == 0",
		"order.Numbers.all(x, x >= 0)", "order.Nested.At == timestamp('0001-01-01T00:00:00Z')",
		"order.Numbers.map(x, x + 1).all(x, x > 0)", "dyn(order.Nested).Missing == 1", "order.renamed == 0", "(",
	} {
		f.Add(source, []byte{1, 2, 3})
	}
	b := builder[nativeMapping](f, cel.WithCostLimit(100))
	if err := b.Bind("order", func(_ context.Context, p *nativeMapping) (*nativeMapping, error) { return p, nil }); err != nil {
		f.Fatal(err)
	}
	c := built(f, b)
	f.Fuzz(func(t *testing.T, source string, data []byte) {
		if len(source) > 256 || len(data) > 64 || strings.Count(source, "(")+strings.Count(source, "[")+strings.Count(source, "{") > 16 {
			t.Skip()
		}
		first, err := c.Compile(source)
		second, again := c.Compile(source)
		if (err == nil) != (again == nil) {
			t.Fatal("compile outcome is not deterministic")
		}
		if err != nil {
			var a, b *cel.CompileError
			if first != nil || second != nil || !errors.As(err, &a) || !errors.As(again, &b) || a.Stage() != b.Stage() || a.Error() != b.Error() || a.Unwrap().Error() != b.Unwrap().Error() {
				t.Fatal("compile diagnostics diverged")
			}
			return
		}
		input := nativeMapping{Lookup: map[string]*address{}, Numbers: make(namedNumbers, len(data))}
		for i, value := range data {
			input.Numbers[i] = int64(value)
		}
		if len(data) > 0 {
			input.Child = &address{Score: int64(data[0])}
		}
		before := append(namedNumbers(nil), input.Numbers...)
		a, ae := first(context.Background(), &input)
		bv, be := second(context.Background(), &input)
		if a != bv || (ae == nil) != (be == nil) || len(input.Numbers) != len(before) {
			t.Fatal("mapping outcome changed")
		}
		for i := range before {
			if input.Numbers[i] != before[i] {
				t.Fatal("mapping mutated input")
			}
		}
		if ae != nil {
			var runtimeErr *cel.RuntimeError
			if a || !errors.As(ae, &runtimeErr) || ae.Error() != be.Error() || errors.Is(ae, cel.ErrCostLimit) != errors.Is(be, cel.ErrCostLimit) {
				t.Fatal("mapping failure became a match or lost its class")
			}
		}
		if source == "has(order.Child) && order.Child.Score >= 0" && (ae != nil || a != (len(data) > 0)) {
			t.Fatal("presence disagrees with Go oracle")
		}
		if (source == "order.Lookup['missing'].Score == 0" || source == "dyn(order.Nested).Missing == 1") && (a || ae == nil) {
			t.Fatal("missing data became a silent false")
		}
	})
}
