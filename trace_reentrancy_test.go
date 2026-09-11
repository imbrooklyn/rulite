package rulite_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/imbrooklyn/rulite"
)

func TestTraceReentrantContext(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			t.Run(fmt.Sprintf("nested_%t_trace_%t", nested, traced), func(t *testing.T) {
				condition := rulite.Condition[executionInput](func(ctx context.Context, input *executionInput) (bool, error) {
					return rulite.All(
						func(context.Context, *executionInput) (bool, error) {
							// Reuse the enclosing callback's context instead of the child context.
							return rulite.All[executionInput]()(ctx, input)
						},
						constantCondition(true, nil),
					)(ctx, input)
				})
				if nested {
					condition = rulite.All(condition)
				}
				var options []rulite.FireOption
				if traced {
					options = append(options, rulite.WithTrace())
				}
				engine := mustEngine(t, rulite.NewRule[executionInput]("reentrant").When(condition).Then(successfulAction))
				result, err := engine.Fire(context.Background(), &executionInput{}, options...)
				if err != nil || result.Counts().Fired != 1 {
					t.Fatalf("reentrant condition: fired=%d, error=%v", result.Counts().Fired, err)
				}
				if traced {
					checkOpaqueTrace(t, result, nested)
				}
			})
		}
	}
}

func TestTraceConcurrentContext(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested_%t", nested), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				condition := rulite.Condition[executionInput](func(ctx context.Context, input *executionInput) (bool, error) {
					arrived, release := make(chan struct{}), make(chan struct{})
					shared := rulite.All(func(context.Context, *executionInput) (bool, error) {
						arrived <- struct{}{}
						<-release
						return true, nil
					}, constantCondition(true, nil))
					var results [2]struct {
						matched bool
						err     error
					}
					var workers sync.WaitGroup
					for i := range results {
						workers.Go(func() { results[i].matched, results[i].err = shared(ctx, input) })
					}
					// Both callbacks must run before either completes: recorder locks
					// must not be held across user code.
					for range results {
						<-arrived
					}
					close(release)
					workers.Wait()
					for _, result := range results {
						if !result.matched || result.err != nil {
							return false, fmt.Errorf("concurrent condition: matched=%t, error=%v", result.matched, result.err)
						}
					}
					return true, nil
				})
				if nested {
					condition = rulite.All(condition)
				}
				engine := mustEngine(t, rulite.NewRule[executionInput]("concurrent").When(condition).Then(successfulAction))
				result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
				if err != nil || result.Counts().Fired != 1 {
					t.Fatalf("concurrent condition: fired=%d, error=%v", result.Counts().Fired, err)
				}
				checkOpaqueTrace(t, result, nested)
			})
		})
	}
}

func checkOpaqueTrace(t *testing.T, result rulite.Result, nested bool) {
	t.Helper()
	trace, _ := result.Trace()
	root, ok := trace.Rules()[0].ConditionTree()
	if ok != nested {
		t.Fatal("multiple operators on one context claimed a condition tree")
	}
	if nested {
		checkConditionTree(t, root, expectedCondition{
			kind: rulite.ConditionAll, index: -1, outcome: rulite.ConditionOutcomeTrue,
			children: []expectedCondition{{index: 0, outcome: rulite.ConditionOutcomeTrue}},
		})
	}
}

func TestTraceConcurrentRetainedContext(t *testing.T) {
	var workers sync.WaitGroup
	defer workers.Wait()
	shared := rulite.All(constantCondition(true, nil))
	leaf := func(ctx context.Context, input *executionInput) (bool, error) {
		// The input is not read or written by these callbacks. Reusing a retained
		// context can overlap freezing and later reads of the returned trace.
		workers.Go(func() {
			for range 64 {
				if matched, err := shared(ctx, input); !matched || err != nil {
					t.Errorf("retained context: matched=%t, error=%v", matched, err)
				}
			}
		})
		return true, nil
	}
	engine := mustEngine(t, rulite.NewRule[executionInput]("retained").When(rulite.All(leaf)).Then(successfulAction))
	for range 16 {
		result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
		if err != nil {
			t.Fatal(err)
		}
		trace, _ := result.Trace()
		root, _ := trace.Rules()[0].ConditionTree()
		before := root.Children()
		workers.Wait()
		if !reflect.DeepEqual(root.Children(), before) {
			t.Fatal("concurrent retained context changed an immutable trace")
		}
	}
}
