package cel_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"cel.dev/cel-go/common/types"
	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
)

func TestConditionPolicyAndTraceParity(t *testing.T) {
	c := compiler[price](t)
	noAction := func(context.Context, *price) error { t.Error("failed condition ran action"); return nil }
	first := rulite.NewRule[price]("check/division").When(condition(t, c, "1 / input.Total > 0")).Then(noAction)
	second := rulite.NewRule[price]("check/index").When(condition(t, c, "input.Items[0] > 0")).Then(noAction)
	goRule := rulite.NewRule[price]("pricing/prepare").When(func(_ context.Context, p *price) (bool, error) { return p.Total == 0, nil }).
		Then(func(_ context.Context, p *price) error { p.Total = 12000; return nil })
	celRule := rulite.NewRule[price]("pricing/apply").When(condition(t, c, "input.Total == 12000")).
		Then(func(_ context.Context, p *price) error { p.Discount = 20; return nil })
	set, err := rulite.Compile(first, second, goRule, celRule)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
		for _, stop := range []rulite.StopMode{rulite.EvaluateAll, rulite.StopOnFirstMatch, rulite.StopOnFirstFire} {
			t.Run(fmt.Sprintf("errors_%d_stop_%d", mode, stop), func(t *testing.T) {
				var baseline rulite.Result
				for _, traced := range []bool{false, true} {
					options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(mode).WithStop(stop))}
					if traced {
						options = append(options, rulite.WithTrace())
					}
					input := price{}
					result, err := engine.Fire(context.Background(), &input, options...)
					wantEvaluated, wantFailed, wantFired, wantStop := 1, 1, 0, rulite.StopConditionError
					if mode == rulite.ContinueOnError {
						wantEvaluated, wantFailed, wantFired, wantStop = 4, 2, 2, rulite.StopCompleted
						if stop != rulite.EvaluateAll {
							wantEvaluated, wantFired = 3, 1
							wantStop = rulite.StopFirstMatch
							if stop == rulite.StopOnFirstFire {
								wantStop = rulite.StopFirstFire
							}
						}
					}
					counts := result.Counts()
					if counts.Evaluated != wantEvaluated || counts.ConditionFailed != wantFailed || counts.Matched != wantFired || counts.Fired != wantFired || result.StopReason() != wantStop {
						t.Fatalf("unexpected facts: %+v, %v", counts, result.StopReason())
					}
					if (input.Discount == 20) != (wantFired == 2) {
						t.Fatal("later CEL condition missed Go mutation")
					}
					var executionErr *rulite.ExecutionError
					var failure rulite.Failure
					var runtimeErr *cel.RuntimeError
					var upstream *types.Err
					if !errors.As(err, &executionErr) || !errors.As(err, &failure) || !errors.As(err, &runtimeErr) || !errors.As(err, &upstream) {
						t.Fatal("canonical error tree changed")
					}
					failures := result.Failures()
					for i, failure := range failures {
						wantID := []rulite.RuleID{"check/division", "check/index"}[i]
						if failure.RuleID() != wantID || failure.Phase() != rulite.ConditionPhase || failure.Continued() != (mode == rulite.ContinueOnError) || failure != executionErr.Failures()[i] || !errors.Is(err, failure.Cause()) {
							t.Fatal("failure identity, order, cause, or disposition changed")
						}
					}
					for _, rule := range result.Explain().Rules() {
						lookup, _ := result.Rule(rule.ID())
						if !reflect.DeepEqual(rule, lookup) {
							t.Fatal("explanation diverged from result")
						}
					}
					clear(failures)
					if result.Failures()[0].RuleID() == "" {
						t.Fatal("failure slice is mutable")
					}
					if traced {
						if counts != baseline.Counts() || result.Explain().String() != baseline.Explain().String() {
							t.Fatal("trace changed outcomes")
						}
						trace, _ := result.Trace()
						for _, rule := range trace.Rules() {
							lookup, _ := result.Rule(rule.ID())
							if rule.State() != lookup.State() || rule.NotEvaluatedReason() != lookup.NotEvaluatedReason() {
								t.Fatal("trace diverged from ledger")
							}
							if lookup.Error() != nil && rule.Error() != lookup.Error() {
								t.Fatal("trace created another failure")
							}
						}
					} else {
						baseline = result
					}
				}
			})
		}
	}
}

func TestCELGroupSeesPartialActionMutation(t *testing.T) {
	c := compiler[price](t)
	partial := errors.New("discount provider unavailable")
	primary := rulite.NewRule[price]("pricing/primary").When(condition(t, c, "input.VIP")).Then(func(_ context.Context, p *price) error { p.Total = 42; return partial })
	backup := rulite.NewRule[price]("pricing/backup").When(condition(t, c, "input.Total == 42")).Then(func(_ context.Context, p *price) error { p.Discount = 10; p.AppliedBy = "pricing/backup"; return nil })
	audit := rulite.NewRule[price]("pricing/audit").When(func(_ context.Context, p *price) (bool, error) { return p.Discount == 10, nil }).Then(func(context.Context, *price) error { return nil })
	set, err := rulite.CompileEntries(rulite.FirstFireGroup("pricing/offers", primary, backup).Entry(), audit.Entry())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set, rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)))
	if err != nil {
		t.Fatal(err)
	}
	input := price{VIP: true}
	result, err := engine.Fire(context.Background(), &input)
	group, _ := result.Group("pricing/offers")
	selected, ok := group.SelectedRule()
	if !errors.Is(err, partial) || !ok || selected != "pricing/backup" || input.AppliedBy != selected || result.Counts().Fired != 2 {
		t.Fatal("mixed group lost partial effects or local selection")
	}
}

func TestConcurrentCompilerAndProgram(t *testing.T) {
	c := compiler[price](t)
	f := condition(t, c, "input.Total > 50 && input.Items.all(x, x > 0)")
	rule := rulite.NewRule[price]("pricing/shared").When(f).Then(func(_ context.Context, p *price) error { p.Discount++; return nil })
	engine, err := rulite.NewEngine(rule)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		var wg sync.WaitGroup
		for worker := range workers {
			wg.Go(func() {
				compiled, err := c.Compile("input.VIP")
				if err != nil {
					t.Error(err)
					return
				}
				for iteration := range 20 {
					input := price{Total: int64(worker + iteration*4), Items: []int64{1, 2}, VIP: worker%2 == 0}
					ok, err := compiled(context.Background(), &input)
					if err != nil || ok != input.VIP {
						t.Error("concurrent compile/eval diverged")
					}
					result, err := engine.Fire(context.Background(), &input, rulite.WithTrace())
					want := 0
					if input.Total > 50 {
						want = 1
					}
					if err != nil || result.Counts().Fired != want || input.Discount != want {
						t.Error("shared program reused another input")
					}
					_ = result.Explain().String()
				}
			})
		}
		wg.Wait()
	}
	var mu sync.Mutex
	shared := price{Total: 100, Items: []int64{1}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				mu.Lock()
				_, err := engine.Fire(context.Background(), &shared)
				mu.Unlock()
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if shared.Discount != 160 {
		t.Fatal("caller synchronization lost mutations")
	}
}
