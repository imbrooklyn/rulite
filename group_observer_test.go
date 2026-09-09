package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/imbrooklyn/rulite"
)

func observedGroupSet(t testing.TB, kind rulite.GroupKind, outcomes []byte, cause error, audit bool) *rulite.RuleSet[executionInput] {
	t.Helper()
	members := observationRules(outcomes, cause)
	group := rulite.FirstMatchGroup("providers", members...)
	if kind == rulite.GroupFirstFire {
		group = rulite.FirstFireGroup("providers", members...)
	}
	entries := []rulite.Entry[executionInput]{group.Entry()}
	if audit {
		entries = append(entries, rulite.NewRule[executionInput]("audit").When(constantCondition(true, nil)).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil }).Entry(), rulite.FirstFireGroup[executionInput]("empty").Entry())
	}
	return mustEntries(t, entries...)
}

func TestGroupObserverFailureIsolation(t *testing.T) {
	cause, exportCause := errors.New("provider unavailable"), errors.New("export unavailable")
	for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
		for stop := range 3 {
			for condition := range 2 {
				for action := range 2 {
					policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(stop)).WithConditionErrors(rulite.ErrorMode(condition)).WithActionErrors(rulite.ErrorMode(action))
					for _, outcomes := range [][]byte{{}, {0, 0}, {0, 2, 3, 1, 1}, {3, 3}, {4, 1}, {5, 1}} {
						set := observedGroupSet(t, kind, outcomes, cause, true)
						plain := mustReuse(t, set, rulite.WithPolicy(policy), rulite.WithTrace())
						input := executionInput{}
						baseline, baselineErr := plain.Fire(context.Background(), &input)
						facts := expectedEvents(baseline)
						for index, fact := range facts {
							if fact.groupID == "" {
								continue
							}
							for _, panics := range []bool{false, true} {
								var events []rulite.Event
								observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
									events = append(events, event)
									if len(events)-1 == index {
										if panics {
											panic("export unavailable")
										}
										return exportCause
									}
									return nil
								})
								engine := mustReuse(t, set, rulite.WithPolicy(policy), rulite.WithTrace(), rulite.WithObserver(observer))
								for range 2 {
									events = nil
									actual := executionInput{}
									result, err := engine.Fire(context.Background(), &actual)
									checkBusinessParity(t, result, err, baseline, baselineErr)
									if !reflect.DeepEqual(actual, input) || len(events) != index+1 || len(result.Diagnostics()) != 1 || errors.Is(err, exportCause) {
										t.Fatal("group diagnostic changed calls or business outcome")
									}
									diagnostic := result.Diagnostics()[0]
									if !reflect.DeepEqual(diagnostic.Event(), events[index]) {
										t.Fatal("diagnostic lost its delivered snapshot")
									}
									var panicErr *rulite.ObserverPanicError
									if panics {
										if !errors.As(diagnostic, &panicErr) || panicErr.Value() != "export unavailable" || len(panicErr.Stack()) == 0 {
											t.Fatal("group observation panic lost")
										}
										stack := panicErr.Stack()
										clear(panicErr.Stack())
										if !slices.Equal(stack, panicErr.Stack()) {
											t.Fatal("diagnostic stack exposed")
										}
									} else if !errors.Is(diagnostic, exportCause) {
										t.Fatal("diagnostic cause lost")
									}
									for i, event := range events {
										checkObservedEvent(t, event, facts[i], result)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestGroupObserverCancellationBoundaries(t *testing.T) {
	for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
		for _, target := range []rulite.EventKind{rulite.EventGroupResolved, rulite.EventGroupFinished} {
			for _, audit := range []bool{false, true} {
				for _, fault := range []string{"none", "error", "panic"} {
					t.Run(fmt.Sprintf("%s/%s/audit_%t/%s", kind, target, audit, fault), func(t *testing.T) {
						ctx, cancel := context.WithCancelCause(context.Background())
						defer cancel(nil)
						cause, exportCause := errors.New("request withdrawn"), errors.New("export unavailable")
						var events []rulite.Event
						observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
							events = append(events, event)
							if event.Kind() == target {
								cancel(cause)
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
						result, err := mustReuse(t, observedGroupSet(t, kind, []byte{1, 1}, nil, audit)).Fire(ctx, &input, rulite.WithObserver(observer), rulite.WithTrace())
						wantStop, end := rulite.StopContextCanceled, rulite.GroupEndExecutionStopped
						if target == rulite.EventGroupFinished {
							end = rulite.GroupEndResolved
							if !audit {
								wantStop = rulite.StopCompleted
							}
						}
						group, _ := result.Group("providers")
						wantActions := 1
						if kind == rulite.GroupFirstMatch && target == rulite.EventGroupResolved {
							wantActions = 0
						}
						if result.StopReason() != wantStop || group.EndReason() != end || !group.Resolved() || input.value != wantActions || errors.Is(err, cause) != (wantStop == rulite.StopContextCanceled) || errors.Is(err, exportCause) {
							t.Fatal("group boundary changed recorded facts or context precedence")
						}
						if wantStop == rulite.StopContextCanceled {
							var executionErr *rulite.ExecutionError
							if !errors.As(err, &executionErr) || !reflect.DeepEqual(executionErr.Unwrap(), []error{context.Canceled, cause}) {
								t.Fatal("context cause order changed")
							}
						}
						facts := expectedEvents(result)
						if fault == "none" && len(events) != len(facts) || fault != "none" && (len(result.Diagnostics()) != 1 || events[len(events)-1].Kind() != target) {
							t.Fatal("group event delivery did not stop at its diagnostic")
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
}

func TestGroupObserverPropagation(t *testing.T) {
	for _, target := range []rulite.EventKind{rulite.EventGroupResolved, rulite.EventGroupFinished} {
		payload := &unsafePanicStringer{}
		var last rulite.EventKind
		engine := mustReuse(t, observedGroupSet(t, rulite.GroupFirstFire, []byte{1, 1}, nil, true), rulite.WithPanicMode(rulite.PropagatePanics))
		input := executionInput{}
		func() {
			defer func() {
				if recover() != payload {
					t.Error("group observer panic changed")
				}
			}()
			_, _ = engine.Fire(context.Background(), &input, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
				last = event.Kind()
				if last == target {
					panic(payload)
				}
				return nil
			})))
			t.Error("group observer panic did not propagate")
		}()
		if last != target || input.value != 1 {
			t.Fatal("propagated observation panic continued or undid effects")
		}
	}
}

func TestGroupObserverTiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rule := rulite.NewRule[int]("timed").When(func(context.Context, *int) (bool, error) { time.Sleep(3 * time.Second); return true, nil }).Then(func(context.Context, *int) error { time.Sleep(2 * time.Second); return nil })
		var calls int
		observer := rulite.ObserverFunc(func(context.Context, rulite.Event) error { calls++; time.Sleep(7 * time.Second); return nil })
		engine := mustReuse(t, mustEntries(t, rulite.FirstFireGroup("provider", rule).Entry()), rulite.WithObserver(observer), rulite.WithTrace())
		result, err := engine.Fire(context.Background(), new(int))
		trace, _ := result.Trace()
		if err != nil || calls != 7 || trace.Duration() != 54*time.Second || trace.Rules()[0].ConditionDuration() != 3*time.Second || trace.Rules()[0].ActionDuration() != 2*time.Second {
			t.Fatal("group events contaminated callback duration")
		}
	})
}

func TestGroupEventConcurrentReads(t *testing.T) {
	set := observedGroupSet(t, rulite.GroupFirstFire, []byte{3, 1, 1}, errors.New("provider unavailable"), true)
	var count atomic.Int64
	observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		if _, ok := event.Group(); ok {
			count.Add(1)
		}
		return nil
	})
	engine := mustReuse(t, set, rulite.WithTrace(), rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)), rulite.WithObserver(observer))
	var retainedEvents []rulite.Event
	retained, _ := engine.Fire(context.Background(), &executionInput{}, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		retainedEvents = append(retainedEvents, event)
		return nil
	})))
	facts := expectedEvents(retained)
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			var wg sync.WaitGroup
			for range workers {
				wg.Go(func() {
					for range 4 {
						input := executionInput{}
						result, _ := engine.Fire(context.Background(), &input)
						if input.value != 3 || result.Counts().Fired != 2 {
							t.Error("shared group execution changed")
						}
						for i, event := range retainedEvents {
							checkObservedEvent(t, event, facts[i], retained)
						}
						checkResultConsistency(t, retained)
					}
				})
			}
			wg.Wait()
		})
	}
	if count.Load() != 3*4*(1+2+4+8+16+32) {
		t.Fatal("group event count changed under concurrency")
	}
}
