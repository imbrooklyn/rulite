package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func FuzzRuleValidation(f *testing.F) {
	for _, id := range []string{"", "pricing/vip", "A", "/invalid", strings.Repeat("a", 128), strings.Repeat("a", 129), "bad\xff"} {
		f.Add(id, byte(0))
	}
	f.Add("pricing/vip", byte(3))
	syntax := regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
	f.Fuzz(func(t *testing.T, id string, flags byte) {
		if len(id) > 256 {
			id = id[:256]
		}
		condition := rulite.Condition[pricingState](eligible)
		action := rulite.Action[pricingState](applyDiscount)
		if flags&1 != 0 {
			condition = nil
		}
		if flags&2 != 0 {
			action = nil
		}
		rule := rulite.NewRule[pricingState](rulite.RuleID(id)).When(condition).Then(action)
		valid := syntax.MatchString(id)
		var want []error
		for index := range 2 {
			if !valid {
				want = append(want, rulite.ErrInvalidRule)
			} else if index == 1 {
				want = append(want, rulite.ErrDuplicateRuleID)
			}
			if condition == nil {
				want = append(want, rulite.ErrInvalidCondition)
			}
			if action == nil {
				want = append(want, rulite.ErrInvalidRule)
			}
		}
		engine, err := rulite.NewEngine(rule, rule)
		var validation *rulite.ValidationError
		if engine != nil || !errors.As(err, &validation) {
			t.Fatalf("expected validation error, got %v", err)
		}
		issues := validation.Issues()
		if len(issues) != len(want) {
			t.Fatalf("issues=%v; want %d", issues, len(want))
		}
		previous := -1
		for i, issue := range issues {
			gotID, ok := issue.RuleID()
			if !errors.Is(issue, want[i]) || ok != valid || ok && gotID != rulite.RuleID(id) || !ok && gotID != "" || issue.Index() < previous || issue.Index() > 1 {
				t.Fatalf("invalid issue %d: %v", i, issue)
			}
			previous = issue.Index()
		}
		clear(issues)
		clear(validation.Unwrap())
		if len(validation.Issues()) != len(want) || !errors.Is(validation, want[0]) {
			t.Fatal("validation copy changed error tree")
		}
		single, singleErr := rulite.NewEngine(rule)
		if (singleErr == nil) != (valid && condition != nil && action != nil) || (single != nil) != (singleErr == nil) {
			t.Fatalf("single rule validation disagrees with syntax: %v", singleErr)
		}
	})
}

type conditionSpec struct {
	kind     byte
	index    int
	children []conditionSpec
}

// Prefix encoding consumes at most 128 bytes, with depth at most eight and
// fan-out at most three. Leaves include both error booleans and nil callbacks.
func decodeCondition(data []byte, cursor *int, depth int) conditionSpec {
	node := conditionSpec{index: *cursor}
	if *cursor >= len(data) {
		return node
	}
	value := data[*cursor]
	*cursor++
	node.kind = value % 8
	if depth >= 8 {
		node.kind %= 5
	}
	if node.kind < 5 {
		return node
	}
	count := int(value/8) % 4
	if node.kind == 7 {
		count = 1
	}
	for range count {
		node.children = append(node.children, decodeCondition(data, cursor, depth+1))
	}
	return node
}

func (n conditionSpec) build(cause error, calls *[]int) rulite.Condition[executionInput] {
	if n.kind == 4 {
		return nil
	}
	if n.kind < 4 {
		return func(context.Context, *executionInput) (bool, error) {
			*calls = append(*calls, n.index)
			if n.kind >= 2 {
				return n.kind == 3, cause
			}
			return n.kind == 1, nil
		}
	}
	children := make([]rulite.Condition[executionInput], len(n.children))
	for i, child := range n.children {
		children[i] = child.build(cause, calls)
	}
	switch n.kind {
	case 5:
		return rulite.All(children...)
	case 6:
		return rulite.Any(children...)
	default:
		return rulite.Not(children[0])
	}
}

func (n conditionSpec) evaluate(cause error, calls *[]int) (bool, error) {
	if n.kind == 4 {
		return false, rulite.ErrInvalidCondition
	}
	if n.kind < 4 {
		*calls = append(*calls, n.index)
		if n.kind >= 2 {
			return false, cause
		}
		return n.kind == 1, nil
	}
	value := n.kind == 5
	for _, child := range n.children {
		got, err := child.evaluate(cause, calls)
		if err != nil {
			return false, err
		}
		switch n.kind {
		case 5:
			value = value && got
		case 6:
			value = value || got
		case 7:
			return !got, nil
		}
		if n.kind == 5 && !value || n.kind == 6 && value {
			break
		}
	}
	return value, nil
}

func FuzzCombinatorNesting(f *testing.F) {
	for _, seed := range [][]byte{{}, {5}, {6}, {7, 4}, {21, 1, 22, 0, 7, 0}, {29, 1, 3, 4}, {30, 0, 1, 4}, {7, 7, 7, 1}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 128 {
			data = data[:128]
		}
		cursor := 0
		// A root All ensures nil leaves are evaluated through the public combinator boundary.
		spec := conditionSpec{kind: 5, index: -1, children: []conditionSpec{decodeCondition(data, &cursor, 0)}}
		cause := errors.New("eligibility unavailable")
		var wantCalls, calls []int
		want, wantErr := spec.evaluate(cause, &wantCalls)
		engine := mustEngine(t, rulite.NewRule[executionInput]("condition").When(spec.build(cause, &calls)).Then(successfulAction))
		for _, traced := range []bool{false, true} {
			calls = nil
			var options []rulite.FireOption
			if traced {
				options = append(options, rulite.WithTrace())
			}
			result, err := engine.Fire(context.Background(), &executionInput{}, options...)
			if (result.Counts().Matched == 1) != want || !errors.Is(err, wantErr) || !slices.Equal(calls, wantCalls) {
				t.Fatalf("result=%+v error=%v calls=%v; want %t %v %v", result.Counts(), err, calls, want, wantErr, wantCalls)
			}
			checkResultConsistency(t, result)
		}
	})
}

type treeCause struct{ index int }

func (e *treeCause) Error() string { return fmt.Sprintf("provider %d unavailable", e.index) }

func FuzzExecutionErrorTree(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3}, byte(0))
	f.Add([]byte{3, 2, 1}, byte(3))
	f.Add([]byte{}, byte(1))
	f.Fuzz(func(t *testing.T, data []byte, flags byte) {
		if len(data) > 24 {
			data = data[:24]
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		cancellation := &treeCause{index: -1}
		leaves := make([]*treeCause, len(data))
		causes := make([]error, len(data))
		var rules []rulite.Rule[executionInput]
		for i, encoded := range data {
			leaves[i] = &treeCause{index: i}
			var cause error = leaves[i]
			for depth := byte(0); depth < encoded%5; depth++ {
				if depth%2 == 0 {
					cause = fmt.Errorf("provider attempt: %w", cause)
				} else {
					cause = errors.Join(errors.New("route unavailable"), cause)
				}
			}
			causes[i] = cause
			condition := constantCondition(true, nil)
			action := rulite.Action[executionInput](successfulAction)
			fail := func() error {
				if i == len(data)-1 && flags&1 != 0 {
					cancel(cancellation)
				}
				return cause
			}
			if encoded&1 == 0 {
				condition = func(context.Context, *executionInput) (bool, error) { return true, fail() }
			} else {
				action = func(context.Context, *executionInput) error { return fail() }
			}
			rules = append(rules, rulite.NewRule[executionInput](rulite.RuleID(fmt.Sprintf("provider/%d", i))).When(condition).Then(action))
		}
		result, err := mustEngine(t, rules...).Fire(ctx, &executionInput{}, rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)))
		checkResultConsistency(t, result)
		if len(data) == 0 {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		var aggregate *rulite.ExecutionError
		var first *treeCause
		if !errors.As(err, &aggregate) || !errors.As(err, &first) || first != leaves[0] {
			t.Fatal("outer error lost typed cause")
		}
		for i, failure := range result.Failures() {
			if failure.Cause() != causes[i] || !errors.Is(err, leaves[i]) || !failure.Continued() {
				t.Fatal("error tree lost original cause or disposition")
			}
			var fromRule rulite.Failure
			view, _ := result.Rule(failure.RuleID())
			if !errors.As(view.Error(), &fromRule) || !reflect.DeepEqual(fromRule, failure) {
				t.Fatal("rule error differs from canonical failure")
			}
		}
		wantSize := len(data)
		if flags&1 != 0 {
			wantSize += 2
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cancellation) || result.StopReason() != rulite.StopContextCanceled {
				t.Fatal("context causes lost")
			}
		} else if result.StopReason() != rulite.StopCompleted {
			t.Fatal("continued errors did not complete")
		}
		wrapped := aggregate.Unwrap()
		if len(wrapped) != wantSize || !reflect.DeepEqual(aggregate.Failures(), result.Failures()) {
			t.Fatal("aggregate differs from ledger")
		}
		for i, failure := range result.Failures() {
			if !reflect.DeepEqual(wrapped[i], failure) {
				t.Fatal("unwrap observation order differs")
			}
		}
		if flags&1 != 0 && (wrapped[len(data)] != context.Canceled || wrapped[len(data)+1] != cancellation) {
			t.Fatal("context unwrap order differs")
		}
		clear(wrapped)
		clear(aggregate.Failures())
		if !errors.Is(err, leaves[0]) {
			t.Fatal("accessor mutation changed error tree")
		}
	})
}
