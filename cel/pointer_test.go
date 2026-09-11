package cel_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite/cel"
)

type pointerLabel string
type pointerFlag bool
type pointerInt8 int8
type pointerUint16 uint16

type scalarPointers struct {
	Int8    *int8
	Int16   *int16
	Uint8   *uint8
	Uint16  *uint16
	NamedI  *pointerInt8
	NamedU  *pointerUint16
	Label   *pointerLabel
	Flag    *pointerFlag
	Float32 *float32
	Float64 *float64
	At      *time.Time
	Delay   *time.Duration
	Labels  []*pointerLabel
	Flags   map[string]*pointerFlag
	Nested  []map[string]*int8
}

func TestScalarPointerConstruction(t *testing.T) {
	c := compiler[scalarPointers](t)
	for _, tc := range []struct {
		field string
		value string
	}{
		{"Int8", "0"}, {"Int8", "-128"}, {"Int8", "127"},
		{"Int16", "-32768"}, {"Int16", "32767"},
		{"Uint8", "0u"}, {"Uint8", "255u"}, {"Uint16", "65535u"},
		{"NamedI", "-128"}, {"NamedI", "127"}, {"NamedU", "65535u"},
		{"Label", "''"}, {"Label", "'ready'"}, {"Flag", "false"}, {"Flag", "true"},
		{"Float32", "1.5"}, {"Float64", "-2.5"},
		{"At", "timestamp('2026-09-11T00:00:00Z')"}, {"Delay", "duration('3s')"},
	} {
		t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
			literal := fmt.Sprintf("cel_test.scalarPointers{%s: %s}", tc.field, tc.value)
			checkTrue(t, c, &scalarPointers{}, fmt.Sprintf("has(%s.%s) && %s.%s == %s", literal, tc.field, literal, tc.field, tc.value))
		})
	}
	checkTrue(t, c, &scalarPointers{},
		"cel_test.scalarPointers{Labels: ['ready', '']}.Labels == ['ready', '']",
		"cel_test.scalarPointers{Flags: {'yes': true, 'no': false}}.Flags == {'yes': true, 'no': false}",
		"cel_test.scalarPointers{Nested: [{'min': -128, 'max': 127}]}.Nested == [{'min': -128, 'max': 127}]",
		"!has(cel_test.scalarPointers{}.Int8) && !has(cel_test.scalarPointers{}.Flag)")
	// Reusing native collections must preserve nil pointer elements.
	input := scalarPointers{Labels: []*pointerLabel{nil}, Flags: map[string]*pointerFlag{"nil": nil}}
	checkTrue(t, c, &input,
		"dyn(cel_test.scalarPointers{Labels: input.Labels}.Labels[0]) == null",
		"dyn(cel_test.scalarPointers{Flags: input.Flags}.Flags['nil']) == null")
}

func TestScalarPointerOverflow(t *testing.T) {
	c := compiler[scalarPointers](t)
	for _, tc := range []struct {
		field string
		value string
	}{
		{"Int8", "-129"}, {"Int8", "128"}, {"Int16", "-32769"}, {"Int16", "32768"},
		{"Uint8", "256u"}, {"Uint16", "65536u"},
		{"NamedI", "128"}, {"NamedU", "65536u"}, {"Nested", "[{'overflow': 128}]"},
	} {
		t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
			source := fmt.Sprintf("cel_test.scalarPointers{%s: %s} == input", tc.field, tc.value)
			ok, err := condition(t, c, source)(context.Background(), &scalarPointers{})
			var runtimeErr *cel.RuntimeError
			if ok || !errors.As(err, &runtimeErr) {
				t.Fatalf("overflow lost RuntimeError: matched=%t, error=%v", ok, err)
			}
		})
	}
}

func TestScalarPointerNullConstruction(t *testing.T) {
	c := compiler[scalarPointers](t)
	label, flag := pointerLabel(""), pointerFlag(false)
	input := scalarPointers{
		Labels: []*pointerLabel{nil, &label},
		Flags:  map[string]*pointerFlag{"nil": nil, "zero": &flag},
		Nested: []map[string]*int8{{"nil": nil}},
	}
	checkTrue(t, c, &input,
		"!has(cel_test.scalarPointers{Label: dyn(null)}.Label)",
		"cel_test.scalarPointers{Label: dyn(null)} != cel_test.scalarPointers{Label: ''}",
		"cel_test.scalarPointers{Labels: input.Labels.map(x, x)}.Labels == input.Labels",
		"cel_test.scalarPointers{Labels: [dyn(null), '']}.Labels == input.Labels",
		"cel_test.scalarPointers{Flags: {'nil': dyn(null), 'zero': false}}.Flags == input.Flags",
		"cel_test.scalarPointers{Nested: [{'nil': dyn(null)}]}.Nested == input.Nested")

	native := compiler[nativeMapping](t)
	objects := nativeMapping{Children: []*address{nil, {}}, Lookup: map[string]*address{"nil": nil}}
	checkTrue(t, native, &objects,
		"!has(cel_test.nativeMapping{Child: null}.Child)",
		"cel_test.nativeMapping{Child: null} != cel_test.nativeMapping{Child: cel_test.address{}}",
		"cel_test.nativeMapping{Children: input.Children.map(x, x)}.Children == input.Children",
		"cel_test.nativeMapping{Children: [null, cel_test.address{}]}.Children == input.Children",
		"cel_test.nativeMapping{Lookup: {'nil': null}}.Lookup == input.Lookup")
	for _, source := range []string{
		"cel_test.nativeMapping{Nested: dyn(null)} == input",
		"cel_test.nativeMapping{Numbers: [dyn(null)]} == input",
	} {
		matched, err := condition(t, native, source)(context.Background(), &objects)
		var runtimeErr *cel.RuntimeError
		if matched || !errors.As(err, &runtimeErr) {
			t.Fatalf("null assigned to a value field: matched=%t, error=%v", matched, err)
		}
	}
}
