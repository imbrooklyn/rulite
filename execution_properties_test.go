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

func TestRepeatedExecutionDeterminism(t *testing.T) {
	random := rand.New(rand.NewPCG(31, 109))
	for trial := range 100 {
		var rules []rulite.Rule[executionInput]
		var observed []rulite.RuleID
		for i := range 64 {
			id := rulite.RuleID(fmt.Sprintf("pricing/%d", i))
			rules = append(rules, rulite.NewRule[executionInput](id).Priority(rulite.Priority(random.IntN(9)-4)).
				When(func(_ context.Context, input *executionInput) (bool, error) {
					observed = append(observed, id)
					return (input.value+i)%3 == 0, nil
				}).
				Then(func(_ context.Context, input *executionInput) error { input.value++; return nil }))
		}
		engine := mustEngine(t, rules...)
		policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(trial % 3))
		initial := random.IntN(100)
		var baseline rulite.Result
		var sequence []rulite.RuleID
		var finalValue int
		for attempt := range 4 {
			observed = nil
			input := executionInput{value: initial}
			var options = []rulite.FireOption{rulite.WithPolicy(policy)}
			if attempt%2 != 0 {
				options = append(options, rulite.WithTrace())
			}
			result, err := engine.Fire(context.Background(), &input, options...)
			if err != nil {
				t.Fatal(err)
			}
			checkResultConsistency(t, result)
			views := result.Explain().Rules()
			for order, view := range views {
				if order > 0 && (views[order-1].Priority() < view.Priority() || views[order-1].Priority() == view.Priority() && views[order-1].RegistrationIndex() >= view.RegistrationIndex()) {
					t.Fatal("unstable execution order")
				}
				if order < len(observed) && view.ID() != observed[order] {
					t.Fatal("observed order differs from explanation")
				}
			}
			if len(observed) != result.Evaluated() {
				t.Fatal("callback count differs from ledger")
			}
			if attempt == 0 {
				baseline, sequence, finalValue = result, slices.Clone(observed), input.value
				continue
			}
			if !slices.Equal(observed, sequence) || input.value != finalValue || result.Counts() != baseline.Counts() || !reflect.DeepEqual(views, baseline.Explain().Rules()) || result.StopReason() != baseline.StopReason() {
				t.Fatal("same engine and reset input changed execution")
			}
		}
	}
}

func TestCallerSynchronizesSharedInput(t *testing.T) {
	engine := mustEngine(t, rulite.NewRule[executionInput]("counter/increment").When(rulite.All[executionInput]()).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil }))
	var input executionInput
	var inputMu sync.Mutex
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			for range 8 {
				// The caller locks the entire execution, including every condition and action.
				inputMu.Lock()
				result, err := engine.Fire(context.Background(), &input)
				inputMu.Unlock()
				if err != nil || result.Counts().Fired != 1 {
					t.Error("serialized execution failed")
				}
			}
		})
	}
	workers.Wait()
	if input.value != 256 {
		t.Fatalf("shared input=%d; want 256", input.value)
	}
}

func TestPricingAuditOneHundredRules(t *testing.T) {
	type price struct {
		discount  int
		appliedBy rulite.RuleID
	}
	eligibilityError := errors.New("customer eligibility unavailable")
	discountError := errors.New("discount reservation unavailable")
	for _, tc := range []struct {
		name                                string
		mode                                rulite.StopMode
		stop                                rulite.StopReason
		evaluated, matched, fired, discount int
		appliedBy                           rulite.RuleID
	}{
		{"all", rulite.EvaluateAll, rulite.StopCompleted, 100, 4, 3, 10, "pricing/050"},
		{"first_match", rulite.StopOnFirstMatch, rulite.StopFirstMatch, 21, 1, 0, 5, "pricing/020"},
		{"first_fire", rulite.StopOnFirstFire, rulite.StopFirstFire, 31, 2, 1, 20, "pricing/030"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rules []rulite.Rule[price]
			var evaluated, attempted []rulite.RuleID
			for i := 99; i >= 0; i-- {
				id := rulite.RuleID(fmt.Sprintf("pricing/%03d", i))
				rules = append(rules, rulite.NewRule[price](id).Priority(rulite.Priority(100-i)).When(func(context.Context, *price) (bool, error) {
					evaluated = append(evaluated, id)
					if i == 10 {
						return true, eligibilityError
					}
					return i == 20 || i == 30 || i == 40 || i == 50, nil
				}).Then(func(_ context.Context, p *price) error {
					attempted = append(attempted, id)
					if i == 40 {
						return nil
					} // A successful action need not write any field.
					p.appliedBy = id
					switch i {
					case 20:
						p.discount = 5
						return discountError // Partial mutation survives failure.
					case 30:
						p.discount = 20
					case 50:
						p.discount = 10
					}
					return nil
				}))
			}
			engine := mustEngine(t, rules...)
			input := price{}
			policy := rulite.DefaultPolicy().WithStop(tc.mode).WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)
			result, err := engine.Fire(context.Background(), &input, rulite.WithPolicy(policy), rulite.WithTrace())
			if !errors.Is(err, eligibilityError) || !errors.Is(err, discountError) || result.StopReason() != tc.stop {
				t.Fatalf("stop=%v, errors=%v", result.StopReason(), err)
			}
			c := result.Counts()
			if c.Total != 100 || c.Evaluated != tc.evaluated || c.Matched != tc.matched || c.Fired != tc.fired || c.ConditionFailed != 1 || c.ActionFailed != 1 || input.discount != tc.discount || input.appliedBy != tc.appliedBy {
				t.Fatalf("counts=%+v, price=%+v", c, input)
			}
			if len(evaluated) != tc.evaluated || len(attempted) != tc.matched {
				t.Fatal("callbacks ran past stop or were repeated")
			}
			for order, view := range result.Explain().Rules() {
				id := rulite.RuleID(fmt.Sprintf("pricing/%03d", order))
				if view.ID() != id || view.RegistrationIndex() != 99-order || view.Priority() != rulite.Priority(100-order) {
					t.Fatal("audit lost compiled or registration order")
				}
				if order < tc.evaluated {
					if evaluated[order] != id || !view.Evaluated() {
						t.Fatal("observed condition differs from audit")
					}
				} else if view.State() != rulite.RuleNotEvaluated || view.NotEvaluatedReason() != rulite.NotEvaluatedExecutionStopped {
					t.Fatal("audit cannot explain untouched suffix")
				}
			}
			failures := result.Failures()
			if len(failures) != 2 || failures[0].RuleID() != "pricing/010" || failures[0].Phase() != rulite.ConditionPhase || failures[1].RuleID() != "pricing/020" || failures[1].Phase() != rulite.ActionPhase {
				t.Fatal("audit lost ordered phase failures")
			}
			checkResultConsistency(t, result)
		})
	}
}
