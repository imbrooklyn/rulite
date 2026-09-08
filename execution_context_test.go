package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/imbrooklyn/rulite"
)

// Cancellation occurs immediately after one successful Err observation, so
// only the next boundary can observe it. All accesses are on the Fire goroutine.
type cancelAfterObservation struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cancelAfterObservation) Err() error {
	err := c.Context.Err()
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return err
}

func TestContextBeforeEachCondition(t *testing.T) {
	for _, after := range []int{1, 3, 4} {
		t.Run(fmt.Sprintf("observation_%d", after), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAfterObservation{Context: base, cancel: cancel, remaining: after}
			matched := after != 3
			first := rulite.NewRule[executionInput]("first").When(constantCondition(matched, nil)).Then(successfulAction)
			next := rulite.NewRule[executionInput]("next").When(func(context.Context, *executionInput) (bool, error) {
				t.Fatal("condition started after cancellation")
				return false, nil
			}).Then(successfulAction)
			result, err := mustEngine(t, first, next).Fire(ctx, &executionInput{})
			wantEvaluated := 1
			if after == 1 {
				wantEvaluated = 0
			}
			if result.Evaluated() != wantEvaluated || result.StopReason() != rulite.StopContextCanceled || !errors.Is(err, context.Canceled) {
				t.Fatalf("condition boundary = %+v, %v, %v", result.Counts(), result.StopReason(), err)
			}
			checkResultConsistency(t, result)
		})
	}
}

func TestContextBeforeExecution(t *testing.T) {
	custom := errors.New("request withdrawn")
	canceled, cancel := context.WithCancelCause(context.Background())
	cancel(custom)
	expired, release := context.WithDeadlineCause(context.Background(), time.Now().Add(-time.Hour), custom)
	defer release()
	for _, ctx := range []context.Context{canceled, expired} {
		for _, empty := range []bool{false, true} {
			for _, trace := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v_empty_%t_trace_%t", ctx.Err(), empty, trace), func(t *testing.T) {
					var rules []rulite.Rule[executionInput]
					if !empty {
						rules = append(rules, rulite.NewRule[executionInput]("unreached").When(func(context.Context, *executionInput) (bool, error) {
							t.Fatal("condition called on canceled context")
							return true, nil
						}).Then(successfulAction))
					}
					var options []rulite.FireOption
					if trace {
						options = append(options, rulite.WithTrace())
					}
					result, err := mustEngine(t, rules...).Fire(ctx, &executionInput{}, options...)
					want := rulite.StopContextCanceled
					if ctx.Err() == context.DeadlineExceeded {
						want = rulite.StopContextDeadlineExceeded
					}
					if !result.Executed() || !result.Stopped() || result.StopReason() != want || result.Evaluated() != 0 || !errors.Is(err, ctx.Err()) || !errors.Is(err, custom) {
						t.Fatalf("canceled execution = %+v, %v, %v", result.Counts(), result.StopReason(), err)
					}
					var aggregate *rulite.ExecutionError
					if !errors.As(err, &aggregate) || len(aggregate.Failures()) != 0 {
						t.Fatal("context error is not a rule failure")
					}
					if causes := aggregate.Unwrap(); len(causes) != 2 || causes[0] != ctx.Err() || causes[1] != custom {
						t.Fatalf("context causes = %v", causes)
					}
					checkResultConsistency(t, result)
				})
			}
		}
	}
}

func TestContextAtFinalCallbackBoundary(t *testing.T) {
	callbackCause := errors.New("provider unavailable")
	custom := errors.New("request withdrawn")
	for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
		for _, outcome := range []string{"false", "true", "error"} {
			if phase == rulite.ActionPhase && outcome == "false" {
				continue
			}
			for _, mode := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
				for _, trace := range []bool{false, true} {
					t.Run(fmt.Sprintf("phase_%d_%s_mode_%d_trace_%t", phase, outcome, mode, trace), func(t *testing.T) {
						ctx, cancel := context.WithCancelCause(context.Background())
						defer cancel(nil)
						condition := func(context.Context, *executionInput) (bool, error) {
							if phase != rulite.ConditionPhase {
								return true, nil
							}
							cancel(custom)
							if outcome == "error" {
								return true, callbackCause
							}
							return outcome == "true", nil
						}
						action := func(_ context.Context, input *executionInput) error {
							if phase == rulite.ConditionPhase {
								t.Fatal("action started after match boundary cancellation")
							}
							input.value = 12
							cancel(custom)
							if outcome == "error" {
								return callbackCause
							}
							return nil
						}
						policy := rulite.DefaultPolicy().WithStop(rulite.StopOnFirstMatch).WithConditionErrors(mode).WithActionErrors(mode)
						options := []rulite.FireOption{rulite.WithPolicy(policy)}
						if trace {
							options = append(options, rulite.WithTrace())
						}
						input := &executionInput{}
						result, err := mustEngine(t, rulite.NewRule[executionInput]("final").When(condition).Then(action)).Fire(ctx, input, options...)
						if result.StopReason() != rulite.StopContextCanceled || !errors.Is(err, context.Canceled) || !errors.Is(err, custom) {
							t.Fatalf("context boundary = %v, %v", result.StopReason(), err)
						}
						var aggregate *rulite.ExecutionError
						if !errors.As(err, &aggregate) {
							t.Fatal("missing ExecutionError")
						}
						causes := aggregate.Unwrap()
						if outcome == "error" {
							if len(causes) != 3 {
								t.Fatalf("causes = %v", causes)
							}
							failure, ok := causes[0].(rulite.Failure)
							if !ok || failure.Phase() != phase || failure.Cause() != callbackCause || failure.Continued() != (mode == rulite.ContinueOnError) {
								t.Fatal("callback failure must precede context and preserve disposition")
							}
							causes = causes[1:]
						}
						if len(causes) != 2 || causes[0] != context.Canceled || causes[1] != custom {
							t.Fatalf("context cause order = %v", causes)
						}
						x, _ := result.Rule("final")
						wantState := rulite.RuleFired
						if phase == rulite.ConditionPhase {
							wantState = rulite.RuleUnmatched
							if outcome == "true" {
								wantState = rulite.RuleSkipped
							}
						} else if input.value != 12 {
							t.Fatal("action mutation lost at cancellation")
						}
						if outcome == "error" {
							wantState = rulite.RuleFailed
						}
						if x.State() != wantState {
							t.Fatalf("final state = %v; want %v", x.State(), wantState)
						}
						checkResultConsistency(t, result)
					})
				}
			}
		}
	}
}

func TestActiveContextSentinelIsCallbackError(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
			for _, mode := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
				t.Run(fmt.Sprintf("%v_phase_%d_mode_%d", sentinel, phase, mode), func(t *testing.T) {
					condition := constantCondition(true, nil)
					action := rulite.Action[executionInput](successfulAction)
					if phase == rulite.ConditionPhase {
						condition = constantCondition(true, sentinel)
					} else {
						action = func(context.Context, *executionInput) error { return sentinel }
					}
					result, err := mustEngine(t,
						rulite.NewRule[executionInput]("failure").When(condition).Then(action),
						rulite.NewRule[executionInput]("fallback").When(constantCondition(true, nil)).Then(successfulAction),
					).Fire(context.Background(), &executionInput{}, rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(mode).WithActionErrors(mode)))
					wantStop := rulite.StopConditionError
					if phase == rulite.ActionPhase {
						wantStop = rulite.StopActionError
					}
					if mode == rulite.ContinueOnError {
						wantStop = rulite.StopCompleted
					}
					var aggregate *rulite.ExecutionError
					if result.StopReason() != wantStop || !errors.Is(err, sentinel) || !errors.As(err, &aggregate) || len(aggregate.Unwrap()) != 1 {
						t.Fatalf("active context error = %v, %v", result.StopReason(), err)
					}
					wantEvaluated := 1
					if mode == rulite.ContinueOnError {
						wantEvaluated = 2
					}
					if result.Evaluated() != wantEvaluated {
						t.Fatal("context sentinel bypassed phase policy")
					}
					checkResultConsistency(t, result)
				})
			}
		}
	}
}

func TestDeadlineDuringCallback(t *testing.T) {
	for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
		t.Run(fmt.Sprintf("phase_%d", phase), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.New("pricing time budget exhausted")
				ctx, cancel := context.WithTimeoutCause(context.Background(), time.Second, cause)
				defer cancel()
				condition := func(ctx context.Context, _ *executionInput) (bool, error) {
					if phase == rulite.ConditionPhase {
						<-ctx.Done()
						return false, nil
					}
					return true, nil
				}
				action := func(ctx context.Context, _ *executionInput) error { <-ctx.Done(); return nil }
				result, err := mustEngine(t, rulite.NewRule[executionInput]("deadline").When(condition).Then(action)).Fire(ctx, &executionInput{}, rulite.WithTrace(), rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)))
				if result.StopReason() != rulite.StopContextDeadlineExceeded || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cause) {
					t.Fatalf("deadline = %v, %v", result.StopReason(), err)
				}
				checkResultConsistency(t, result)
			})
		})
	}
}

func TestFireWaitsForUncooperativeCallback(t *testing.T) {
	for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
		t.Run(fmt.Sprintf("phase_%d", phase), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
				condition := func(context.Context, *executionInput) (bool, error) {
					if phase == rulite.ConditionPhase {
						close(entered)
						<-release
					}
					return true, nil
				}
				action := func(_ context.Context, input *executionInput) error {
					if phase == rulite.ActionPhase {
						close(entered)
						<-release
						input.value = 99
					}
					return nil
				}
				engine := mustEngine(t, rulite.NewRule[executionInput]("blocking").When(condition).Then(action))
				input := &executionInput{}
				var result rulite.Result
				var err error
				go func() { result, err = engine.Fire(ctx, input); close(finished) }()
				<-entered
				cancel()
				synctest.Wait()
				returnedEarly := false
				select {
				case <-finished:
					returnedEarly = true
				default:
				}
				close(release)
				<-finished
				synctest.Wait()
				if returnedEarly {
					t.Fatal("Fire returned while its callback was still blocked")
				}
				if phase == rulite.ActionPhase && input.value != 99 {
					t.Fatal("Fire did not wait for final mutation")
				}
				if !errors.Is(err, context.Canceled) || result.StopReason() != rulite.StopContextCanceled {
					t.Fatalf("uncooperative boundary = %v, %v", result.StopReason(), err)
				}
				checkResultConsistency(t, result)
			})
		})
	}
}
