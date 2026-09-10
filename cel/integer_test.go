package cel_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/imbrooklyn/rulite/cel"
)

type nativeInt int
type nativeUint uint
type nativeInts []nativeInt
type nativeIntMap map[nativeInt][]nativeUint

type integerInput struct {
	Signed   int64
	Unsigned uint64
	Native   integerObject
}

type integerObject struct {
	Signed   int
	Unsigned uint
	Named    nativeInt
	Pointer  *int
	UPointer *uint
	Values   nativeInts
	Nested   []nativeIntMap
}

func TestIntegerFunctionBoundaries(t *testing.T) {
	t.Run("int", func(t *testing.T) { signedFunctionBounds[int](t, math.MinInt, math.MaxInt) })
	t.Run("named_int", func(t *testing.T) { signedFunctionBounds[nativeInt](t, math.MinInt, math.MaxInt) })
	t.Run("int8", func(t *testing.T) { signedFunctionBounds[int8](t, math.MinInt8, math.MaxInt8) })
	t.Run("int16", func(t *testing.T) { signedFunctionBounds[int16](t, math.MinInt16, math.MaxInt16) })
	t.Run("int32", func(t *testing.T) { signedFunctionBounds[int32](t, math.MinInt32, math.MaxInt32) })
	t.Run("int64", func(t *testing.T) { signedFunctionBounds[int64](t, math.MinInt64, math.MaxInt64) })
	t.Run("uint", func(t *testing.T) { unsignedFunctionBounds[uint](t, math.MaxUint) })
	t.Run("named_uint", func(t *testing.T) { unsignedFunctionBounds[nativeUint](t, math.MaxUint) })
	t.Run("uint8", func(t *testing.T) { unsignedFunctionBounds[uint8](t, math.MaxUint8) })
	t.Run("uint16", func(t *testing.T) { unsignedFunctionBounds[uint16](t, math.MaxUint16) })
	t.Run("uint32", func(t *testing.T) { unsignedFunctionBounds[uint32](t, math.MaxUint32) })
	t.Run("uint64", func(t *testing.T) { unsignedFunctionBounds[uint64](t, math.MaxUint64) })
}

func signedFunctionBounds[A ~int | ~int8 | ~int16 | ~int32 | ~int64](t *testing.T, min, max int64) {
	t.Helper()
	b := builder[integerInput](t)
	if err := b.Bind("amount", func(_ context.Context, p *integerInput) (int64, error) { return p.Signed, nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := b.Function("echo", func(value A) (int64, error) { calls++; return int64(value), nil }); err != nil {
		t.Fatal(err)
	}
	check := condition(t, built(t, b), "echo(amount) == amount")
	values := []int64{math.MinInt64, math.MinInt32 - 1, math.MinInt32, -1, 0, 1, math.MaxInt32, math.MaxInt32 + 1, math.MaxInt64, min, max}
	if min > math.MinInt64 {
		values = append(values, min-1, max+1)
	}
	for _, value := range values {
		calls = 0
		ok, err := check(context.Background(), &integerInput{Signed: value})
		assertIntegerConversion(t, value, ok, err, calls, value >= min && value <= max)
	}
}

func unsignedFunctionBounds[A ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](t *testing.T, max uint64) {
	t.Helper()
	b := builder[integerInput](t)
	if err := b.Bind("amount", func(_ context.Context, p *integerInput) (uint64, error) { return p.Unsigned, nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := b.Function("echo", func(value A) (uint64, error) { calls++; return uint64(value), nil }); err != nil {
		t.Fatal(err)
	}
	check := condition(t, built(t, b), "echo(amount) == amount")
	values := []uint64{0, 1, math.MaxUint32, math.MaxUint32 + 1, math.MaxInt64, math.MaxUint64, max}
	if max < math.MaxUint64 {
		values = append(values, max+1)
	}
	for _, value := range values {
		calls = 0
		ok, err := check(context.Background(), &integerInput{Unsigned: value})
		assertIntegerConversion(t, value, ok, err, calls, value <= max)
	}
}

func assertIntegerConversion(t *testing.T, value any, ok bool, err error, calls int, fits bool) {
	t.Helper()
	if fits {
		if !ok || err != nil || calls != 1 {
			t.Fatalf("valid %v: matched=%t, error=%v, cause=%v, calls=%d", value, ok, err, errors.Unwrap(err), calls)
		}
	} else {
		var runtimeErr *cel.RuntimeError
		if ok || !errors.As(err, &runtimeErr) || calls != 0 {
			t.Fatalf("overflow %v reached callback or lost RuntimeError: matched=%t, error=%v, calls=%d", value, ok, err, calls)
		}
	}
}

type integerMaps struct {
	Signed   int64
	Unsigned uint64
	Keys     map[int]string
	UKeys    map[uint]string
	Named    map[nativeInt]string
	UNamed   map[nativeUint]string
	Nested   []map[int]string
	Narrow   map[int32]string
}

func TestIntegerMapBoundaries(t *testing.T) {
	c := compiler[integerMaps](t)
	for _, value := range []int64{math.MinInt64, math.MinInt32 - 1, math.MinInt32, -1, 0, math.MaxInt32, math.MaxInt32 + 1, math.MaxInt64} {
		if value < math.MinInt || value > math.MaxInt {
			continue
		}
		t.Run(strconv.FormatInt(value, 10), func(t *testing.T) {
			input := integerMaps{Signed: value, Keys: map[int]string{int(value): "yes"}, Named: map[nativeInt]string{nativeInt(value): "yes"}, Nested: []map[int]string{{int(value): "yes"}}}
			checkTrue(t, c, &input,
				"input.Keys[input.Signed] == 'yes'", fmt.Sprintf("input.Keys[%d] == 'yes'", value),
				"input.Signed in input.Keys", "input.Keys[?input.Signed].hasValue()",
				"dyn(input.Keys)[input.Signed] == 'yes'", "dyn(input).Keys[input.Signed] == 'yes'",
				"input.Named[input.Signed] == 'yes'", "input.Signed in input.Named",
				"input.Nested[0][input.Signed] == 'yes'",
				"input.Keys == {input.Signed: 'yes'}", "{input.Signed: 'yes'} == input.Keys", "input.Keys == input.Keys",
				"input.Keys.all(k, input.Keys[k] == 'yes')",
				"input.Keys != {input.Signed: 'other'}", "input.Keys != {}")
			if value >= 0 {
				checkTrue(t, c, &input, "dyn(uint(input.Signed)) in input.Keys")
			}
		})
	}
	for _, value := range []uint64{0, math.MaxUint32, math.MaxUint32 + 1, math.MaxInt64, math.MaxUint64} {
		if value > math.MaxUint {
			continue
		}
		input := integerMaps{Unsigned: value, UKeys: map[uint]string{uint(value): "yes"}, UNamed: map[nativeUint]string{nativeUint(value): "yes"}}
		checkTrue(t, c, &input,
			"input.UKeys[input.Unsigned] == 'yes'", fmt.Sprintf("input.UKeys[%du] == 'yes'", value),
			"input.Unsigned in input.UKeys", "input.UKeys[?input.Unsigned].hasValue()",
			"input.UNamed[input.Unsigned] == 'yes'", "input.Unsigned in input.UNamed",
			"input.UKeys == {input.Unsigned: 'yes'}", "{input.Unsigned: 'yes'} == input.UKeys")
		if value <= math.MaxInt64 {
			checkTrue(t, c, &input, "dyn(int(input.Unsigned)) in input.UKeys")
		}
	}
	// Missing and non-integral keys must not alias an existing native key.
	input := integerMaps{Keys: map[int]string{1: "yes"}, UKeys: map[uint]string{1: "yes"}, Narrow: map[int32]string{1: "yes"}}
	checkTrue(t, c, &input,
		"!(2 in input.Keys) && !input.Keys[?2].hasValue()", "!(2u in input.UKeys)",
		"dyn(1.0) in input.Keys && dyn(1.0) in input.UKeys",
		"!(dyn(1.5) in input.Keys) && !(dyn(1.5) in input.UKeys)",
		"!(dyn('1') in input.Keys) && !(dyn(-1) in input.UKeys)",
		"!(dyn(18446744073709551615u) in input.Keys)", "!(4294967297 in input.Narrow)")
	if strconv.IntSize == 32 {
		checkTrue(t, c, &input, "!(4294967297 in input.Keys)", "!(4294967297u in input.UKeys)")
	}
	if ok, err := condition(t, c, "input.Keys[2] == 'yes'")(context.Background(), &input); ok || err == nil {
		t.Fatal("missing key stopped reporting an error")
	}
}

func TestIntegerObjectConstruction(t *testing.T) {
	c := compiler[integerInput](t)
	for _, signed := range []int64{math.MinInt64, math.MinInt32 - 1, math.MinInt32, 0, math.MaxInt32, math.MaxInt32 + 1, math.MaxInt64} {
		for _, unsigned := range []uint64{0, math.MaxUint32, math.MaxUint32 + 1, math.MaxUint64} {
			input := integerInput{Signed: signed, Unsigned: unsigned}
			fitsSigned := signed >= math.MinInt && signed <= math.MaxInt
			fitsUnsigned := unsigned <= math.MaxUint
			for _, tc := range []struct {
				source string
				fits   bool
			}{
				{"cel_test.integerObject{Signed: input.Signed}.Signed == input.Signed", fitsSigned},
				{"cel_test.integerObject{Named: input.Signed}.Named == input.Signed", fitsSigned},
				{"cel_test.integerObject{Pointer: input.Signed}.Pointer == input.Signed", fitsSigned},
				{"cel_test.integerObject{Values: [input.Signed]}.Values[0] == input.Signed", fitsSigned},
				{"cel_test.integerObject{Unsigned: input.Unsigned}.Unsigned == input.Unsigned", fitsUnsigned},
				{"cel_test.integerObject{UPointer: input.Unsigned}.UPointer == input.Unsigned", fitsUnsigned},
				{"cel_test.integerObject{Nested: [{input.Signed: [input.Unsigned]}]}.Nested[0][input.Signed][0] == input.Unsigned", fitsSigned && fitsUnsigned},
			} {
				ok, err := condition(t, c, tc.source)(context.Background(), &input)
				if ok != tc.fits || (err == nil) != tc.fits {
					t.Fatalf("%s (%d, %d): matched=%t, error=%v, cause=%v, fits=%t", tc.source, signed, unsigned, ok, err, errors.Unwrap(err), tc.fits)
				}
			}
		}
	}
	checkTrue(t, c, &integerInput{},
		"cel_test.integerObject{Values: input.Native.Values, Nested: input.Native.Nested} == input.Native")
}

func FuzzIntegerConversions(f *testing.F) {
	for _, seed := range []struct {
		signed   int64
		unsigned uint64
	}{
		{0, 0}, {-1, 1}, {math.MinInt32, math.MaxUint32},
		{math.MinInt32 - 1, math.MaxUint32 + 1}, {math.MaxInt32 + 1, math.MaxUint32 + 1},
		{math.MinInt64, math.MaxUint64}, {math.MaxInt64, math.MaxUint64},
	} {
		f.Add(seed.signed, seed.unsigned, seed.signed, seed.unsigned)
	}
	c := compiler[integerMaps](f)
	signed := condition(f, c, "input.Signed in input.Keys")
	unsigned := condition(f, c, "input.Unsigned in input.UKeys")
	b := builder[integerMaps](f)
	if err := b.Bind("input", func(_ context.Context, p *integerMaps) (*integerMaps, error) { return p, nil }); err != nil {
		f.Fatal(err)
	}
	if err := b.Function("echoSigned", func(value int) (int64, error) { return int64(value), nil }); err != nil {
		f.Fatal(err)
	}
	if err := b.Function("echoUnsigned", func(value uint) (uint64, error) { return uint64(value), nil }); err != nil {
		f.Fatal(err)
	}
	convert := condition(f, built(f, b), "echoSigned(input.Signed) == input.Signed && echoUnsigned(input.Unsigned) == input.Unsigned")
	f.Fuzz(func(t *testing.T, signedKey int64, unsignedKey uint64, signedProbe int64, unsignedProbe uint64) {
		input := integerMaps{Signed: signedProbe, Unsigned: unsignedProbe,
			Keys: map[int]string{int(signedKey): "yes"}, UKeys: map[uint]string{uint(unsignedKey): "yes"}}
		gotSigned, errSigned := signed(context.Background(), &input)
		gotUnsigned, errUnsigned := unsigned(context.Background(), &input)
		if errSigned != nil || errUnsigned != nil || gotSigned != (int64(int(signedKey)) == signedProbe) || gotUnsigned != (uint64(uint(unsignedKey)) == unsignedProbe) {
			t.Fatal("CEL key membership differs from native integer keys")
		}
		fits := int64(int(signedProbe)) == signedProbe && uint64(uint(unsignedProbe)) == unsignedProbe
		ok, err := convert(context.Background(), &input)
		if ok != fits || (err == nil) != fits {
			t.Fatalf("native conversion differs from lossless Go round trip: %d, %d: %t, %v", signedProbe, unsignedProbe, ok, err)
		}
	})
}
