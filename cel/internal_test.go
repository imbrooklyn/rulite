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
		reflect.TypeFor[int](), reflect.TypeFor[*nativeFixture](), reflect.TypeFor[struct{ X int }](),
		reflect.TypeFor[embeddedFixture](), reflect.TypeFor[time.Time](),
	} {
		if _, err := nativeSchema(tp); err == nil {
			t.Fatalf("unsupported schema accepted: %v", tp)
		}
	}
	assertRejectedSchema[structWithField[*int64]](t)
	assertRejectedSchema[structWithField[nativeFixture]](t)
	assertRejectedSchema[structWithField[map[string]int]](t)
	assertRejectedSchema[structWithField[any]](t)
	assertRejectedSchema[structWithField[chan int]](t)
	assertRejectedSchema[structWithField[func()]](t)
	assertRejectedSchema[structWithField[complex128]](t)
	assertRejectedSchema[structWithField[uintptr]](t)
	assertRejectedSchema[structWithField[[2]int]](t)
	assertRejectedSchema[structWithField[time.Duration]](t)
	assertRejectedSchema[structWithField[[]time.Duration]](t)
	assertRejectedSchema[structWithField[[][]int]](t)
	assertRejectedSchema[structWithField[namedList]](t)
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
	fields, err := nativeSchema(tp)
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
		if withinInputLimits(reflect.ValueOf(tc.value), fields) != tc.want {
			t.Fatal("aggregate bound changed")
		}
	}
}
