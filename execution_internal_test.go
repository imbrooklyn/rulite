package rulite

import (
	"context"
	"fmt"
	"testing"
)

func TestSparseAllMissAllocation(t *testing.T) {
	var previous float64
	for _, size := range []int{1, 100, 10000} {
		rules := make([]Rule[int], size)
		for index := range rules {
			rules[index] = NewRule[int](RuleID(fmt.Sprintf("miss/%d", index))).When(func(context.Context, *int) (bool, error) { return false, nil }).Then(func(context.Context, *int) error { t.Fatal("unmatched action called"); return nil })
		}
		engine, err := NewEngine(rules...)
		if err != nil {
			t.Fatal(err)
		}
		set, err := Compile(rules...)
		if err != nil {
			t.Fatal(err)
		}
		reused, err := NewEngineFromRuleSet(set)
		if err != nil {
			t.Fatal(err)
		}
		entries := make([]Entry[int], len(rules))
		for index, rule := range rules {
			entries[index] = rule.Entry()
		}
		mixedSet, err := CompileEntries(entries...)
		if err != nil {
			t.Fatal(err)
		}
		mixed, err := NewEngineFromRuleSet(mixedSet)
		if err != nil {
			t.Fatal(err)
		}
		for path, engine := range []*Engine[int]{engine, reused, mixed} {
			var input int
			var result Result
			allocations := testing.AllocsPerRun(20, func() { result, err = engine.Fire(context.Background(), &input) })
			if err != nil || result.counts.Unmatched != size || len(result.records) != 0 || result.groups != nil || result.trace != nil || result.diagnostics != nil || result.metadata != engine.snapshot.metadata {
				t.Fatal("all-miss execution is not sparse")
			}
			if allocations != 0 || size > 1 && allocations > previous {
				t.Fatalf("all-miss execution allocated: %v", allocations)
			}
			t.Logf("rules=%d path=%d allocations=%g", size, path, allocations)
			previous = allocations
			runtime, err := NewRuntime(engine)
			if err != nil {
				t.Fatal(err)
			}
			allocations = testing.AllocsPerRun(20, func() { result, err = runtime.Fire(context.Background(), &input) })
			if err != nil || allocations != 0 || result.counts.Unmatched != size || len(result.records) != 0 || result.metadata != engine.snapshot.metadata || result.Snapshot().Revision() != 1 {
				t.Fatal("runtime lost sparse allocation or metadata boundary", allocations, err)
			}
		}
	}
}

func TestFireCapturesConfiguration(t *testing.T) {
	options := []FireOption{WithPolicy(DefaultPolicy().WithStop(StopOnFirstMatch))}
	first := NewRule[int]("first").When(func(context.Context, *int) (bool, error) {
		options[0] = WithPolicy(DefaultPolicy())
		return true, nil
	}).Then(func(context.Context, *int) error { return nil })
	next := NewRule[int]("unreached").When(func(context.Context, *int) (bool, error) {
		t.Fatal("execution config changed during a callback")
		return false, nil
	}).Then(func(context.Context, *int) error { return nil })
	engine, err := NewEngine(first, next)
	if err != nil {
		t.Fatal(err)
	}
	var input int
	result, err := engine.Fire(context.Background(), &input, options...)
	if err != nil || result.StopReason() != StopFirstMatch {
		t.Fatal("Fire did not capture immutable config")
	}
}
