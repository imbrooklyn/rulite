package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/imbrooklyn/rulite"
)

type executionInput struct {
	value int
	calls []string
}

func mustEngine[T any](t testing.TB, rules ...rulite.Rule[T]) *rulite.Engine[T] {
	t.Helper()
	engine, err := rulite.NewEngine(rules...)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func constantCondition(matched bool, err error) rulite.Condition[executionInput] {
	return func(context.Context, *executionInput) (bool, error) { return matched, err }
}

func successfulAction(context.Context, *executionInput) error { return nil }

func requireZeroResult(t *testing.T, result rulite.Result) {
	t.Helper()
	if result.Executed() || result.Stopped() || result.StopReason() != rulite.StopNone || result.Counts() != (rulite.Counts{}) || result.Evaluated() != 0 {
		t.Fatalf("expected not-started result: %+v, %v", result.Counts(), result.StopReason())
	}
	if len(result.Matched()) != 0 || len(result.Fired()) != 0 || len(result.Failures()) != 0 || len(result.Explain().Rules()) != 0 {
		t.Fatal("not-started result contains rules")
	}
	if _, ok := result.Rule("unknown"); ok {
		t.Fatal("not-started result contains an ID")
	}
	if _, ok := result.Trace(); ok {
		t.Fatal("not-started result has a trace")
	}
}

func TestPolicyValueSemantics(t *testing.T) {
	var zero rulite.ExecutionPolicy
	if zero != rulite.DefaultPolicy() || zero.StopMode() != rulite.EvaluateAll || zero.ConditionErrorMode() != rulite.StopOnError || zero.ActionErrorMode() != rulite.StopOnError {
		t.Fatal("zero policy differs from default")
	}
	changed := zero.WithStop(rulite.StopOnFirstFire).WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)
	if changed.StopMode() != rulite.StopOnFirstFire || changed.ConditionErrorMode() != rulite.ContinueOnError || changed.ActionErrorMode() != rulite.ContinueOnError || zero != rulite.DefaultPolicy() {
		t.Fatal("policy copies are not independent")
	}
	if got := changed.WithStop(rulite.StopOnFirstMatch).WithStop(rulite.EvaluateAll).WithConditionErrors(rulite.StopOnError).WithActionErrors(rulite.StopOnError); got != zero {
		t.Fatal("policy setters do not replace their fields")
	}
}

func TestFirePreflightAndOptionOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	empty := mustEngine[executionInput](t)
	var nilEngine *rulite.Engine[executionInput]
	var zeroEngine rulite.Engine[executionInput]
	input := &executionInput{}
	badPolicy := rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopMode(255)))
	badPanic := rulite.WithPanicMode(rulite.PanicMode(255))
	cases := []struct {
		name    string
		engine  *rulite.Engine[executionInput]
		ctx     context.Context
		input   *executionInput
		options []rulite.FireOption
		want    error
	}{
		{"nil_context_first", nilEngine, nil, nil, []rulite.FireOption{badPolicy}, rulite.ErrNilContext},
		{"nil_input_second", nilEngine, ctx, nil, []rulite.FireOption{badPolicy}, rulite.ErrNilInput},
		{"nil_engine_third", nilEngine, ctx, input, []rulite.FireOption{badPolicy}, rulite.ErrInvalidEngine},
		{"zero_engine", &zeroEngine, ctx, input, []rulite.FireOption{badPolicy}, rulite.ErrInvalidEngine},
		{"options_before_cancellation", empty, ctx, input, []rulite.FireOption{rulite.WithTrace(), badPolicy}, rulite.ErrInvalidPolicy},
		{"policy_cannot_be_repaired", empty, ctx, input, []rulite.FireOption{badPolicy, rulite.WithPolicy(rulite.DefaultPolicy())}, rulite.ErrInvalidPolicy},
		{"panic_cannot_be_repaired", empty, ctx, input, []rulite.FireOption{badPanic, rulite.WithPanicMode(rulite.RecoverPanics)}, rulite.ErrInvalidPanicMode},
		{"policy_invalid_first", empty, ctx, input, []rulite.FireOption{badPolicy, badPanic}, rulite.ErrInvalidPolicy},
		{"panic_invalid_first", empty, ctx, input, []rulite.FireOption{badPanic, badPolicy}, rulite.ErrInvalidPanicMode},
		{"invalid_condition_mode", empty, ctx, input, []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(2))}, rulite.ErrInvalidPolicy},
		{"invalid_action_mode", empty, ctx, input, []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(2))}, rulite.ErrInvalidPolicy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.engine.Fire(tc.ctx, tc.input, tc.options...)
			if err != tc.want {
				t.Fatalf("error = %v; want direct %v", err, tc.want)
			}
			requireZeroResult(t, result)
		})
	}
	for _, trace := range []bool{false, true} {
		var options []rulite.FireOption
		if trace {
			options = append(options, rulite.WithTrace(), rulite.WithTrace())
		}
		result, err := empty.Fire(context.Background(), input, options...)
		if err != nil || !result.Executed() || result.StopReason() != rulite.StopCompleted || result.Stopped() || result.Counts() != (rulite.Counts{}) {
			t.Fatalf("empty execution = %+v, %v, %v", result.Counts(), result.StopReason(), err)
		}
		gotTrace, ok := result.Trace()
		if ok != trace || len(gotTrace.Rules()) != 0 {
			t.Fatal("empty trace differs from option")
		}
	}
	first := rulite.NewRule[executionInput]("first").When(constantCondition(true, nil)).Then(successfulAction)
	second := rulite.NewRule[executionInput]("second").When(constantCondition(true, nil)).Then(successfulAction)
	engine := mustEngine(t, first, second)
	for _, last := range []rulite.StopMode{rulite.EvaluateAll, rulite.StopOnFirstMatch, rulite.StopOnFirstFire} {
		result, err := engine.Fire(context.Background(), input,
			rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)),
			rulite.WithPolicy(rulite.DefaultPolicy().WithStop(last)), rulite.FireOption{}, rulite.WithTrace(), rulite.WithTrace())
		wantCount := 1
		if last == rulite.EvaluateAll {
			wantCount = 2
		}
		if err != nil || result.Counts().Fired != wantCount {
			t.Fatalf("last policy did not win: %+v, %v", result.Counts(), err)
		}
		if _, ok := result.Trace(); !ok {
			t.Fatal("trace was disabled by a zero option")
		}
	}
}

func TestFirePolicyMatrix(t *testing.T) {
	conditionCause := errors.New("eligibility unavailable")
	actionCause := errors.New("provider unavailable")
	for _, stop := range []rulite.StopMode{rulite.EvaluateAll, rulite.StopOnFirstMatch, rulite.StopOnFirstFire} {
		for _, conditionMode := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
			for _, actionMode := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
				for _, conditionFails := range []bool{false, true} {
					t.Run(fmt.Sprintf("stop_%d_condition_%d_action_%d_condition_failure_%t", stop, conditionMode, actionMode, conditionFails), func(t *testing.T) {
						var calls []string
						var rules []rulite.Rule[executionInput]
						ids := []rulite.RuleID{"miss", "eligibility", "primary", "backup", "tail"}
						for index, id := range ids {
							rules = append(rules, rulite.NewRule[executionInput](id).When(func(_ context.Context, input *executionInput) (bool, error) {
								calls = append(calls, string(id)+":condition")
								if index == 1 && conditionFails {
									return true, conditionCause
								}
								if index == 3 && input.value != 10 {
									t.Fatal("partial action mutation is not visible to fallback")
								}
								return index >= 2, nil
							}).Then(func(_ context.Context, input *executionInput) error {
								calls = append(calls, string(id)+":action")
								if index == 2 {
									input.value = 10
									return actionCause
								}
								input.value++
								return nil
							}))
						}
						policy := rulite.DefaultPolicy().WithStop(stop).WithConditionErrors(conditionMode).WithActionErrors(actionMode)
						result, err := mustEngine(t, rules...).Fire(context.Background(), &executionInput{}, rulite.WithPolicy(policy))
						wantCalls := []string{"miss:condition", "eligibility:condition"}
						wantStates := []rulite.RuleState{rulite.RuleUnmatched, rulite.RuleUnmatched, rulite.RuleNotEvaluated, rulite.RuleNotEvaluated, rulite.RuleNotEvaluated}
						wantStop := rulite.StopConditionError
						if conditionFails {
							wantStates[1] = rulite.RuleFailed
						}
						if !conditionFails || conditionMode == rulite.ContinueOnError {
							wantCalls = append(wantCalls, "primary:condition", "primary:action")
							wantStates[2] = rulite.RuleFailed
							wantStop = rulite.StopActionError
							if actionMode == rulite.ContinueOnError {
								wantStop = rulite.StopFirstMatch
								if stop != rulite.StopOnFirstMatch {
									wantCalls = append(wantCalls, "backup:condition", "backup:action")
									wantStates[3] = rulite.RuleFired
									wantStop = rulite.StopFirstFire
									if stop == rulite.EvaluateAll {
										wantCalls = append(wantCalls, "tail:condition", "tail:action")
										wantStates[4] = rulite.RuleFired
										wantStop = rulite.StopCompleted
									}
								}
							}
						}
						if !slices.Equal(calls, wantCalls) || result.StopReason() != wantStop {
							t.Fatalf("calls = %v, stop = %v; want %v, %v", calls, result.StopReason(), wantCalls, wantStop)
						}
						for index, id := range ids {
							x, ok := result.Rule(id)
							if !ok || x.State() != wantStates[index] {
								t.Fatalf("rule %s state = %v; want %v", id, x.State(), wantStates[index])
							}
						}
						var aggregate *rulite.ExecutionError
						if !errors.As(err, &aggregate) {
							t.Fatalf("execution error = %T, %v", err, err)
						}
						if conditionFails && !errors.Is(err, conditionCause) {
							t.Fatal("condition cause lost")
						}
						if wantStates[2] == rulite.RuleFailed && !errors.Is(err, actionCause) {
							t.Fatal("action cause lost")
						}
						for _, failure := range result.Failures() {
							mode := conditionMode
							if failure.Phase() == rulite.ActionPhase {
								mode = actionMode
							}
							if failure.Continued() != (mode == rulite.ContinueOnError) {
								t.Fatal("incorrect continued disposition")
							}
						}
						checkResultConsistency(t, result)
					})
				}
			}
		}
	}
}

func TestConditionErrorContinuesToCompletion(t *testing.T) {
	cause := errors.New("eligibility unavailable")
	engine := mustEngine(t, rulite.NewRule[executionInput]("eligibility").When(constantCondition(true, cause)).Then(func(context.Context, *executionInput) error {
		t.Fatal("true with error must not match")
		return nil
	}))
	result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError)))
	if !errors.Is(err, cause) || result.StopReason() != rulite.StopCompleted || result.Stopped() || result.Counts().Matched != 0 || !result.Failures()[0].Continued() {
		t.Fatalf("continued condition failure = %+v, %v, %v", result.Counts(), result.StopReason(), err)
	}
}

func TestCompiledExecutionOrderAndMutation(t *testing.T) {
	ids := []rulite.RuleID{"middle", "high/first", "low", "high/second"}
	priorities := []rulite.Priority{0, 100, -10, 100}
	var conditions []string
	var rules []rulite.Rule[executionInput]
	for index, id := range ids {
		rules = append(rules, rulite.NewRule[executionInput](id).Priority(priorities[index]).When(func(_ context.Context, input *executionInput) (bool, error) {
			conditions = append(conditions, fmt.Sprintf("%s:%d", id, input.value))
			return true, nil
		}).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil }))
	}
	result, err := mustEngine(t, rules...).Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	if err != nil || !slices.Equal(conditions, []string{"high/first:0", "high/second:1", "middle:2", "low:3"}) {
		t.Fatalf("interleaved order = %v, %v", conditions, err)
	}
	for order, index := range []int{1, 3, 0, 2} {
		x, ok := result.Rule(ids[index])
		if !ok || x.Order() != order || x.RegistrationIndex() != index || x.Priority() != priorities[index] {
			t.Fatal("compiled metadata differs from registration")
		}
	}
	checkResultConsistency(t, result)
}

func checkResultConsistency(t testing.TB, result rulite.Result) {
	t.Helper()
	checkGroupViews(t, result)
	c := result.Counts()
	if c.Total != c.Evaluated+c.NotEvaluated || c.Evaluated != c.Unmatched+c.Fired+c.Skipped+c.ConditionFailed+c.ActionFailed || c.Matched != c.Fired+c.Skipped+c.ActionFailed || c.Failed != c.ConditionFailed+c.ActionFailed || c.PanicRecovered > c.Failed {
		t.Fatalf("count invariants failed: %+v", c)
	}
	var counted rulite.Counts
	var matched, fired []rulite.RuleID
	var failures []rulite.Failure
	for order, x := range result.Explain().Rules() {
		fromResult, ok := result.Rule(x.ID())
		if !ok || !reflect.DeepEqual(fromResult, x) || x.Order() != order {
			t.Fatal("explanation differs from Result.Rule")
		}
		counted.Total++
		if x.Evaluated() {
			counted.Evaluated++
		} else {
			counted.NotEvaluated++
		}
		if x.Matched() {
			counted.Matched++
			matched = append(matched, x.ID())
		}
		if x.Fired() {
			counted.Fired++
			fired = append(fired, x.ID())
		}
		if x.ActionStarted() && !x.Matched() || x.Fired() && !x.ActionStarted() {
			t.Fatal("invalid action state")
		}
		switch x.State() {
		case rulite.RuleUnmatched:
			counted.Unmatched++
		case rulite.RuleSkipped:
			counted.Skipped++
			if x.SkipReason() != rulite.SkipContextDone || x.ActionStarted() {
				t.Fatal("invalid skipped state")
			}
		case rulite.RuleNotEvaluated:
			if (x.NotEvaluatedReason() != rulite.NotEvaluatedExecutionStopped && x.NotEvaluatedReason() != rulite.NotEvaluatedGroupResolved) || x.Matched() {
				t.Fatal("invalid not-evaluated state")
			}
		case rulite.RuleFailed:
			counted.Failed++
			failure, ok := x.Error().(rulite.Failure)
			if !ok || failure.RuleID() != x.ID() {
				t.Fatal("failed state lacks canonical Failure")
			}
			failures = append(failures, failure)
			if failure.Phase() == rulite.ConditionPhase {
				counted.ConditionFailed++
				if x.Matched() {
					t.Fatal("condition failure matched")
				}
			} else {
				counted.ActionFailed++
			}
			var panicErr *rulite.PanicError
			if errors.As(failure, &panicErr) {
				counted.PanicRecovered++
			}
		}
		if x.State() != rulite.RuleFailed && x.Error() != nil {
			t.Fatal("non-failed state has an error")
		}
		if x.State() != rulite.RuleSkipped && x.SkipReason() != rulite.NotSkipped {
			t.Fatal("non-skipped state has a skip reason")
		}
		if x.Evaluated() && x.NotEvaluatedReason() != rulite.NotEvaluatedNone {
			t.Fatal("evaluated state has a non-evaluation reason")
		}
	}
	if counted != c || result.Evaluated() != c.Evaluated || !slices.Equal(matched, result.Matched()) || !slices.Equal(fired, result.Fired()) || !reflect.DeepEqual(failures, result.Failures()) {
		t.Fatalf("ledger queries differ: counted %+v, summary %+v", counted, c)
	}
	if trace, ok := result.Trace(); ok {
		if trace.StopReason() != result.StopReason() || len(trace.Rules()) != c.Total || trace.Duration() < 0 {
			t.Fatal("trace summary differs")
		}
		for _, tr := range trace.Rules() {
			x, _ := result.Rule(tr.ID())
			group, grouped := tr.GroupID()
			xGroup, xGrouped := x.GroupID()
			member, memberOK := tr.MemberIndex()
			xMember, xMemberOK := x.MemberIndex()
			if group != xGroup || grouped != xGrouped || member != xMember || memberOK != xMemberOK || tr.TopLevelOrder() != x.TopLevelOrder() {
				t.Fatal("trace lost group coordinates")
			}
			if tr.Order() != x.Order() || tr.RegistrationIndex() != x.RegistrationIndex() || tr.Priority() != x.Priority() || tr.State() != x.State() || tr.Evaluated() != x.Evaluated() || tr.Matched() != x.Matched() || tr.ActionStarted() != x.ActionStarted() || tr.Fired() != x.Fired() || tr.SkipReason() != x.SkipReason() || tr.NotEvaluatedReason() != x.NotEvaluatedReason() || !reflect.DeepEqual(tr.Error(), x.Error()) {
				t.Fatal("trace rule differs from canonical ledger")
			}
			if tr.ConditionDuration() < 0 || tr.ActionDuration() < 0 || !tr.Evaluated() && tr.ConditionDuration() != 0 || !tr.ActionStarted() && tr.ActionDuration() != 0 {
				t.Fatal("trace has invalid durations")
			}
		}
	}
}
