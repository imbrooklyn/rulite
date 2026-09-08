package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
)

// Each byte selects a callback outcome and optional cancellation. This model
// predicts final states and call order independently of the engine's ledger.
func checkOutcomeSequence(t testing.TB, outcomes []byte, stop rulite.StopMode, conditionMode, actionMode rulite.ErrorMode) {
	t.Helper()
	if len(outcomes) > 64 {
		outcomes = outcomes[:64]
	}
	cause := errors.New("callback unavailable")
	var expectedStates []rulite.RuleState
	var expectedCalls []string
	wantStop := rulite.StopCompleted
	wantError := false
	for index, encoded := range outcomes {
		state := rulite.RuleNotEvaluated
		if wantStop == rulite.StopCompleted {
			kind, canceled := encoded%6, encoded&8 != 0
			expectedCalls = append(expectedCalls, fmt.Sprintf("%d:condition", index))
			switch kind {
			case 0:
				state = rulite.RuleUnmatched
			case 1, 4:
				state = rulite.RuleFailed
				wantError = true
				if kind == 4 {
					wantStop = rulite.StopPanic
				} else if conditionMode == rulite.StopOnError {
					wantStop = rulite.StopConditionError
				}
			default:
				state = rulite.RuleSkipped
				// Bit 4 moves cancellation from condition return to action return.
				if !canceled || encoded&16 != 0 {
					expectedCalls = append(expectedCalls, fmt.Sprintf("%d:action", index))
					state = rulite.RuleFired
					if kind == 3 || kind == 5 {
						state = rulite.RuleFailed
						wantError = true
						if kind == 5 {
							wantStop = rulite.StopPanic
						} else if actionMode == rulite.StopOnError {
							wantStop = rulite.StopActionError
						}
					}
					if wantStop == rulite.StopCompleted {
						if stop == rulite.StopOnFirstMatch {
							wantStop = rulite.StopFirstMatch
						}
						if stop == rulite.StopOnFirstFire && state == rulite.RuleFired {
							wantStop = rulite.StopFirstFire
						}
					}
				}
			}
			if canceled {
				wantError = true
				if wantStop != rulite.StopPanic {
					wantStop = rulite.StopContextCanceled
				}
			}
		}
		expectedStates = append(expectedStates, state)
	}
	var baselineText string
	for _, traced := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var calls []string
		rules := make([]rulite.Rule[executionInput], len(outcomes))
		for index, encoded := range outcomes {
			kind, canceled := encoded%6, encoded&8 != 0
			rules[index] = rulite.NewRule[executionInput](rulite.RuleID(fmt.Sprintf("rule/%d", index))).When(func(context.Context, *executionInput) (bool, error) {
				calls = append(calls, fmt.Sprintf("%d:condition", index))
				if canceled && (encoded&16 == 0 || kind == 0 || kind == 1 || kind == 4) {
					cancel()
				}
				if kind == 4 {
					panic("condition panic")
				}
				if kind == 1 {
					return true, cause
				}
				return kind != 0, nil
			}).Then(func(_ context.Context, input *executionInput) error {
				calls = append(calls, fmt.Sprintf("%d:action", index))
				input.value++
				if canceled {
					cancel()
				}
				if kind == 5 {
					panic("action panic")
				}
				if kind == 3 {
					return cause
				}
				return nil
			})
		}
		options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithStop(stop).WithConditionErrors(conditionMode).WithActionErrors(actionMode))}
		if traced {
			options = append(options, rulite.WithTrace())
		}
		result, err := mustEngine(t, rules...).Fire(ctx, &executionInput{}, options...)
		cancel()
		if result.StopReason() != wantStop || (err != nil) != wantError || !slices.Equal(calls, expectedCalls) {
			t.Fatalf("outcomes=%v modes=%d/%d/%d: stop=%v error=%v calls=%v; want stop=%v error=%t calls=%v", outcomes, stop, conditionMode, actionMode, result.StopReason(), err, calls, wantStop, wantError, expectedCalls)
		}
		for index, expected := range expectedStates {
			x, ok := result.Rule(rules[index].ID())
			if !ok || x.State() != expected {
				t.Fatalf("rule %d state=%v; want %v", index, x.State(), expected)
			}
		}
		checkResultConsistency(t, result)
		if text := result.Explain().String(); traced {
			if text != baselineText {
				t.Fatal("trace changed explanation semantics")
			}
		} else {
			baselineText = text
		}
	}
}

func TestExecutionStateProperties(t *testing.T) {
	random := rand.New(rand.NewPCG(53, 97))
	for trial := 0; trial < 600; trial++ {
		outcomes := make([]byte, random.IntN(32))
		for index := range outcomes {
			outcomes[index] = byte(random.IntN(6))
			if random.IntN(8) == 0 {
				outcomes[index] |= 8
			}
			if random.IntN(2) == 0 {
				outcomes[index] |= 16
			}
		}
		checkOutcomeSequence(t, outcomes, rulite.StopMode(random.IntN(3)), rulite.ErrorMode(random.IntN(2)), rulite.ErrorMode(random.IntN(2)))
	}
}

func FuzzExecutionOutcomes(f *testing.F) {
	f.Add([]byte{0, 1, 3, 2}, byte(2), byte(1), byte(1))
	f.Add([]byte{2, 3, 5}, byte(0), byte(1), byte(1))
	f.Add([]byte{24, 25, 26}, byte(1), byte(0), byte(0))
	f.Fuzz(func(t *testing.T, outcomes []byte, stop, conditionMode, actionMode byte) {
		checkOutcomeSequence(t, outcomes, rulite.StopMode(stop%3), rulite.ErrorMode(conditionMode%2), rulite.ErrorMode(actionMode%2))
	})
}

func TestResultCopiesAndConcurrentReads(t *testing.T) {
	cause := errors.New("provider unavailable")
	engine := mustEngine(t,
		rulite.NewRule[executionInput]("fired").When(rulite.All(constantCondition(true, nil))).Then(successfulAction),
		rulite.NewRule[executionInput]("failed").When(constantCondition(true, nil)).Then(func(context.Context, *executionInput) error { return cause }),
		rulite.NewRule[executionInput]("unreached").When(constantCondition(false, nil)).Then(successfulAction),
	)
	result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	copyOfResult := result
	baseline := result.Explain().String()
	var readers sync.WaitGroup
	for reader := 0; reader < 32; reader++ {
		readers.Go(func() {
			for attempt := 0; attempt < 15; attempt++ {
				clear(copyOfResult.Matched())
				clear(copyOfResult.Fired())
				clear(copyOfResult.Failures())
				clear(copyOfResult.Explain().Rules())
				trace, ok := copyOfResult.Trace()
				if !ok {
					t.Error("trace disappeared")
					return
				}
				rules := trace.Rules()
				root, _ := rules[0].ConditionTree()
				clear(root.Children())
				clear(rules)
				if copyOfResult.Explain().String() != baseline {
					t.Error("concurrent accessor mutation changed result")
					return
				}
				checkResultConsistency(t, copyOfResult)
			}
		})
	}
	readers.Wait()
	if _, ok := result.Rule("unknown"); ok {
		t.Fatal("unknown ID returned a rule")
	}
	if !reflect.DeepEqual(result.Counts(), copyOfResult.Counts()) {
		t.Fatal("result copy changed")
	}
}

func TestSharedEngineConcurrentFire(t *testing.T) {
	var rules []rulite.Rule[executionInput]
	for index := 0; index < 32; index++ {
		rules = append(rules, rulite.NewRule[executionInput](rulite.RuleID(fmt.Sprintf("increment/%d", index))).When(rulite.All(
			func(_ context.Context, input *executionInput) (bool, error) { return input.value == index, nil },
			rulite.Not(constantCondition(false, nil)),
		)).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil }))
	}
	engine := mustEngine(t, rules...)
	for _, concurrency := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("goroutines_%d", concurrency), func(t *testing.T) {
			var workers sync.WaitGroup
			for worker := 0; worker < concurrency; worker++ {
				workers.Go(func() {
					for attempt := 0; attempt < 8; attempt++ {
						input := &executionInput{}
						var options []rulite.FireOption
						if (worker+attempt)%2 == 0 {
							options = append(options, rulite.WithTrace())
						}
						result, err := engine.Fire(context.Background(), input, options...)
						if err != nil || input.value != 32 || result.Counts().Fired != 32 {
							t.Errorf("concurrent execution value=%d counts=%+v error=%v", input.value, result.Counts(), err)
							return
						}
						checkResultConsistency(t, result)
					}
				})
			}
			workers.Wait()
		})
	}
}
