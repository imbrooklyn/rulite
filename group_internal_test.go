package rulite

import (
	"context"
	"fmt"
	"testing"
)

func TestSparseGroupHoles(t *testing.T) {
	for _, size := range []int{10, 100, 10000} {
		for _, matched := range []bool{false, true} {
			rules := make([]Rule[int], size)
			for i := range rules {
				rules[i] = NewRule[int](RuleID(fmt.Sprintf("member/%d", i))).When(func(context.Context, *int) (bool, error) {
					if matched && i > 0 {
						t.Fatal("resolved suffix was evaluated")
					}
					return matched, nil
				}).Then(func(_ context.Context, input *int) error { *input++; return nil })
			}
			audit := NewRule[int]("audit").When(func(context.Context, *int) (bool, error) { return matched, nil }).Then(func(_ context.Context, input *int) error { *input++; return nil })
			set, err := CompileEntries(FirstMatchGroup("offers", rules...).Entry(), audit.Entry())
			if err != nil {
				t.Fatal(err)
			}
			engine, err := NewEngineFromRuleSet(set)
			if err != nil || engine.snapshot != set.snapshot {
				t.Fatal("group snapshot not shared")
			}
			var input int
			var result Result
			allocations := testing.AllocsPerRun(20, func() { input = 0; result, err = engine.Fire(context.Background(), &input) })
			wantRecords, wantAllocations, wantEvaluated := 0, float64(1), size+1
			if matched {
				wantRecords, wantAllocations, wantEvaluated = 2, 3, 2
			}
			if err != nil || len(result.records) != wantRecords || len(result.groups) != 1 || result.trace != nil || result.evaluatedThrough != size+1 || result.Evaluated() != wantEvaluated || allocations != wantAllocations {
				t.Fatalf("size=%d matched=%t records=%d allocations=%g counts=%+v", size, matched, len(result.records), allocations, result.Counts())
			}
			last, _ := result.Rule(rules[size-1].ID())
			if matched && last.NotEvaluatedReason() != NotEvaluatedGroupResolved {
				t.Fatal("sparse hole lost")
			}
			t.Logf("members=%d matched=%t records=%d allocations=%g", size, matched, len(result.records), allocations)
		}
	}
}
