package rulite_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
)

type panicPayload struct{ formatted *bool }

func (p *panicPayload) String() string {
	*p.formatted = true
	panic("panic value must not be formatted")
}

func TestRecoveredPanic(t *testing.T) {
	for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
		for _, canceled := range []bool{false, true} {
			for _, trace := range []bool{false, true} {
				t.Run(fmt.Sprintf("phase_%d_canceled_%t_trace_%t", phase, canceled, trace), func(t *testing.T) {
					formatted := false
					value := &panicPayload{formatted: &formatted}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					crash := func() {
						if canceled {
							cancel()
						}
						panic(value)
					}
					condition := func(context.Context, *executionInput) (bool, error) {
						if phase == rulite.ConditionPhase {
							crash()
						}
						return true, nil
					}
					action := func(_ context.Context, input *executionInput) error { input.value = 42; crash(); return nil }
					unreached := rulite.NewRule[executionInput]("unreached").When(func(context.Context, *executionInput) (bool, error) {
						t.Fatal("condition called after panic")
						return false, nil
					}).Then(successfulAction)
					options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstMatch).WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError))}
					if trace {
						options = append(options, rulite.WithTrace())
					}
					input := &executionInput{}
					result, err := mustEngine(t, rulite.NewRule[executionInput]("panicking").When(condition).Then(action), unreached).Fire(ctx, input, options...)
					var panicErr *rulite.PanicError
					var aggregate *rulite.ExecutionError
					var failure rulite.Failure
					if !errors.As(err, &aggregate) || !errors.As(err, &panicErr) || !errors.As(err, &failure) {
						t.Fatalf("panic error tree = %T", err)
					}
					if panicErr.RuleID() != "panicking" || panicErr.Phase() != phase || panicErr.Value() != value || failure.Cause() != panicErr || failure.Continued() {
						t.Fatal("panic metadata differs")
					}
					if result.StopReason() != rulite.StopPanic || result.Counts().PanicRecovered != 1 || result.Evaluated() != 1 {
						t.Fatal("panic was not terminal")
					}
					x, _ := result.Rule("panicking")
					if x.Matched() != (phase == rulite.ActionPhase) || x.ActionStarted() != (phase == rulite.ActionPhase) || x.Fired() || x.State() != rulite.RuleFailed {
						t.Fatal("panic phase state lost")
					}
					if phase == rulite.ActionPhase && input.value != 42 {
						t.Fatal("panic rolled back partial mutation")
					}
					stack := panicErr.Stack()
					if !bytes.Contains(stack, []byte("TestRecoveredPanic")) || !bytes.Contains(stack, []byte("runtime/debug.Stack")) {
						t.Fatalf("missing callback stack: %s", stack)
					}
					original := bytes.Clone(stack)
					clear(stack)
					if !bytes.Equal(panicErr.Stack(), original) {
						t.Fatal("Stack exposes mutable bytes")
					}
					_ = panicErr.Error()
					_ = err.Error()
					_ = result.Explain().String()
					if formatted {
						t.Fatal("diagnostics formatted the panic value")
					}
					if canceled {
						causes := aggregate.Unwrap()
						if len(causes) != 2 || causes[1] != context.Canceled || !errors.Is(err, context.Canceled) {
							t.Fatal("panic boundary lost cancellation")
						}
					}
					checkResultConsistency(t, result)
				})
			}
		}
	}
}

func TestPanicModesAndOverrideOrder(t *testing.T) {
	value := &struct{ label string }{"original panic"}
	for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
		condition := func(context.Context, *executionInput) (bool, error) {
			if phase == rulite.ConditionPhase {
				panic(value)
			}
			return true, nil
		}
		action := func(context.Context, *executionInput) error { panic(value) }
		engine := mustEngine(t, rulite.NewRule[executionInput]("panic").When(condition).Then(action))
		for _, trace := range []bool{false, true} {
			options := []rulite.FireOption{rulite.WithPanicMode(rulite.RecoverPanics), rulite.WithPanicMode(rulite.PropagatePanics), {}}
			if trace {
				options = append(options, rulite.WithTrace())
			}
			func() {
				defer func() {
					if got := recover(); got != value {
						t.Fatalf("propagated panic = %T; want original pointer", got)
					}
				}()
				_, _ = engine.Fire(context.Background(), &executionInput{}, options...)
				t.Error("PropagatePanics returned")
			}()
			options = append(options, rulite.WithPanicMode(rulite.RecoverPanics))
			result, err := engine.Fire(context.Background(), &executionInput{}, options...)
			var panicErr *rulite.PanicError
			if !errors.As(err, &panicErr) || result.StopReason() != rulite.StopPanic {
				t.Fatal("last panic mode did not win")
			}
		}
	}
	engine := mustEngine(t, rulite.NewRule[executionInput]("nil-panic").When(func(context.Context, *executionInput) (bool, error) { panic(nil) }).Then(successfulAction))
	result, err := engine.Fire(context.Background(), &executionInput{})
	var panicErr *rulite.PanicError
	if !errors.As(err, &panicErr) || len(panicErr.Stack()) == 0 || result.StopReason() != rulite.StopPanic {
		t.Fatal("nil panic was not recovered")
	}
}

type providerError struct{ provider string }

func (e *providerError) Error() string { return e.provider + " unavailable" }

func TestExecutionErrorOrderAndCopies(t *testing.T) {
	first := errors.New("eligibility unavailable")
	second := &providerError{provider: "primary"}
	last := errors.New("audit unavailable")
	custom := errors.New("request withdrawn")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	engine := mustEngine(t,
		rulite.NewRule[executionInput]("first").When(constantCondition(true, first)).Then(successfulAction),
		rulite.NewRule[executionInput]("second").When(constantCondition(true, nil)).Then(func(context.Context, *executionInput) error { return fmt.Errorf("route: %w", second) }),
		rulite.NewRule[executionInput]("last").When(constantCondition(true, nil)).Then(func(context.Context, *executionInput) error { cancel(custom); return last }),
	)
	result, err := engine.Fire(ctx, &executionInput{}, rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)))
	var aggregate *rulite.ExecutionError
	var business *providerError
	if !errors.As(err, &aggregate) || !errors.As(err, &business) || business != second {
		t.Fatal("errors.As lost business cause")
	}
	for _, cause := range []error{first, second, last, context.Canceled, custom} {
		if !errors.Is(err, cause) {
			t.Errorf("lost cause %v", cause)
		}
	}
	wrapped, failures := aggregate.Unwrap(), aggregate.Failures()
	if len(wrapped) != 5 || len(failures) != 3 || wrapped[3] != context.Canceled || wrapped[4] != custom {
		t.Fatalf("aggregation order = %v", wrapped)
	}
	for index, id := range []rulite.RuleID{"first", "second", "last"} {
		failure, ok := wrapped[index].(rulite.Failure)
		if !ok || failure.RuleID() != id || !reflect.DeepEqual(failure, failures[index]) || !reflect.DeepEqual(failure, result.Failures()[index]) || failure.Cause() != failure.Unwrap() {
			t.Fatal("canonical failure order differs")
		}
	}
	before := err.Error()
	clear(wrapped)
	clear(failures)
	if err.Error() != before || aggregate.Unwrap()[0] == nil || aggregate.Failures()[0].RuleID() != "first" {
		t.Fatal("error accessors expose owned slices")
	}
	checkResultConsistency(t, result)
}

func TestExecutionErrorZeroValues(t *testing.T) {
	var failure rulite.Failure
	if failure.Error() == "" || failure.Cause() != nil || failure.Unwrap() != nil || failure.RuleID() != "" || failure.Continued() {
		t.Fatal("unsafe zero Failure")
	}
	for _, e := range []*rulite.ExecutionError{nil, {}} {
		if e.Error() == "" || len(e.Unwrap()) != 0 || len(e.Failures()) != 0 {
			t.Fatal("unsafe zero ExecutionError")
		}
	}
	for _, e := range []*rulite.PanicError{nil, {}} {
		if e.Error() == "" || e.RuleID() != "" || e.Phase() != rulite.ConditionPhase || e.Value() != nil || len(e.Stack()) != 0 {
			t.Fatal("unsafe zero PanicError")
		}
	}
	var result rulite.Result
	requireZeroResult(t, result)
	if !strings.Contains(result.Explain().String(), "execution: not-started") || strings.Contains(result.Explain().String(), "completed") {
		t.Fatal("zero explanation claims completion")
	}
}

func TestConcurrentPanicResultReads(t *testing.T) {
	engine := mustEngine(t, rulite.NewRule[executionInput]("panic").When(func(context.Context, *executionInput) (bool, error) { panic("private contents") }).Then(successfulAction))
	result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	var original *rulite.PanicError
	if !errors.As(err, &original) {
		t.Fatal("missing recovered panic")
	}
	stack := original.Stack()
	var readers sync.WaitGroup
	for reader := 0; reader < 16; reader++ {
		readers.Go(func() {
			for attempt := 0; attempt < 20; attempt++ {
				var panicErr *rulite.PanicError
				x, _ := result.Rule("panic")
				if !errors.As(x.Error(), &panicErr) || !bytes.Equal(panicErr.Stack(), stack) {
					t.Error("panic stack changed")
					return
				}
				clear(panicErr.Stack())
				checkResultConsistency(t, result)
			}
		})
	}
	readers.Wait()
}
