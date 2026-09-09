package cel_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"regexp/syntax"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/interpreter"
	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
)

type price struct {
	VIP       bool
	Total     int64 `json:"total_amount"`
	Country   string
	Items     []int64
	Discount  int
	AppliedBy rulite.RuleID
	secret    string
}

func compiler[T any](t testing.TB, options ...cel.Option) *cel.Compiler[T] {
	t.Helper()
	c, err := cel.NewCompiler[T]("input", options...)
	if err != nil {
		t.Fatalf("compiler: %v: %v", err, errors.Unwrap(err))
	}
	return c
}

func condition[T any](t testing.TB, c *cel.Compiler[T], source string) rulite.Condition[T] {
	t.Helper()
	f, err := c.Compile(source)
	if err != nil {
		t.Fatalf("compile: %v: %v", err, errors.Unwrap(err))
	}
	return f
}

func TestCompileStages(t *testing.T) {
	c := compiler[price](t)
	for _, tc := range []struct{ name, source, stage string }{
		{"syntax", "(", "parse"}, {"empty", "", "parse"},
		{"variable", "missing.VIP", "check"}, {"field", "input.Missing > 0", "check"},
		{"type", "input.Total + 'a' == 1", "check"}, {"tag", "input.total_amount > 0", "check"},
		{"private", "input.secret == ''", "check"}, {"method", "input.String() == ''", "check"},
		{"integer", "input.Total", "output"}, {"dynamic", "dyn(input.VIP)", "output"},
		{"program", "input.Country.matches('[')", "program"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := c.Compile(tc.source)
			var ce *cel.CompileError
			if f != nil || !errors.As(err, &ce) || ce.Stage() != tc.stage || ce.Unwrap() == nil {
				t.Fatalf("compile = %v; want %s", err, tc.stage)
			}
			if ce.ExpressionID() != fmt.Sprintf("%x", sha256.Sum256([]byte(tc.source))) {
				t.Fatal("source identity changed")
			}
			if !errors.Is(err, ce.Unwrap()) {
				t.Fatal("compile cause lost")
			}
			if tc.stage == "output" && !errors.Is(err, cel.ErrNonBool) {
				t.Fatal("bool check lost")
			}
			if tc.stage == "program" {
				var regexErr *syntax.Error
				if !errors.As(err, &regexErr) {
					t.Fatal("program cause type lost")
				}
			}
		})
	}
	for _, source := range []string{"true", "false", "input.VIP && input.Total >= 100"} {
		f := condition(t, c, source)
		for _, vip := range []bool{false, true} {
			input := price{VIP: vip, Total: 100}
			got, err := f(context.Background(), &input)
			want := source == "true" || source != "false" && vip
			if err != nil || got != want {
				t.Fatalf("got %v, %v; want %v", got, err, want)
			}
		}
	}
}

func TestFieldRenameFailsBeforeEvaluation(t *testing.T) {
	type renamed struct{ TotalAmount int64 }
	condition(t, compiler[price](t), "input.Total > 0")
	if f, err := compiler[renamed](t).Compile("input.Total > 0"); f != nil || err == nil {
		t.Fatal("old field survived rename")
	}
	condition(t, compiler[renamed](t), "input.TotalAmount > 0")
}

func TestCompilerConfigurationOwnership(t *testing.T) {
	options := []cel.Option{cel.WithCostLimit(10)}
	low := compiler[price](t, options...)
	options[0] = cel.WithCostLimit(10000)
	high := compiler[price](t, options...)
	clear(options)
	input := price{Items: make([]int64, 100)}
	for _, c := range []*cel.Compiler[price]{low, high, low} {
		f := condition(t, c, "input.Items.all(x, x == 0)")
		got, err := f(context.Background(), &input)
		if c == low {
			var cost interpreter.EvalCancelledError
			if got || !errors.Is(err, cel.ErrCostLimit) || !errors.As(err, &cost) {
				t.Fatal("cost setting or original cause lost")
			}
		} else if !got || err != nil {
			t.Fatalf("independent compiler failed: %v", err)
		}
	}
	for _, name := range []string{"", "a.b", "a-b", "true", "false", "null", "in", "for", strings.Repeat("x", 65)} {
		if c, err := cel.NewCompiler[price](name); c != nil || err == nil {
			t.Errorf("invalid variable accepted: %q", name)
		}
	}
	if c, err := cel.NewCompiler[price]("input", cel.WithCostLimit(0), cel.WithCostLimit(10)); c != nil || err == nil {
		t.Fatal("invalid option repaired")
	}
	for _, c := range []*cel.Compiler[price]{nil, {}} {
		if f, err := c.Compile("true"); f != nil || !errors.Is(err, cel.ErrInvalidCompiler) {
			t.Fatal("zero compiler accepted")
		}
	}
}

func TestCompileAndInputBudgets(t *testing.T) {
	c := compiler[price](t)
	for _, source := range []string{
		strings.Repeat(" ", 4097), strings.Repeat("(", 100) + "true" + strings.Repeat(")", 100),
		"input.Items.all(x, input.Items.all(y, input.Items.all(z, x == y && y == z)))",
		"input.Country.matches(input.Country)", "input.Country.matches('a{1000}')",
		"input.Country.matches('" + strings.Repeat("a", 129) + "')",
	} {
		if f, err := c.Compile(source); f != nil || err == nil {
			t.Fatal("compile bound bypassed")
		}
	}
	for _, source := range []string{"input.Country.matches('a{2}')", "input.Country.matches('a+')"} {
		if ok, err := condition(t, c, source)(context.Background(), &price{Country: "aa"}); !ok || err != nil {
			t.Fatal("bounded regex rejected", err)
		}
	}
	constant := condition(t, c, "true")
	for _, tc := range []struct {
		input price
		want  bool
	}{
		{price{Country: strings.Repeat("a", 65536)}, true}, {price{Country: strings.Repeat("a", 65537)}, false},
		{price{Items: make([]int64, 4096)}, true}, {price{Items: make([]int64, 4097)}, false},
		{price{Country: strings.Repeat("a", 65536), AppliedBy: "writer"}, false},
	} {
		ok, err := constant(context.Background(), &tc.input)
		if ok != tc.want || (err == nil) != tc.want || err != nil && !errors.Is(err, cel.ErrInputLimit) {
			t.Fatal("input bound changed")
		}
	}
}

func TestRuntimeErrorsAndPrivacy(t *testing.T) {
	c := compiler[price](t)
	for _, source := range []string{"1 / input.Total > 0", "input.Items[1] == 0", "int(input.Country) > 0"} {
		f := condition(t, c, source)
		input := price{Country: "private-account-value", secret: "private-state"}
		matched, err := f(context.Background(), &input)
		var runtimeErr *cel.RuntimeError
		var upstream *types.Err
		if matched || !errors.As(err, &runtimeErr) || !errors.As(err, &upstream) {
			t.Fatalf("runtime failure lost: %v", err)
		}
		if strings.Contains(err.Error(), input.Country) || strings.Contains(err.Error(), source) || strings.Contains(err.Error(), input.secret) {
			t.Fatal("default error disclosed input or source")
		}
		engine, buildErr := rulite.NewEngine(rulite.NewRule[price]("pricing/check").When(f).Then(func(context.Context, *price) error { t.Error("failed condition fired"); return nil }))
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		result, fireErr := engine.Fire(context.Background(), &input, rulite.WithTrace())
		if strings.Contains(result.Explain().String(), input.Country) || strings.Contains(fireErr.Error(), input.Country) {
			t.Fatal("root formatting disclosed input")
		}
	}
}

// Cancellation is triggered synchronously when CEL attaches its interrupt channel,
// after the adapter's initial context check. The underlying context owns Cause.
type cancelOnDone struct {
	context.Context
	cancel context.CancelCauseFunc
	cause  error
}

func (c cancelOnDone) Done() <-chan struct{} { c.cancel(c.cause); return c.Context.Done() }

func TestContextBoundaries(t *testing.T) {
	c := compiler[price](t)
	constant := condition(t, c, "true")
	cause := errors.New("request withdrawn")
	canceled, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	expired, stop := context.WithDeadlineCause(context.Background(), time.Unix(0, 0), cause)
	defer stop()
	for _, ctx := range []context.Context{canceled, expired} {
		if matched, err := constant(ctx, &price{}); matched || !errors.Is(err, ctx.Err()) || !errors.Is(err, cause) {
			t.Fatal("initial context cause lost")
		}
	}
	if _, err := constant(nil, &price{}); !errors.Is(err, rulite.ErrNilContext) {
		t.Fatal("nil context accepted")
	}
	if _, err := constant(context.Background(), nil); !errors.Is(err, rulite.ErrNilInput) {
		t.Fatal("nil input accepted")
	}
	for _, source := range []string{"true", "input.Items.all(x, x == 0)"} {
		base, cancel := context.WithCancelCause(context.Background())
		ctx := cancelOnDone{base, cancel, cause}
		matched, err := condition(t, c, source)(ctx, &price{Items: make([]int64, 100)})
		cancel(nil)
		if matched || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatal("post-evaluation context cause lost")
		}
		if source != "true" && !errors.Is(err, interpreter.InterruptError{}) {
			t.Fatal("comprehension did not cooperatively interrupt")
		}
	}
}

func TestNativeMapping(t *testing.T) {
	type amount int64
	type label string
	type input struct {
		B         bool
		S         string
		I         int
		I8        int8
		I16       int16
		I32       int32
		I64       int64
		U         uint
		U8        uint8
		U16       uint16
		U32       uint32
		U64       uint64
		F32       float32
		F64       float64
		Amount    amount
		Label     label
		AppliedBy rulite.RuleID
		Ints      []int64
		Strings   []string
		Bools     []bool
		Bytes     []byte
		Amounts   []amount
	}
	value := input{true, "ok", -1, -1, -1, -1, -1, 1, 1, 1, 1, 1, 1.5, 1.5, 1250, "ok", "pricing/vip", []int64{1}, []string{"ok"}, []bool{true}, []byte("ok"), []amount{1250}}
	before := value
	c := compiler[input](t)
	for _, source := range []string{
		"input.B && input.S == 'ok'", "input.I == -1 && input.I8 == -1 && input.I16 == -1 && input.I32 == -1 && input.I64 == -1",
		"input.U == 1u && input.U8 == 1u && input.U16 == 1u && input.U32 == 1u && input.U64 == 1u",
		"input.F32 == 1.5 && input.F64 == 1.5", "input.Amount + 1 == 1251 && input.Label == 'ok' && input.AppliedBy == 'pricing/vip'",
		"input.Ints == [1] && input.Strings == ['ok'] && input.Bools == [true] && input.Bytes == b'ok' && input.Amounts[0] == 1250",
	} {
		if got, err := condition(t, c, source)(context.Background(), &value); !got || err != nil {
			t.Fatalf("native mapping failed: %v: %v", err, errors.Unwrap(err))
		}
	}
	if !reflect.DeepEqual(value, before) {
		t.Fatal("condition mutated input")
	}
	zero := input{}
	if got, err := condition(t, c, "!has(input.I) && !has(input.Ints) && size(input.Ints) == 0")(context.Background(), &zero); !got || err != nil {
		t.Fatal("zero presence changed")
	}
	zero.Ints = []int64{}
	if got, err := condition(t, c, "has(input.Ints) && size(input.Ints) == 0")(context.Background(), &zero); !got || err != nil {
		t.Fatal("empty presence changed")
	}
}

type scalarList[V any] struct{ Values []V }

func checkScalarList[V any](t *testing.T, value V, literal string) {
	t.Helper()
	c := compiler[scalarList[V]](t)
	input := scalarList[V]{Values: []V{value}}
	f := condition(t, c, "input.Values[0] == "+literal)
	if ok, err := f(context.Background(), &input); !ok || err != nil {
		t.Fatalf("scalar list mapping: %v: %v", err, errors.Unwrap(err))
	}
}

func TestScalarListMapping(t *testing.T) {
	checkScalarList(t, true, "true")
	checkScalarList(t, "ok", "'ok'")
	checkScalarList(t, int(1), "1")
	checkScalarList(t, int8(1), "1")
	checkScalarList(t, int16(1), "1")
	checkScalarList(t, int32(1), "1")
	checkScalarList(t, int64(1), "1")
	checkScalarList(t, uint(1), "1u")
	// []uint8 is bytes, rather than a CEL list, and is tested by TestNativeMapping.
	checkScalarList(t, uint16(1), "1u")
	checkScalarList(t, uint32(1), "1u")
	checkScalarList(t, uint64(1), "1u")
	checkScalarList(t, float32(1.5), "1.5")
	checkScalarList(t, float64(1.5), "1.5")
}
