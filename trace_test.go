package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/imbrooklyn/rulite"
)

type expectedCondition struct {
	kind     rulite.ConditionKind
	index    int
	outcome  rulite.ConditionOutcome
	err      error
	reason   rulite.ConditionNotEvaluatedReason
	children []expectedCondition
}

func checkConditionTree(t *testing.T, got rulite.ConditionTrace, want expectedCondition) {
	t.Helper()
	if got.Kind() != want.kind || got.Index() != want.index || got.Outcome() != want.outcome || got.Error() != want.err || got.NotEvaluatedReason() != want.reason {
		t.Fatalf("tree node kind=%d index=%d outcome=%d error=%v reason=%d; want %+v", got.Kind(), got.Index(), got.Outcome(), got.Error(), got.NotEvaluatedReason(), want)
	}
	children := got.Children()
	if len(children) != len(want.children) {
		t.Fatalf("tree has %d children; want %d", len(children), len(want.children))
	}
	for index, child := range children {
		checkConditionTree(t, child, want.children[index])
	}
}

func TestTraceCombinatorTree(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprintf("child_error_%t", fails), func(t *testing.T) {
			var cause error
			if fails {
				cause = errors.New("eligibility unavailable")
			}
			var calls []string
			leaf := func(name string, value bool, err error) rulite.Condition[executionInput] {
				return func(context.Context, *executionInput) (bool, error) { calls = append(calls, name); return value, err }
			}
			forbidden := func(context.Context, *executionInput) (bool, error) {
				t.Fatal("short-circuited child called")
				return false, nil
			}
			condition := rulite.All(leaf("start", true, nil), rulite.Any(
				leaf("miss", false, nil), rulite.Not(rulite.All(leaf("inverted", false, cause), forbidden)), forbidden,
			), leaf("finish", true, nil))
			engine := mustEngine(t, rulite.NewRule[executionInput]("eligibility").When(condition).Then(successfulAction))
			var baseline rulite.Result
			var baselineCalls []string
			for _, traced := range []bool{false, true} {
				calls = nil
				var options []rulite.FireOption
				if traced {
					options = append(options, rulite.WithTrace())
				}
				result, err := engine.Fire(context.Background(), &executionInput{}, options...)
				if !errors.Is(err, cause) {
					t.Fatalf("tree changed cause: %v", err)
				}
				trace, ok := result.Trace()
				if ok != traced {
					t.Fatal("trace option differs")
				}
				if !traced {
					baseline = result
					baselineCalls = slices.Clone(calls)
					continue
				}
				if !slices.Equal(calls, baselineCalls) || !reflect.DeepEqual(result.Explain().Rules(), baseline.Explain().Rules()) || result.Counts() != baseline.Counts() || result.Explain().String() != baseline.Explain().String() {
					t.Fatal("trace changed execution semantics")
				}
				root, ok := trace.Rules()[0].ConditionTree()
				if !ok {
					t.Fatal("combinator tree missing")
				}
				yes, no, skipped := rulite.ConditionOutcomeTrue, rulite.ConditionOutcomeFalse, rulite.ConditionOutcomeNotEvaluated
				short := func(index int) expectedCondition {
					return expectedCondition{index: index, outcome: skipped, reason: rulite.ConditionNotEvaluatedShortCircuit}
				}
				want := expectedCondition{kind: rulite.ConditionAll, index: -1, outcome: yes, children: []expectedCondition{
					{index: 0, outcome: yes},
					{kind: rulite.ConditionAny, index: 1, outcome: yes, children: []expectedCondition{
						{index: 0, outcome: no},
						{kind: rulite.ConditionNot, index: 1, outcome: yes, children: []expectedCondition{
							{kind: rulite.ConditionAll, index: 0, outcome: no, children: []expectedCondition{{index: 0, outcome: no}, short(1)}},
						}}, short(2),
					}}, {index: 2, outcome: yes},
				}}
				if fails {
					for _, node := range []*expectedCondition{&want, &want.children[1], &want.children[1].children[1], &want.children[1].children[1].children[0], &want.children[1].children[1].children[0].children[0]} {
						node.outcome = rulite.ConditionOutcomeError
						node.err = cause
					}
					want.children[2] = short(2)
				}
				checkConditionTree(t, root, want)
				children := root.Children()
				clear(children)
				checkConditionTree(t, root, want)
				checkResultConsistency(t, result)
			}
		})
	}
}

func TestTraceCombinatorErrorFirstAndEmpty(t *testing.T) {
	cause := errors.New("eligibility unavailable")
	for name, combine := range map[string]func(...rulite.Condition[executionInput]) rulite.Condition[executionInput]{"all": rulite.All[executionInput], "any": rulite.Any[executionInput]} {
		for _, nilChild := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_nil_%t", name, nilChild), func(t *testing.T) {
				child := constantCondition(true, cause)
				want := cause
				if nilChild {
					child = nil
					want = rulite.ErrInvalidCondition
				}
				engine := mustEngine(t, rulite.NewRule[executionInput]("error").When(combine(child, constantCondition(true, nil))).Then(successfulAction))
				result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
				if !errors.Is(err, want) || result.Counts().Matched != 0 {
					t.Fatal("child bool took precedence over error")
				}
				trace, _ := result.Trace()
				root, _ := trace.Rules()[0].ConditionTree()
				children := root.Children()
				if root.Outcome() != rulite.ConditionOutcomeError || children[0].Outcome() != rulite.ConditionOutcomeError || children[0].Error() != want || children[1].NotEvaluatedReason() != rulite.ConditionNotEvaluatedShortCircuit {
					t.Fatal("error tree differs")
				}
			})
		}
	}
	cases := []struct {
		name      string
		condition rulite.Condition[executionInput]
		outcome   rulite.ConditionOutcome
		kind      rulite.ConditionKind
		cause     error
	}{
		{"empty_all", rulite.All[executionInput](), rulite.ConditionOutcomeTrue, rulite.ConditionAll, nil},
		{"empty_any", rulite.Any[executionInput](), rulite.ConditionOutcomeFalse, rulite.ConditionAny, nil},
		{"nil_not", rulite.Not[executionInput](nil), rulite.ConditionOutcomeError, rulite.ConditionNot, rulite.ErrInvalidCondition},
		{"error_not", rulite.Not(constantCondition(true, cause)), rulite.ConditionOutcomeError, rulite.ConditionNot, cause},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := mustEngine(t, rulite.NewRule[executionInput]("condition").When(tc.condition).Then(successfulAction)).Fire(context.Background(), &executionInput{}, rulite.WithTrace())
			if !errors.Is(err, tc.cause) {
				t.Fatalf("condition error = %v", err)
			}
			trace, _ := result.Trace()
			root, ok := trace.Rules()[0].ConditionTree()
			if !ok || root.Kind() != tc.kind || root.Outcome() != tc.outcome || root.Index() != -1 {
				t.Fatal("empty or unary trace differs")
			}
		})
	}
}

func TestTracePanicTree(t *testing.T) {
	condition := rulite.All(constantCondition(true, nil), rulite.Not(rulite.Any(
		func(context.Context, *executionInput) (bool, error) { panic("condition failed") }, constantCondition(false, nil),
	)), constantCondition(true, nil))
	result, err := mustEngine(t, rulite.NewRule[executionInput]("panic").When(condition).Then(successfulAction)).Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	var panicErr *rulite.PanicError
	if !errors.As(err, &panicErr) {
		t.Fatal("missing panic")
	}
	trace, _ := result.Trace()
	root, _ := trace.Rules()[0].ConditionTree()
	not := root.Children()[1]
	anyNode := not.Children()[0]
	leaf := anyNode.Children()[0]
	for _, node := range []rulite.ConditionTrace{root, not, anyNode, leaf} {
		if node.Outcome() != rulite.ConditionOutcomeError || node.Error() != panicErr || node.NotEvaluatedReason() != rulite.ConditionNotEvaluatedNone {
			t.Fatal("panic path not preserved")
		}
	}
	for _, node := range []rulite.ConditionTrace{root.Children()[2], anyNode.Children()[1]} {
		if node.Outcome() != rulite.ConditionOutcomeNotEvaluated || node.NotEvaluatedReason() != rulite.ConditionNotEvaluatedShortCircuit {
			t.Fatal("panic suffix not marked")
		}
	}
}

func TestTraceContextSemanticsAndIsolation(t *testing.T) {
	type requestKey struct{}
	cause := errors.New("request withdrawn")
	base := context.WithValue(context.Background(), requestKey{}, "request-123")
	deadlineCtx, release := context.WithDeadline(base, time.Now().Add(time.Hour))
	defer release()
	ctx, cancel := context.WithCancelCause(deadlineCtx)
	defer cancel(nil)
	checkContext := func(got context.Context) {
		if got.Value(requestKey{}) != ctx.Value(requestKey{}) || got.Done() != ctx.Done() || got.Err() != ctx.Err() || context.Cause(got) != context.Cause(ctx) {
			t.Fatal("derived context changed caller semantics")
		}
		gotDeadline, gotOK := got.Deadline()
		deadline, ok := ctx.Deadline()
		if gotDeadline != deadline || gotOK != ok {
			t.Fatal("derived context changed deadline")
		}
	}
	var retained context.Context
	leaf := func(got context.Context, _ *executionInput) (bool, error) {
		checkContext(got)
		retained = got
		return true, nil
	}
	engine := mustEngine(t, rulite.NewRule[executionInput]("context").When(rulite.All(leaf)).Then(func(got context.Context, _ *executionInput) error { checkContext(got); return nil }))
	result, err := engine.Fire(ctx, &executionInput{}, rulite.WithTrace())
	if err != nil {
		t.Fatal(err)
	}
	trace, _ := result.Trace()
	root, _ := trace.Rules()[0].ConditionTree()
	before := root.Children()
	// A retained callback context must not retain a mutable path into the result.
	_, _ = rulite.Any[executionInput]()(retained, &executionInput{})
	if !reflect.DeepEqual(root.Children(), before) {
		t.Fatal("retained recorder changed an immutable trace")
	}
	cancel(cause)
	checkContext(retained)
}

func TestNestedFireHasIndependentRecorder(t *testing.T) {
	for _, innerTrace := range []bool{false, true} {
		inner := mustEngine(t, rulite.NewRule[executionInput]("inner").When(rulite.Any(constantCondition(true, nil))).Then(successfulAction))
		leaf := func(ctx context.Context, input *executionInput) (bool, error) {
			var options []rulite.FireOption
			if innerTrace {
				options = append(options, rulite.WithTrace())
			}
			result, err := inner.Fire(ctx, input, options...)
			if _, ok := result.Trace(); ok != innerTrace {
				t.Fatal("inner Fire inherited tracing")
			}
			return true, err
		}
		outer := mustEngine(t, rulite.NewRule[executionInput]("outer").When(rulite.All(leaf)).Then(successfulAction))
		result, err := outer.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
		if err != nil {
			t.Fatal(err)
		}
		trace, _ := result.Trace()
		root, _ := trace.Rules()[0].ConditionTree()
		if root.Kind() != rulite.ConditionAll || root.Children()[0].Kind() != rulite.ConditionLeaf {
			t.Fatal("inner execution contaminated the outer tree")
		}
	}
}

func TestTraceDurationsAndPlainCondition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine := mustEngine(t, rulite.NewRule[executionInput]("timed").When(func(context.Context, *executionInput) (bool, error) { time.Sleep(3 * time.Second); return true, nil }).Then(func(context.Context, *executionInput) error { time.Sleep(2 * time.Second); return nil }))
		result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
		if err != nil {
			t.Fatal(err)
		}
		trace, _ := result.Trace()
		rule := trace.Rules()[0]
		if trace.Duration() != 5*time.Second || rule.ConditionDuration() != 3*time.Second || rule.ActionDuration() != 2*time.Second {
			t.Fatal("callback durations differ from elapsed time")
		}
		if _, ok := rule.ConditionTree(); ok {
			t.Fatal("plain condition invented a tree")
		}
	})
}

func TestTraceOpaqueCallbackControlFlow(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		wrapped := func(ctx context.Context, input *executionInput) (bool, error) {
			_, err := rulite.All(constantCondition(true, nil))(ctx, input)
			if multiple {
				return rulite.Any[executionInput]()(ctx, input)
			}
			return false, err
		}
		for _, nested := range []bool{false, true} {
			condition := rulite.Condition[executionInput](wrapped)
			if nested {
				condition = rulite.Not(condition)
			}
			result, err := mustEngine(t, rulite.NewRule[executionInput]("opaque").When(condition).Then(successfulAction)).Fire(context.Background(), &executionInput{}, rulite.WithTrace())
			if err != nil {
				t.Fatal(err)
			}
			trace, _ := result.Trace()
			root, ok := trace.Rules()[0].ConditionTree()
			if ok != nested {
				t.Fatal("opaque callback claimed an operator tree")
			}
			if nested {
				child := root.Children()[0]
				if child.Kind() != rulite.ConditionLeaf || child.Outcome() != rulite.ConditionOutcomeFalse || len(child.Children()) != 0 {
					t.Fatal("opaque child claimed combinator semantics")
				}
			}
		}
	}
}

func TestTraceIsComplete(t *testing.T) {
	const size = 1200
	children := make([]rulite.Condition[executionInput], size)
	for index := range children {
		children[index] = constantCondition(true, nil)
	}
	rules := make([]rulite.Rule[executionInput], size)
	for index := range rules {
		rules[index] = rulite.NewRule[executionInput](rulite.RuleID(fmt.Sprintf("rule/%d", index))).When(rulite.Any(children...)).Then(successfulAction)
	}
	result, err := mustEngine(t, rules...).Fire(context.Background(), &executionInput{}, rulite.WithTrace(), rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)))
	if err != nil {
		t.Fatal(err)
	}
	trace, _ := result.Trace()
	views := trace.Rules()
	root, _ := views[0].ConditionTree()
	if len(views) != size || len(root.Children()) != size || root.Children()[size-1].NotEvaluatedReason() != rulite.ConditionNotEvaluatedShortCircuit {
		t.Fatal("trace was truncated")
	}
	if views[size-1].State() != rulite.RuleNotEvaluated {
		t.Fatal("unevaluated trace suffix missing")
	}
	clear(views)
	if trace.Rules()[0].ID() != "rule/0" {
		t.Fatal("Trace.Rules exposes its slice")
	}
	checkResultConsistency(t, result)
}
