package rulite_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func TestConditionCombinators(t *testing.T) {
	failure := errors.New("eligibility unavailable")
	type outcome struct {
		matched bool
		err     error
		nil     bool
	}
	truth := outcome{matched: true}
	falsehood := outcome{}
	trueError := outcome{matched: true, err: failure}
	falseError := outcome{err: failure}
	nilChild := outcome{nil: true}
	cases := []struct {
		name     string
		combine  func(...rulite.Condition[pricingState]) rulite.Condition[pricingState]
		children []outcome
		want     bool
		wantErr  error
		calls    []int
	}{
		{"all_empty", rulite.All[pricingState], nil, true, nil, nil},
		{"all_true", rulite.All[pricingState], []outcome{truth, truth, truth}, true, nil, []int{0, 1, 2}},
		{"all_false_first", rulite.All[pricingState], []outcome{falsehood, truth}, false, nil, []int{0}},
		{"all_false_middle", rulite.All[pricingState], []outcome{truth, falsehood, truth}, false, nil, []int{0, 1}},
		{"all_false_last", rulite.All[pricingState], []outcome{truth, truth, falsehood}, false, nil, []int{0, 1, 2}},
		{"all_error_true", rulite.All[pricingState], []outcome{truth, trueError, truth}, false, failure, []int{0, 1}},
		{"all_error_false", rulite.All[pricingState], []outcome{truth, falseError, truth}, false, failure, []int{0, 1}},
		{"all_nil_first", rulite.All[pricingState], []outcome{nilChild, truth}, false, rulite.ErrInvalidCondition, nil},
		{"all_nil_middle", rulite.All[pricingState], []outcome{truth, nilChild, truth}, false, rulite.ErrInvalidCondition, []int{0}},
		{"all_nil_after_stop", rulite.All[pricingState], []outcome{falsehood, nilChild}, false, nil, []int{0}},
		{"any_empty", rulite.Any[pricingState], nil, false, nil, nil},
		{"any_false", rulite.Any[pricingState], []outcome{falsehood, falsehood, falsehood}, false, nil, []int{0, 1, 2}},
		{"any_true_first", rulite.Any[pricingState], []outcome{truth, falsehood}, true, nil, []int{0}},
		{"any_true_middle", rulite.Any[pricingState], []outcome{falsehood, truth, falsehood}, true, nil, []int{0, 1}},
		{"any_true_last", rulite.Any[pricingState], []outcome{falsehood, falsehood, truth}, true, nil, []int{0, 1, 2}},
		{"any_error_true", rulite.Any[pricingState], []outcome{falsehood, trueError, truth}, false, failure, []int{0, 1}},
		{"any_error_false", rulite.Any[pricingState], []outcome{falsehood, falseError, truth}, false, failure, []int{0, 1}},
		{"any_nil_first", rulite.Any[pricingState], []outcome{nilChild, falsehood}, false, rulite.ErrInvalidCondition, nil},
		{"any_nil_middle", rulite.Any[pricingState], []outcome{falsehood, nilChild, truth}, false, rulite.ErrInvalidCondition, []int{0}},
		{"any_nil_after_stop", rulite.Any[pricingState], []outcome{truth, nilChild}, true, nil, []int{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := &pricingState{total: 150}
			var calls []int
			children := make([]rulite.Condition[pricingState], len(tc.children))
			for index, result := range tc.children {
				if result.nil {
					continue
				}
				children[index] = func(gotContext context.Context, gotInput *pricingState) (bool, error) {
					if len(calls) >= len(tc.calls) || index != tc.calls[len(calls)] {
						t.Fatalf("unexpected child %d called after %v", index, calls)
					}
					if gotContext.Done() != ctx.Done() || gotContext.Err() != ctx.Err() || gotInput != input {
						t.Fatal("combinator changed context semantics or input")
					}
					calls = append(calls, index)
					return result.matched, result.err
				}
			}
			combined := tc.combine(children...)
			if len(calls) != 0 {
				t.Fatal("composition invoked a child")
			}
			for attempt := 0; attempt < 2; attempt++ {
				calls = nil
				got, err := combined(ctx, input)
				if got != tc.want || err != tc.wantErr {
					t.Fatalf("condition = %t, %v; want %t, %v", got, err, tc.want, tc.wantErr)
				}
				if !slices.Equal(calls, tc.calls) {
					t.Fatalf("calls = %v; want %v", calls, tc.calls)
				}
			}
		})
	}
}

func TestNot(t *testing.T) {
	failure := errors.New("eligibility unavailable")
	cases := []struct {
		name     string
		matched  bool
		err      error
		nilChild bool
		want     bool
		wantErr  error
	}{
		{"true", true, nil, false, false, nil},
		{"false", false, nil, false, true, nil},
		{"error_true", true, failure, false, false, failure},
		{"error_false", false, failure, false, false, failure},
		{"nil", false, nil, true, false, rulite.ErrInvalidCondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := &pricingState{total: 150}
			calls := 0
			var child rulite.Condition[pricingState]
			if !tc.nilChild {
				child = func(gotContext context.Context, gotInput *pricingState) (bool, error) {
					if gotContext.Done() != ctx.Done() || gotContext.Err() != ctx.Err() || gotInput != input {
						t.Fatal("Not changed context semantics or input")
					}
					calls++
					return tc.matched, tc.err
				}
			}
			combined := rulite.Not(child)
			if calls != 0 {
				t.Fatal("Not invoked the child during composition")
			}
			got, err := combined(ctx, input)
			if got != tc.want || err != tc.wantErr {
				t.Fatalf("Not = %t, %v; want %t, %v", got, err, tc.want, tc.wantErr)
			}
			wantCalls := 1
			if tc.nilChild {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("calls = %d; want %d", calls, wantCalls)
			}
		})
	}
}

func TestNestedConditions(t *testing.T) {
	failure := errors.New("eligibility unavailable")
	for _, nestedError := range []bool{false, true} {
		name := "success"
		if nestedError {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			var calls []string
			leaf := func(name string, matched bool, err error) rulite.Condition[pricingState] {
				return func(context.Context, *pricingState) (bool, error) {
					calls = append(calls, name)
					return matched, err
				}
			}
			forbidden := func(context.Context, *pricingState) (bool, error) {
				t.Fatal("child called after short circuit")
				return false, nil
			}
			var childError error
			if nestedError {
				childError = failure
			}
			condition := rulite.All(
				leaf("start", true, nil),
				rulite.Any(
					leaf("miss", false, nil),
					rulite.Not(rulite.All(leaf("inverted", false, childError), forbidden)),
					forbidden,
				),
				leaf("finish", true, nil),
			)
			got, err := condition(context.Background(), &pricingState{})
			wantCalls := []string{"start", "miss", "inverted"}
			if !nestedError {
				wantCalls = append(wantCalls, "finish")
			}
			if got == nestedError || err != childError || !slices.Equal(calls, wantCalls) {
				t.Fatalf("nested condition = %t, %v, calls %v; want %t, %v, %v", got, err, calls, !nestedError, childError, wantCalls)
			}
		})
	}
	condition := rulite.Not(rulite.Any(rulite.All[pricingState](), rulite.Not[pricingState](nil)))
	if got, err := condition(context.Background(), &pricingState{}); got || err != nil {
		t.Fatalf("nested empty conditions = %t, %v; want false, nil", got, err)
	}
	condition = rulite.All(rulite.Any[pricingState](), rulite.Not[pricingState](nil))
	if got, err := condition(context.Background(), &pricingState{}); got || err != nil {
		t.Fatalf("nested empty short circuit = %t, %v; want false, nil", got, err)
	}
	condition = rulite.Not(rulite.All(rulite.Not[pricingState](nil)))
	if got, err := condition(context.Background(), &pricingState{}); got || !errors.Is(err, rulite.ErrInvalidCondition) {
		t.Fatalf("nested nil child = %t, %v; want ErrInvalidCondition", got, err)
	}
}

func TestConditionSliceIsCopied(t *testing.T) {
	for _, useAll := range []bool{true, false} {
		name := "any"
		if useAll {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			child := func(context.Context, *pricingState) (bool, error) {
				calls++
				return useAll, nil
			}
			children := []rulite.Condition[pricingState]{child, child}
			var condition rulite.Condition[pricingState]
			if useAll {
				condition = rulite.All(children...)
			} else {
				condition = rulite.Any(children...)
			}
			children[0], children[1] = nil, nil
			got, err := condition(context.Background(), &pricingState{})
			if got != useAll || err != nil || calls != 2 {
				t.Fatalf("slice mutation changed condition: %t, %v, %d calls", got, err, calls)
			}
		})
	}
}
