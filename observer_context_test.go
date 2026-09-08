package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/imbrooklyn/rulite"
)

func TestObserverCancellationAtEveryEvent(t *testing.T) {
	for outcome := byte(0); outcome < 6; outcome++ {
		cause := errors.New("provider unavailable")
		set := mustCompile(t, observationRules([]byte{outcome, 1}, cause)...)
		policy := rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)
		engine := mustReuse(t, set, rulite.WithPolicy(policy))
		baseline, _ := engine.Fire(context.Background(), &executionInput{})
		for index, fact := range expectedEvents(baseline) {
			for _, fault := range []string{"none", "error", "panic"} {
				t.Run(fmt.Sprintf("outcome_%d/event_%d/%s", outcome, index, fault), func(t *testing.T) {
					ctx, cancel := context.WithCancelCause(context.Background())
					defer cancel(nil)
					cancelCause := errors.New("request withdrawn")
					exportCause := errors.New("export unavailable")
					var events []rulite.Event
					observer := rulite.ObserverFunc(func(gotCtx context.Context, event rulite.Event) error {
						if gotCtx != ctx {
							t.Fatal("observer received a replacement context")
						}
						events = append(events, event)
						if len(events)-1 == index {
							cancel(cancelCause)
							if fault == "error" {
								return exportCause
							}
							if fault == "panic" {
								panic("export unavailable")
							}
						}
						return nil
					})
					input := executionInput{}
					result, err := engine.Fire(ctx, &input, rulite.WithObserver(observer))
					wantStop := rulite.StopContextCanceled
					if result.Counts().PanicRecovered != 0 {
						wantStop = rulite.StopPanic
					}
					if fact.kind == rulite.EventExecutionFinished {
						wantStop = baseline.StopReason()
						if result.Counts() != baseline.Counts() || errors.Is(err, cancelCause) || errors.Is(err, context.Canceled) {
							t.Fatal("finish cancellation changed terminal business facts")
						}
					} else {
						if !errors.Is(err, cancelCause) || !errors.Is(err, context.Canceled) {
							t.Fatal("observer cancellation lost its real cause")
						}
						var executionErr *rulite.ExecutionError
						if !errors.As(err, &executionErr) {
							t.Fatal("missing execution error")
						}
						children := executionErr.Unwrap()
						if len(children) < 2 || children[len(children)-2] != context.Canceled || children[len(children)-1] != cancelCause {
							t.Fatal("cancellation causes out of order")
						}
						for i, failure := range result.Failures() {
							if got, ok := children[i].(rulite.Failure); !ok || got.RuleID() != failure.RuleID() || got.Cause() != failure.Cause() {
								t.Fatal("business failures must precede context causes")
							}
						}
					}
					if result.StopReason() != wantStop || errors.Is(err, exportCause) {
						t.Fatal("diagnostic changed stop precedence or business error")
					}
					if fact.kind == rulite.EventExecutionStarted && (result.Counts().Evaluated != 0 || input.value != 0) {
						t.Fatal("callback ran after start cancellation")
					}
					if fact.kind == rulite.EventRuleMatched || fact.kind == rulite.EventRuleEvaluated && fact.outcome == rulite.ConditionOutcomeTrue {
						if result.Counts().Skipped != 1 || result.Counts().Matched != input.value+1 {
							t.Fatal("match was lost or action ran after cancellation")
						}
					}
					facts := expectedEvents(result)
					wantCalls := len(facts)
					if fault != "none" {
						wantCalls = index + 1
						if len(result.Diagnostics()) != 1 || result.Diagnostics()[0].Event().Kind() != fact.kind {
							t.Fatal("cancellation lost diagnostic")
						}
					} else if len(result.Diagnostics()) != 0 {
						t.Fatal("cancellation invented diagnostic")
					}
					if len(events) != wantCalls {
						t.Fatal("wrong event sequence at cancellation boundary")
					}
					for i, event := range events {
						checkObservedEvent(t, event, facts[i], result)
					}
					checkResultConsistency(t, result)
				})
			}
		}
	}
}

func TestObserverAlreadyDoneContextAndEmptySet(t *testing.T) {
	for _, size := range []int{0, 1} {
		for _, deadline := range []bool{false, true} {
			cause := errors.New("request ended")
			var ctx context.Context
			var cancel context.CancelFunc
			wantStop, wantErr := rulite.StopContextCanceled, context.Canceled
			if deadline {
				ctx, cancel = context.WithDeadlineCause(context.Background(), time.Unix(0, 0), cause)
				wantStop, wantErr = rulite.StopContextDeadlineExceeded, context.DeadlineExceeded
			} else {
				var cancelCause context.CancelCauseFunc
				ctx, cancelCause = context.WithCancelCause(context.Background())
				cancelCause(cause)
				cancel = func() { cancelCause(nil) }
			}
			var events []rulite.Event
			observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error { events = append(events, event); return nil })
			engine := mustEngine(t, observationRules(make([]byte, size), nil)...)
			result, err := engine.Fire(ctx, &executionInput{}, rulite.WithObserver(observer))
			cancel()
			if !errors.Is(err, wantErr) || !errors.Is(err, cause) || result.StopReason() != wantStop || result.Counts().Evaluated != 0 || len(events) != 2 {
				t.Fatal("already-done execution did not emit start and finish")
			}
			for i, fact := range expectedEvents(result) {
				checkObservedEvent(t, events[i], fact, result)
			}
		}
	}
}

func TestObserverBackpressureIsCooperative(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		release := make(chan struct{})
		done := make(chan struct{})
		observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
			if event.Kind() == rulite.EventExecutionStarted {
				<-release
			}
			return nil
		})
		engine := mustEngine(t, observationRules([]byte{1}, nil)...)
		go func() {
			defer close(done)
			result, err := engine.Fire(ctx, &executionInput{}, rulite.WithObserver(observer))
			if !errors.Is(err, context.Canceled) || result.Counts().Evaluated != 0 {
				t.Error("cancellation was not checked after observer returned")
			}
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("Fire returned while observer was blocked")
		default:
		}
		close(release)
		<-done
	})
}

func TestObserverZeroEventAndKindNames(t *testing.T) {
	var event rulite.Event
	rule, ruleOK := event.Rule()
	phase, phaseOK := event.Phase()
	outcome, outcomeOK := event.ConditionOutcome()
	failure, failureOK := event.Failure()
	counts, countsOK := event.Counts()
	if event.Kind() != rulite.EventNone || event.StopReason() != rulite.StopNone || ruleOK || phaseOK || outcomeOK || failureOK || countsOK || rule.ID() != "" || phase != rulite.ConditionPhase || outcome != rulite.ConditionOutcomeNotEvaluated || failure.Cause() != nil || counts != (rulite.Counts{}) {
		t.Fatal("zero event contains facts")
	}
	var names []string
	for kind := rulite.EventNone; kind <= rulite.EventExecutionFinished; kind++ {
		names = append(names, kind.String())
	}
	if !slices.Equal(names, []string{"none", "execution-started", "rule-evaluated", "rule-matched", "rule-fired", "rule-failed", "execution-finished"}) || rulite.EventKind(255).String() != "unknown" {
		t.Fatal("event kind names changed")
	}
}
