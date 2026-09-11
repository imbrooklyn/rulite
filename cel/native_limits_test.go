package cel_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite/cel"
)

type nativeResources struct {
	Values  []int64
	More    []int64
	Nested  [][]int64
	Mapping map[string][]int64
	Text    string
	Label   *string
	Blob    []byte
	Large   *largeNative
	Padded  []paddedNative
}

type largeNative struct {
	Value  int64
	hidden [2 << 20]byte
}

type paddedNative struct {
	Value  int64
	hidden [1024]byte
}

func doubledList(depth int, tail string) string {
	return "optional.of([1])" + strings.Repeat(".optMap(v, v + v)", depth) +
		".optMap(v, " + tail + ").orValue(false)"
}

func TestNativeConstructionLimits(t *testing.T) {
	c := compiler[nativeResources](t, cel.WithCostLimit(1000))
	for _, tc := range []struct {
		name, source string
	}{
		{"list", doubledList(30, "cel_test.nativeResources{Values: v}.Values.size() > 0")},
		{"nested list", doubledList(30, "cel_test.nativeResources{Nested: [v] + [[]]}.Nested.size() > 0")},
		{"map value", doubledList(30, "cel_test.nativeResources{Mapping: {'values': v}}.Mapping.size() > 0")},
		{"optional payload", doubledList(30, "cel_test.nativeResources{Values: dyn(optional.of(v))}.Values.size() > 0")},
		{"fields share item budget", doubledList(12, "cel_test.nativeResources{Values: v, More: v}.Values.size() > 0")},
		{"nested item budget", doubledList(11, "cel_test.nativeResources{Nested: [v, v]}.Nested.size() > 0")},
		{"large struct", "cel_test.largeNative{}.Value == 0"},
		{"large elements", "optional.of([cel_test.paddedNative{}])" + strings.Repeat(".optMap(v, v + v)", 11) +
			".optMap(v, cel_test.nativeResources{Padded: v}.Padded.size() > 0).orValue(false)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := condition(t, c, tc.source)(context.Background(), &nativeResources{})
			var runtimeErr *cel.RuntimeError
			if matched || !errors.As(err, &runtimeErr) || !errors.Is(err, cel.ErrNativeLimit) {
				t.Fatalf("native limit lost: matched=%t, error=%v: %v", matched, err, errors.Unwrap(err))
			}
		})
	}
	checkTrue(t, c, &nativeResources{},
		doubledList(12, "cel_test.nativeResources{Values: v}.Values.size() == 4096"),
		doubledList(11, "cel_test.nativeResources{Values: v, More: v}.More.size() == 2048"),
		"!has(cel_test.nativeResources{Values: input.Values}.Values)",
		"!has(cel_test.nativeResources{Mapping: input.Mapping}.Mapping)")
	checkTrue(t, c, &nativeResources{Values: []int64{}, Mapping: map[string][]int64{}},
		"has(cel_test.nativeResources{Values: input.Values}.Values)",
		"has(cel_test.nativeResources{Mapping: input.Mapping}.Mapping)")
}

func TestNativeConstructionByteLimits(t *testing.T) {
	c := compiler[nativeResources](t, cel.WithCostLimit(100000))
	input := nativeResources{Text: strings.Repeat("x", 32768)}
	checkTrue(t, c, &input,
		"cel_test.nativeResources{Text: input.Text + input.Text}.Text.size() == 65536",
		"cel_test.nativeResources{Label: input.Text + input.Text}.Label.size() == 65536",
		"cel_test.nativeResources{Blob: bytes(input.Text + input.Text)}.Blob.size() == 65536")
	for _, fields := range []string{
		"Text: input.Text + input.Text + 'x'",
		"Label: input.Text + input.Text + 'x'",
		"Blob: bytes(input.Text + input.Text + 'x')",
		"Text: input.Text + 'x', Label: input.Text",
	} {
		source := "cel_test.nativeResources{" + fields + "} == input"
		matched, err := condition(t, c, source)(context.Background(), &input)
		if matched || !errors.Is(err, cel.ErrNativeLimit) {
			t.Fatalf("byte limit lost for %s: matched=%t, error=%v: %v", fields, matched, err, errors.Unwrap(err))
		}
	}
}

func TestNativeConstructionRejectsUntypedObjects(t *testing.T) {
	c := compiler[nativeMapping](t)
	for _, source := range []string{
		"cel_test.nativeMapping{Child: dyn({'score': 1})}.Child.Score == 1",
		"cel_test.nativeMapping{Nested: dyn({'score': 1})}.Nested.Score == 1",
	} {
		matched, err := condition(t, c, source)(context.Background(), &nativeMapping{})
		var runtimeErr *cel.RuntimeError
		if matched || !errors.As(err, &runtimeErr) {
			t.Fatalf("unbounded aggregate conversion accepted: matched=%t, error=%v", matched, err)
		}
	}
	checkTrue(t, c, &nativeMapping{}, "cel_test.nativeMapping{Child: dyn(cel_test.address{Score: 1})}.Child.Score == 1")
}

func FuzzNativeConstruction(f *testing.F) {
	for _, depth := range []uint8{0, 1, 10, 11, 12, 13, 30} {
		for mode := range uint8(4) {
			f.Add(depth, mode)
		}
	}
	c := compiler[nativeResources](f, cel.WithCostLimit(1000))
	f.Fuzz(func(t *testing.T, depth, mode uint8) {
		depth %= 31
		mode %= 4
		field, access, overhead := "Values: v", "Values", 0
		if mode == 1 {
			field, access, overhead = "Nested: [v]", "Nested[0]", 1
		} else if mode == 2 {
			field, access, overhead = "Mapping: {'v': v}", "Mapping['v']", 1
		} else if mode == 3 {
			field = "Values: dyn(optional.of(v))"
		}
		size := int64(1) << depth
		source := doubledList(int(depth), fmt.Sprintf("cel_test.nativeResources{%s}.%s.size() == %d", field, access, size))
		matched, err := condition(t, c, source)(context.Background(), &nativeResources{})
		if size+int64(overhead) <= 4096 {
			if !matched || err != nil {
				t.Fatalf("bounded construction failed: matched=%t, error=%v: %v", matched, err, errors.Unwrap(err))
			}
		} else if matched || !errors.Is(err, cel.ErrNativeLimit) {
			t.Fatalf("oversized construction accepted: matched=%t, error=%v: %v", matched, err, errors.Unwrap(err))
		}
	})
}
