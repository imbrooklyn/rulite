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
		var input int
		var result Result
		allocations := testing.AllocsPerRun(20, func() { result, err = engine.Fire(context.Background(), &input) })
		if err != nil || result.counts.Unmatched != size || len(result.records) != 0 || result.trace != nil || result.metadata != engine.snapshot.metadata {
			t.Fatal("all-miss execution is not sparse")
		}
		if size > 1 && allocations > previous {
			t.Fatalf("all-miss allocation grows with rules: %v > %v", allocations, previous)
		}
		t.Logf("rules=%d allocations=%g", size, allocations)
		previous = allocations
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
