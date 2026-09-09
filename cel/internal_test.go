package cel

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	celgo "cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/imbrooklyn/rulite"
)

type nativeFixture struct{ Total int64 }
type embeddedFixture struct{ nativeFixture }
type namedList []int64

func TestUnsupportedSchemas(t *testing.T) {
	for _, tp := range []reflect.Type{
		reflect.TypeFor[struct{ X int }](), reflect.TypeFor[embeddedFixture](),
	} {
		if _, err := newSchema(false).native(tp, 0); err == nil {
			t.Fatalf("unsupported schema accepted: %v", tp)
		}
	}
	assertRejectedSchema[int](t)
	assertRejectedSchema[*nativeFixture](t)
	assertRejectedSchema[time.Time](t)
	assertRejectedSchema[structWithField[**int64]](t)
	assertRejectedSchema[structWithField[any]](t)
	assertRejectedSchema[structWithField[chan int]](t)
	assertRejectedSchema[structWithField[func()]](t)
	assertRejectedSchema[structWithField[complex128]](t)
	assertRejectedSchema[structWithField[uintptr]](t)
	assertRejectedSchema[structWithField[[2]int]](t)
	assertRejectedSchema[structWithField[types.Int]](t)
}

type structWithField[V any] struct{ Value V }

func assertRejectedSchema[T any](t *testing.T) {
	t.Helper()
	c, err := NewCompiler[T]("input")
	var ce *CompileError
	if c != nil || !errors.As(err, &ce) || ce.Stage() != "environment" {
		t.Fatal("unsupported field accepted")
	}
}

func TestUnknownAndUpstreamCause(t *testing.T) {
	env, err := celgo.NewEnv(celgo.Variable("value", celgo.IntType))
	if err != nil {
		t.Fatal(err)
	}
	checked, issues := env.Compile("value > 0")
	if issues.Err() != nil {
		t.Fatal(issues.Err())
	}
	p, err := env.Program(checked, celgo.EvalOptions(celgo.OptPartialEval), celgo.CostLimit(100))
	if err != nil {
		t.Fatal(err)
	}
	partial, err := celgo.PartialVars(map[string]any{}, celgo.AttributePattern("value"))
	if err != nil {
		t.Fatal(err)
	}
	f := rulite.Condition[nativeFixture](func(ctx context.Context, _ *nativeFixture) (bool, error) {
		return evaluate(ctx, p, partial, "unknown-expression")
	})
	engine, err := rulite.NewEngine(rulite.NewRule[nativeFixture]("cel/unknown").When(f).Then(func(context.Context, *nativeFixture) error { t.Error("unknown fired"); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Fire(context.Background(), &nativeFixture{}, rulite.WithTrace())
	var failure rulite.Failure
	if !errors.Is(err, ErrUnknown) || !errors.As(err, &failure) || failure.Phase() != rulite.ConditionPhase || failure.RuleID() != "cel/unknown" || result.Counts().Matched != 0 {
		t.Fatal("unknown became a miss or lost canonical failure")
	}
	cause := errors.New("sensitive upstream detail")
	value := types.WrapErr(cause)
	for _, explicit := range []error{nil, cause} {
		ok, err := boolOutcome(value, explicit)
		if ok || !errors.Is(err, cause) {
			t.Fatal("upstream cause lost")
		}
	}
	for _, value := range []ref.Val{nil, types.Int(1)} {
		if ok, err := boolOutcome(value, nil); ok || err == nil {
			t.Fatal("unexpected value became a miss")
		}
	}
}

type privateError struct{ formatted *bool }

func (e privateError) Error() string { *e.formatted = true; return "sensitive-error-value" }

func TestErrorFormattingDoesNotInspectCause(t *testing.T) {
	formatted := false
	cause := privateError{&formatted}
	for _, err := range []error{&CompileError{"digest", "check", cause}, &RuntimeError{"digest", cause}} {
		if strings.Contains(err.Error(), "sensitive") || formatted || !errors.Is(err, cause) {
			t.Fatal("default formatting inspected cause")
		}
	}
	var ce *CompileError
	var re *RuntimeError
	if ce.Error() == "" || ce.Stage() != "" || ce.ExpressionID() != "" || ce.Unwrap() != nil || re.Error() == "" || re.ExpressionID() != "" || re.Unwrap() != nil {
		t.Fatal("nil error view is unsafe")
	}
}

func TestAggregateInputLimits(t *testing.T) {
	type input struct {
		Texts []string
		Data  []byte
		Other []int
	}
	tp := reflect.TypeFor[input]()
	schema := newSchema(false)
	_, err := schema.native(tp, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value input
		want  bool
	}{
		{input{Texts: []string{strings.Repeat("a", maxInputBytes)}}, true},
		{input{Texts: []string{strings.Repeat("a", maxInputBytes), "a"}}, false},
		{input{Data: make([]byte, maxInputBytes)}, true},
		{input{Data: make([]byte, maxInputBytes+1)}, false},
		{input{Texts: make([]string, maxListItems), Other: []int{1}}, false},
	} {
		budget := inputBudget{bytes: maxInputBytes, items: maxListItems, nodes: 65536, ctx: context.Background()}
		if (budget.native(reflect.ValueOf(tc.value), schema, 0) == nil) != tc.want {
			t.Fatal("aggregate bound changed")
		}
	}
}

func TestTraversalWorkLimit(t *testing.T) {
	type wide struct{ A, B, C, D, E, F, G, H, I, J, K, L, M, N, O, P int64 }
	type input struct{ Rows []wide }
	s := newSchema(false)
	if _, err := s.native(reflect.TypeFor[input](), 0); err != nil {
		t.Fatal(err)
	}
	value := input{Rows: make([]wide, 4096)}
	budget := inputBudget{bytes: maxInputBytes, items: maxListItems, nodes: 65536, ctx: context.Background()}
	if err := budget.native(reflect.ValueOf(value), s, 0); !errors.Is(err, ErrInputLimit) {
		t.Fatal("input work bound was not enforced")
	}
}

func TestFunctionPanicDoesNotFormatPayload(t *testing.T) {
	formatted := false
	b, err := NewBuilder[nativeFixture]()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Function("broken", func(bool) (bool, error) { panic(privateError{&formatted}) }); err != nil {
		t.Fatal(err)
	}
	c, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	f, err := c.Compile("broken(true)")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f(context.Background(), &nativeFixture{}); ok || !errors.Is(err, ErrFunctionPanic) || formatted {
		t.Fatal("function panic disclosed its payload")
	}
}
