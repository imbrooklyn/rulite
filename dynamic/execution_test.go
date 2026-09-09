package dynamic_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
)

func fireSet(t testing.TB, set *rulite.RuleSet[price], input price, want int) {
	t.Helper()
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Fire(context.Background(), &input)
	if err != nil || result.Counts().Fired != want || input.Discount != want {
		t.Fatalf("unexpected execution: %+v, %v", input, err)
	}
}

type stepParams struct {
	Amount int    `json:"amount"`
	Label  string `json:"label"`
	Fail   bool   `json:"fail,omitempty"`
	Panic  bool   `json:"panic,omitempty"`
	Cancel bool   `json:"cancel,omitempty"`
}

var errAction = errors.New("action unavailable")
var errCanceled = errors.New("caller canceled operation")

type cancelKey struct{}

func applyStep(ctx context.Context, p *price, params stepParams) error {
	p.Discount += params.Amount
	p.Steps = append(p.Steps, params.Label)
	if params.Cancel {
		ctx.Value(cancelKey{}).(context.CancelCauseFunc)(errCanceled)
	}
	if params.Panic {
		panic(errAction)
	}
	if params.Fail {
		return errAction
	}
	return nil
}

func TestTypedAndDynamicOutcomeProperty(t *testing.T) {
	c := compiler(t)
	r := dynamic.NewRegistry[price]()
	if err := r.Register("test.step/v1", applyStep); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(19, 71))
	for scenario := range 40 {
		definitions := make([]dynamic.Definition, 5)
		typed := make([]rulite.Rule[price], 5)
		for i := range definitions {
			threshold := random.IntN(6) - 1
			p := stepParams{Amount: random.IntN(3) + 1, Label: fmt.Sprintf("step/%d", i), Fail: random.IntN(4) == 0}
			d := dynamic.Definition{ID: rulite.RuleID(p.Label), Priority: rulite.Priority(random.IntN(5) - 2), Description: " step ", Tags: []string{" test ", "test"}, When: fmt.Sprintf("input.Discount >= %d", threshold), Action: "test.step/v1"}
			var err error
			d.Params, err = json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			definitions[i] = d
			typed[i] = rulite.NewRule[price](d.ID).Priority(d.Priority).Description(d.Description).Tags(d.Tags...).
				When(func(_ context.Context, p *price) (bool, error) { return p.Discount >= threshold, nil }).
				Then(func(ctx context.Context, input *price) error { return applyStep(ctx, input, p) })
		}
		dynamicSet, err := dynamic.Compile(definitions, c, r)
		if err != nil {
			t.Fatal(err)
		}
		typedSet, err := rulite.Compile(typed...)
		if err != nil {
			t.Fatal(err)
		}
		for _, stop := range []rulite.StopMode{rulite.EvaluateAll, rulite.StopOnFirstMatch, rulite.StopOnFirstFire} {
			for _, ce := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
				for _, ae := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
					policy := rulite.WithPolicy(rulite.DefaultPolicy().WithStop(stop).WithConditionErrors(ce).WithActionErrors(ae))
					for _, trace := range []bool{false, true} {
						options := []rulite.FireOption{policy}
						if trace {
							options = append(options, rulite.WithTrace())
						}
						dynEngine, err := rulite.NewEngineFromRuleSet(dynamicSet, options...)
						if err != nil {
							t.Fatal(err)
						}
						goEngine, err := rulite.NewEngineFromRuleSet(typedSet, options...)
						if err != nil {
							t.Fatal(err)
						}
						a, b := price{Discount: scenario % 5}, price{Discount: scenario % 5}
						x, xe := dynEngine.Fire(context.Background(), &a)
						y, ye := goEngine.Fire(context.Background(), &b)
						if !reflect.DeepEqual(a, b) || x.Counts() != y.Counts() || x.StopReason() != y.StopReason() || errors.Is(xe, errAction) != errors.Is(ye, errAction) || !reflect.DeepEqual(x.Failures(), y.Failures()) || !reflect.DeepEqual(x.Explain().Rules(), y.Explain().Rules()) {
							t.Fatalf("typed parity failed in scenario %d", scenario)
						}
						// Compare public entry facts; private views may retain different timings.
						xs, ys := x.Explain().Entries(), y.Explain().Entries()
						if len(xs) != len(ys) {
							t.Fatal("entry count changed")
						}
						for i, entry := range xs {
							xr, xok := entry.Rule()
							yr, yok := ys[i].Rule()
							if entry.Order() != ys[i].Order() || xok != yok || !reflect.DeepEqual(xr, yr) || len(entry.Members()) != 0 {
								t.Fatal("structured entry outcome changed")
							}
						}
						if tr, ok := x.Trace(); ok != trace {
							t.Fatal("trace mode changed")
						} else if ok {
							for _, rule := range tr.Rules() {
								if _, tree := rule.ConditionTree(); tree {
									t.Fatal("CEL invented a condition tree")
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestCanonicalFailuresContextAndPanic(t *testing.T) {
	c := compiler(t)
	r := dynamic.NewRegistry[price]()
	if err := r.Register("test.step/v1", applyStep); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	for _, trace := range []bool{false, true} {
		for _, conditionErrors := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
			ds := []dynamic.Definition{{ID: "step/error", When: "1 / input.Total > 0", Action: "test.step/v1", Params: json.RawMessage(`{"amount":1,"label":"error"}`)},
				{ID: "step/success", When: "true", Action: "test.step/v1", Params: json.RawMessage(`{"amount":1,"label":"success"}`)}}
			set, err := dynamic.Compile(ds, c, r)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := rulite.NewEngineFromRuleSet(set)
			if err != nil {
				t.Fatal(err)
			}
			options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(conditionErrors))}
			if trace {
				options = append(options, rulite.WithTrace())
			}
			input := price{}
			result, err := engine.Fire(context.Background(), &input, options...)
			var ee *rulite.ExecutionError
			var re *cel.RuntimeError
			if !errors.As(err, &ee) || !errors.As(err, &re) || len(result.Failures()) != 1 || result.Failures()[0].RuleID() != "step/error" || result.Failures()[0].Phase() != rulite.ConditionPhase {
				t.Fatal("condition failure lost identity or cause")
			}
			want := 0
			reason := rulite.StopConditionError
			if conditionErrors == rulite.ContinueOnError {
				want = 1
				reason = rulite.StopCompleted
			}
			if input.Discount != want || result.StopReason() != reason {
				t.Fatal("condition error became a miss or changed policy")
			}
		}
		for _, kind := range []string{"failure", "panic", "cancel", "cancel_failure", "cancel_panic"} {
			p := stepParams{Amount: 3, Label: "partial", Fail: kind == "failure" || kind == "cancel_failure", Panic: kind == "panic" || kind == "cancel_panic", Cancel: kind == "cancel" || kind == "cancel_failure" || kind == "cancel_panic"}
			raw, _ := json.Marshal(p)
			ds := []dynamic.Definition{{ID: "step/partial", When: "true", Action: "test.step/v1", Params: raw}, {ID: "step/later", When: "input.Discount == 3", Action: "test.step/v1", Params: json.RawMessage(`{"amount":1,"label":"later"}`)}}
			set, err := dynamic.Compile(ds, c, r)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := rulite.NewEngineFromRuleSet(set)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			ctx = context.WithValue(ctx, cancelKey{}, cancel)
			options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError))}
			if trace {
				options = append(options, rulite.WithTrace())
			}
			input := price{}
			result, err := engine.Fire(ctx, &input, options...)
			cancel(nil)
			reason, want := rulite.StopCompleted, 4
			if p.Cancel {
				reason, want = rulite.StopContextCanceled, 3
			}
			if p.Panic {
				reason, want = rulite.StopPanic, 3
			}
			if err == nil || result.StopReason() != reason || input.Discount != want || len(input.Steps) != (want-2) {
				t.Fatal("partial effects or stop precedence changed")
			}
			if p.Cancel && (!errors.Is(err, context.Canceled) || !errors.Is(err, errCanceled)) {
				t.Fatal("caller cancellation cause lost")
			}
			if p.Fail && !p.Panic && !errors.Is(err, errAction) {
				t.Fatal("action cause lost")
			}
			if p.Panic {
				var pe *rulite.PanicError
				if !errors.As(err, &pe) || pe.Value() != errAction || pe.Phase() != rulite.ActionPhase {
					t.Fatal("panic contract changed")
				}
				func() {
					defer func() {
						if recover() != errAction {
							t.Error("panic was not propagated")
						}
					}()
					input := price{}
					_, _ = engine.Fire(context.WithValue(context.Background(), cancelKey{}, context.CancelCauseFunc(func(error) {})), &input, rulite.WithPanicMode(rulite.PropagatePanics))
				}()
			}
		}
	}
}

func TestFrozenRegistryProgramAndEnginesConcurrent(t *testing.T) {
	c := compiler(t)
	r := dynamic.NewRegistry[price]()
	var calls, validations atomic.Int64
	if err := r.Register("pricing.apply/v1", func(_ context.Context, p *price, v discountParams) error {
		calls.Add(1)
		p.Discount += v.Limits["bonus"] + len(v.Labels)
		return nil
	}, func(discountParams) error { validations.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	d := definition()
	d.When = "input.Total > 0"
	d.Params = json.RawMessage(`{"limits":{"bonus":2},"labels":["v"]}`)
	raw := document(t, []dynamic.Definition{d})
	set, err := dynamic.CompileJSON(raw, c, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			var wg sync.WaitGroup
			for worker := range workers {
				wg.Go(func() {
					local, err := dynamic.CompileJSON(raw, c, r)
					if err != nil {
						t.Error(err)
						return
					}
					if local.Len() != set.Len() {
						t.Error("concurrent compile changed definitions")
						return
					}
					engine, err := rulite.NewEngineFromRuleSet(set)
					if err != nil {
						t.Error(err)
						return
					}
					for i := range 30 {
						input := price{Total: int64((i + worker) % 2)}
						result, err := engine.Fire(context.Background(), &input, rulite.WithTrace())
						want := int(input.Total)
						if err != nil || result.Counts().Fired != want || input.Discount != want*3 {
							t.Error("input or params leaked across executions")
						}
					}
				})
			}
			wg.Wait()
		})
	}
	if calls.Load() != 63*15 || validations.Load() != 64 {
		t.Fatal("registration, projection or validation repeated during Fire")
	}
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	shared := price{Total: 1}
	var mu sync.Mutex
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
	if shared.Discount != 480 {
		t.Fatal("caller synchronization lost effects")
	}
}

func TestDynamicCostFailure(t *testing.T) {
	c, err := cel.NewCompiler[price]("input", cel.WithCostLimit(1))
	if err != nil {
		t.Fatal(err)
	}
	d := definition()
	d.When = "input.Steps.all(x, x != '')"
	set, err := dynamic.Compile([]dynamic.Definition{d}, c, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	input := price{Steps: []string{"one", "two", "three"}}
	result, err := engine.Fire(context.Background(), &input)
	if !errors.Is(err, cel.ErrCostLimit) || result.StopReason() != rulite.StopConditionError || result.Counts().ConditionFailed != 1 || result.Counts().Matched != 0 || input.Discount != 0 {
		t.Fatal("budget failure became completed or false")
	}
}
