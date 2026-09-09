package cel_test

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite/cel"
)

func builder[T any](t testing.TB, options ...cel.Option) *cel.Builder[T] {
	t.Helper()
	b, err := cel.NewBuilder[T](options...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func built[T any](t testing.TB, b *cel.Builder[T]) *cel.Compiler[T] {
	t.Helper()
	c, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v: %v", err, errors.Unwrap(err))
	}
	return c
}

func checkTrue[T any](t testing.TB, c *cel.Compiler[T], input *T, sources ...string) {
	t.Helper()
	for _, source := range sources {
		got, err := condition(t, c, source)(context.Background(), input)
		if !got || err != nil {
			t.Fatalf("%s = %v, %v: %v", source, got, err, errors.Unwrap(err))
		}
	}
}

func checkCompileFailure[T any](t testing.TB, c *cel.Compiler[T], sources ...string) {
	t.Helper()
	for _, source := range sources {
		f, err := c.Compile(source)
		var ce *cel.CompileError
		if f != nil || !errors.As(err, &ce) {
			t.Fatalf("expected compile failure: %s", source)
		}
	}
}

func TestExplicitJSONNames(t *testing.T) {
	type tagged struct {
		Total   int64  `json:"total_amount,string"`
		Keep    string `json:",omitempty"`
		Hidden  string `json:"-"`
		private int64
	}
	input := tagged{Total: 12, Keep: "ok", Hidden: "visible"}
	checkTrue(t, compiler[tagged](t), &input, "input.Total == 12 && input.Keep == 'ok' && input.Hidden == 'visible'")
	c := compiler[tagged](t, cel.WithJSONFieldNames())
	checkTrue(t, c, &input, "input.total_amount == 12 && input.Keep == 'ok'", "dyn(input).total_amount == 12")
	checkCompileFailure(t, c, "input.Total > 0", "input.Hidden == ''", "input.private == 0")
	type conflict struct {
		First  int `json:"Second"`
		Second int
	}
	for range 2 {
		if c, err := cel.NewCompiler[conflict]("input", cel.WithJSONFieldNames()); c != nil || err == nil {
			t.Fatal("mapping collision accepted")
		}
	}
	type hiddenUnsupported struct {
		Hidden chan int `json:"-"`
		Value  int
	}
	if c, err := cel.NewCompiler[hiddenUnsupported]("input"); c != nil || err == nil {
		t.Fatal("default JSON hiding enabled")
	}
	checkTrue(t, compiler[hiddenUnsupported](t, cel.WithJSONFieldNames()), &hiddenUnsupported{Value: 1}, "input.Value == 1")
	type invalidName struct {
		Total int `json:"total.amount"`
	}
	if c, err := cel.NewCompiler[invalidName]("input", cel.WithJSONFieldNames()); c != nil || err == nil {
		t.Fatal("invalid field name accepted")
	}
}

type address struct {
	Score int64
	At    time.Time
}
type namedNumbers []int64
type namedMapping map[string]int64
type nativeMapping struct {
	Child       *address
	Nested      address
	Count       *int64
	At          time.Time
	AtPointer   *time.Time
	Delay       time.Duration
	Children    []*address
	Lookup      map[string]*address
	Numbers     namedNumbers
	Mapping     namedMapping
	NestedLists [][]int64
	Flags       map[bool]string
	Signed      map[int64]string
	Unsigned    map[uint64]string
	Times       []*time.Time
	Delays      []*time.Duration
}

func TestNestedNativePresenceAndTime(t *testing.T) {
	c := compiler[nativeMapping](t)
	zero := nativeMapping{}
	checkTrue(t, c, &zero,
		"cel_test.address{}.At == timestamp('0001-01-01T00:00:00Z')",
		"dyn(cel_test.address{}).At == input.At",
		"!has(input.Child) && input.Child.Score == 0 && !has(input.Count) && input.Count == 0",
		"!has(input.At) && input.At == timestamp('0001-01-01T00:00:00Z')",
		"dyn(input).At == timestamp('0001-01-01T00:00:00Z') && dyn(input).Child.At == input.At",
		"input.Nested.At == input.At && input.AtPointer == input.At && !has(input.AtPointer)",
		"input.Delay == duration('0s') && input.Numbers.size() == 0 && input.Mapping.size() == 0",
		"!has(input.Numbers) && !has(input.Mapping) && !input.?Child.hasValue()")
	count, at := int64(0), time.Time{}
	delay := 3 * time.Second
	input := nativeMapping{Child: &address{}, Count: &count, AtPointer: &at, Delay: 3 * time.Second, Times: []*time.Time{&at, nil}, Delays: []*time.Duration{&delay},
		Children: []*address{{Score: 7}}, Lookup: map[string]*address{"home": {Score: 7}},
		Numbers: namedNumbers{}, Mapping: namedMapping{}, NestedLists: [][]int64{{1}},
		Flags: map[bool]string{true: "yes"}, Signed: map[int64]string{-1: "yes"}, Unsigned: map[uint64]string{1: "yes"}}
	checkTrue(t, c, &input,
		"input.Times[0] == input.At && input.Times[1] == null && input.Delays[0] == input.Delay",
		"has(input.Count) && input.Count == 0 && has(input.Child) && input.?Child.hasValue()",
		"has(input.AtPointer) && input.AtPointer == timestamp('0001-01-01T00:00:00Z') && input.Delay == duration('3s')",
		"input.Children[0].Score == 7 && input.Lookup['home'].Score == 7 && input.Children[0] == input.Lookup['home']",
		"input.Children[0].At == input.At && dyn(input.Lookup['home']).At == input.At",
		"has(input.Numbers) && has(input.Mapping) && size(input.Numbers) == 0 && size(input.Mapping) == 0",
		"input.NestedLists[0][0] == 1 && input.Flags[true] == 'yes' && input.Signed[-1] == 'yes' && input.Unsigned[1u] == 'yes'")
	input.At = time.Date(2026, 9, 9, 8, 0, 0, 0, time.FixedZone("offset", 3600))
	checkTrue(t, c, &input, "input.At == timestamp('2026-09-09T07:00:00Z') && has(input.At)")
	checkCompileFailure(t, c, "input.Lookup['home'].Missing > 0", "input.At.String() == ''", "input.Child.score == 0")
}

func TestScalarProjectionAndExposedEquality(t *testing.T) {
	type money int64
	type label string
	b := builder[price](t)
	if err := b.Bind("amount", func(_ context.Context, p *price) (money, error) { return money(p.Total), nil }); err != nil {
		t.Fatal(err)
	}
	zero := time.Time{}
	if err := b.Bind("placed", func(context.Context, *price) (*time.Time, error) { return &zero, nil }); err != nil {
		t.Fatal(err)
	}
	if err := b.Function("formatAmount", func(value money) (label, error) {
		if value == 1250 {
			return "exact", nil
		}
		return "different", nil
	}); err != nil {
		t.Fatal(err)
	}
	checkTrue(t, built(t, b), &price{Total: 1250}, "amount == 1250 && formatAmount(amount) == 'exact' && placed == timestamp('0001-01-01T00:00:00Z')")
	type pair struct {
		Left  price
		Right price
	}
	input := pair{Left: price{Total: 1, secret: "left"}, Right: price{Total: 1, secret: "right"}}
	c := compiler[pair](t)
	checkTrue(t, c, &input, "input.Left == input.Right")
	input.Right.Items = []int64{}
	checkTrue(t, c, &input, "input.Left != input.Right")
	type recursivePair struct {
		Left  linked
		Right linked
	}
	checkTrue(t, compiler[recursivePair](t), &recursivePair{}, "input.Left == input.Right")
	type decimal struct{ rational *big.Rat }
	type decimalInput struct{ Amount decimal }
	checkCompileFailure(t, compiler[decimalInput](t), "input.Amount + 1 == 2", "input.Amount.rational == 0")
}

type decision struct {
	Order  price
	User   address
	Amount *big.Rat
}

func exactMinorUnits(amount *big.Rat) (int64, error) {
	if amount == nil {
		return 0, errors.New("amount is required")
	}
	minor := new(big.Rat).Mul(amount, big.NewRat(100, 1))
	if !minor.IsInt() || !minor.Num().IsInt64() {
		return 0, errors.New("amount requires exact int64 minor units")
	}
	return minor.Num().Int64(), nil
}

func TestTypedBindingsFreezeAndExactProjection(t *testing.T) {
	options := []cel.Option{cel.WithCostLimit(1000)}
	b := builder[decision](t, options...)
	clear(options)
	calls := []string{}
	if err := b.Bind("order", func(_ context.Context, input *decision) (*price, error) {
		calls = append(calls, "order")
		return &input.Order, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind("user", func(_ context.Context, input *decision) (address, error) {
		calls = append(calls, "user")
		return input.User, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind("minor", func(_ context.Context, input *decision) (int64, error) {
		calls = append(calls, "minor")
		return exactMinorUnits(input.Amount)
	}); err != nil {
		t.Fatal(err)
	}
	c := built(t, b)
	if len(calls) != 0 {
		t.Fatal("construction evaluated a projector")
	}
	if err := b.Bind("later", func(context.Context, *decision) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	checkCompileFailure(t, c, "later", "order.total_amount > 0", "user.Renamed > 0")
	checkTrue(t, built(t, b), &decision{Amount: big.NewRat(1, 1)}, "later")
	calls = nil
	input := decision{Order: price{VIP: true}, User: address{Score: 7}, Amount: big.NewRat(25, 2)}
	checkTrue(t, c, &input, "order.VIP && user.Score == 7 && minor == 1250 && user.At == timestamp('0001-01-01T00:00:00Z')")
	if !reflect.DeepEqual(calls, []string{"order", "user", "minor"}) {
		t.Fatal("projection order changed")
	}
	for _, amount := range []*big.Rat{big.NewRat(1, 1000), new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 80)), nil} {
		input.Amount = amount
		if ok, err := condition(t, c, "true")(context.Background(), &input); ok || err == nil {
			t.Fatal("inexact projection became a match")
		}
	}
}

func TestBindingRegistrationErrorsAreAtomic(t *testing.T) {
	b := builder[price](t)
	project := func(context.Context, *price) (bool, error) { return true, nil }
	for _, name := range []string{"", "a.b", "true", "null", "for", "in", "size", "has", "exists", "int", "__hidden"} {
		for range 2 {
			if err := b.Bind(name, project); err == nil {
				t.Fatalf("reserved name accepted: %s", name)
			}
		}
	}
	if err := b.Bind[bool]("nilProject", nil); err == nil {
		t.Fatal("nil projector accepted")
	}
	if err := b.Bind("valid", project); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind("valid", project); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	checkTrue(t, built(t, b), &price{}, "valid")
	for _, b := range []*cel.Builder[price]{nil, {}} {
		if _, err := b.Build(); !errors.Is(err, cel.ErrInvalidCompiler) {
			t.Fatal("invalid builder accepted")
		}
	}
	for _, source := range []string{"true", "order.VIP"} {
		nilBuilder := builder[price](t)
		if err := nilBuilder.Bind("order", func(context.Context, *price) (*price, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
		if ok, err := condition(t, built(t, nilBuilder), source)(context.Background(), &price{}); ok || !errors.Is(err, cel.ErrNilBinding) {
			t.Fatal("nil projection became a miss")
		}
	}
}

type linked struct {
	Next    *linked
	Text    string
	Items   []string
	Mapping map[string]string
}

func TestRecursiveInputBounds(t *testing.T) {
	c := compiler[linked](t)
	f := condition(t, c, "true")
	for _, input := range []*linked{
		{Next: &linked{Text: strings.Repeat("x", 65537)}},
		{Mapping: map[string]string{strings.Repeat("x", 65536): "x"}},
		{Items: make([]string, 4097)},
	} {
		if ok, err := f(context.Background(), input); ok || !errors.Is(err, cel.ErrInputLimit) {
			t.Fatal("nested input bound bypassed")
		}
	}
	cycle := &linked{}
	cycle.Next = cycle
	if ok, err := f(context.Background(), cycle); ok || !errors.Is(err, cel.ErrInputLimit) {
		t.Fatal("cyclic input was not bounded")
	}
	b := builder[linked](t)
	for _, name := range []string{"left", "right"} {
		if err := b.Bind(name, func(_ context.Context, input *linked) (string, error) { return input.Text, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := condition(t, built(t, b), "true")(context.Background(), &linked{Text: strings.Repeat("x", 32769)}); ok || !errors.Is(err, cel.ErrInputLimit) {
		t.Fatal("aggregate binding byte bound bypassed")
	}
}
