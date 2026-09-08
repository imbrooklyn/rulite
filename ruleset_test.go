package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func mustCompile[T any](t testing.TB, rules ...rulite.Rule[T]) *rulite.RuleSet[T] {
	t.Helper()
	set, err := rulite.Compile(rules...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func mustReuse[T any](t testing.TB, set *rulite.RuleSet[T], options ...rulite.FireOption) *rulite.Engine[T] {
	t.Helper()
	engine, err := rulite.NewEngineFromRuleSet(set, options...)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestRuleSetValidityAndEngineOptions(t *testing.T) {
	var zero rulite.RuleSet[pricingState]
	for _, set := range []*rulite.RuleSet[pricingState]{nil, &zero} {
		if set.Valid() || set.Len() != 0 || len(set.Rules()) != 0 {
			t.Fatal("uninitialized set masquerades as compiled")
		}
		if info, ok := set.Rule("unknown"); ok || !reflect.DeepEqual(info, rulite.RuleInfo{}) {
			t.Fatal("invalid set returned a rule")
		}
		engine, err := rulite.NewEngineFromRuleSet(set, rulite.WithPanicMode(255))
		if engine != nil || err != rulite.ErrInvalidRuleSet {
			t.Fatalf("invalid set must precede options: %v", err)
		}
	}
	set := mustCompile[pricingState](t)
	if !set.Valid() || set.Len() != 0 || len(set.Rules()) != 0 {
		t.Fatal("empty compiled set is invalid")
	}
	for _, options := range [][]rulite.FireOption{nil, {{}}, {rulite.WithTrace(), rulite.WithTrace()}} {
		engine := mustReuse(t, set, options...)
		result, err := engine.Fire(context.Background(), &pricingState{})
		if err != nil || !result.Executed() || result.Counts() != (rulite.Counts{}) || result.StopReason() != rulite.StopCompleted {
			t.Fatal("empty execution did not complete")
		}
		_, traced := result.Trace()
		if traced != (len(options) == 2) {
			t.Fatal("empty execution lost trace defaults")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err = engine.Fire(ctx, &pricingState{})
		if !errors.Is(err, context.Canceled) || result.StopReason() != rulite.StopContextCanceled || !result.Executed() {
			t.Fatal("empty set lost context boundary")
		}
	}
	badPolicy := rulite.WithPolicy(rulite.DefaultPolicy().WithStop(255))
	badPanic := rulite.WithPanicMode(255)
	for _, tc := range []struct {
		name    string
		options []rulite.FireOption
		want    error
	}{
		{"invalid_stop", []rulite.FireOption{badPolicy}, rulite.ErrInvalidPolicy},
		{"invalid_condition", []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(255))}, rulite.ErrInvalidPolicy},
		{"invalid_action", []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(255))}, rulite.ErrInvalidPolicy},
		{"invalid_panic", []rulite.FireOption{badPanic}, rulite.ErrInvalidPanicMode},
		{"policy_not_repaired", []rulite.FireOption{badPolicy, rulite.WithPolicy(rulite.DefaultPolicy())}, rulite.ErrInvalidPolicy},
		{"panic_not_repaired", []rulite.FireOption{badPanic, rulite.WithPanicMode(rulite.RecoverPanics)}, rulite.ErrInvalidPanicMode},
		{"policy_first", []rulite.FireOption{badPolicy, badPanic}, rulite.ErrInvalidPolicy},
		{"panic_first", []rulite.FireOption{badPanic, badPolicy}, rulite.ErrInvalidPanicMode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := rulite.NewEngineFromRuleSet(set, tc.options...)
			if engine != nil || err != tc.want {
				t.Fatalf("constructor = %v, %v; want nil, %v", engine, err, tc.want)
			}
		})
	}
}

func TestMetadataOwnershipAndNormalization(t *testing.T) {
	tags := []string{" pricing ", "", "\t", "pricing", "VIP", "vip", " two  words ", "\u2003review\u00a0"}
	base := rulite.NewRule[pricingState]("pricing/vip").Name(" \u2003VIP offer\n").Description("\tCustomer discount\n\nDetails. \n").Tags(tags...)
	tags[0] = "changed before completion"
	rule := base.Priority(100).When(eligible).Then(applyDiscount)
	other := base.Name("Other").Description("Other description").Tags("other").When(eligible).Then(applyDiscount)
	cleared := base.Name("").Description(" ").Tags().When(eligible).Then(applyDiscount)
	if other.Name() != "Other" || other.Description() != "Other description" || !slices.Equal(other.Tags(), []string{"other"}) || cleared.Name() != "" || cleared.Description() != "" || len(cleared.Tags()) != 0 {
		t.Fatal("metadata setters did not replace values independently")
	}
	rules := []rulite.Rule[pricingState]{rule}
	set := mustCompile(t, rules...)
	engine := mustReuse(t, set)
	rules[0] = rulite.Rule[pricingState]{}
	clear(tags)
	result, err := engine.Fire(context.Background(), &pricingState{total: 100}, rulite.WithTrace())
	if err != nil {
		t.Fatal(err)
	}
	info, ok := set.Rule("pricing/vip")
	if !ok || info.ID() != rule.ID() || info.Order() != 0 || info.RegistrationIndex() != 0 || info.Priority() != 100 || set.Len() != 1 {
		t.Fatal("incorrect compiled metadata")
	}
	x, _ := result.Rule(rule.ID())
	trace, _ := result.Trace()
	// The interface exists only in this consumer test, not in the runtime model.
	for _, view := range []interface {
		Name() string
		Description() string
		Tags() []string
	}{rule, info, set.Rules()[0], x, result.Explain().Rules()[0], trace.Rules()[0]} {
		if view.Name() != "VIP offer" || view.Description() != "Customer discount\n\nDetails." || !slices.Equal(view.Tags(), []string{"pricing", "VIP", "vip", "two  words", "review"}) {
			t.Fatal("metadata changed across construction or execution")
		}
		clear(view.Tags())
		if view.Tags()[0] != "pricing" {
			t.Fatal("tags accessor exposed internal storage")
		}
	}
	clear(set.Rules())
	clear(result.Explain().Rules())
	clear(trace.Rules())
	if set.Rules()[0].Name() != "VIP offer" || result.Explain().Rules()[0].Tags()[0] != "pricing" {
		t.Fatal("view slice exposed snapshot storage")
	}
	for _, id := range []rulite.RuleID{"unknown", "pricing/vip ", "Pricing/vip", ""} {
		if view, ok := set.Rule(id); ok || view.ID() != "" {
			t.Fatal("lookup normalized an ID or returned unknown metadata")
		}
	}
	for _, view := range []interface {
		Name() string
		Description() string
		Tags() []string
	}{rulite.Rule[pricingState]{}, rulite.RuleInfo{}, rulite.RuleExecution{}, rulite.RuleTrace{}} {
		if view.Name() != "" || view.Description() != "" || len(view.Tags()) != 0 {
			t.Fatal("zero metadata view is not empty")
		}
	}
	// Equal display metadata never substitutes for distinct stable IDs.
	a := rulite.NewRule[pricingState]("a").Name("Same").When(eligible).Then(applyDiscount)
	b := rulite.NewRule[pricingState]("b").Name("Same").When(eligible).Then(applyDiscount)
	if mustCompile(t, a, b).Len() != 2 {
		t.Fatal("name participated in identity")
	}
}

func TestConstructionParityProperties(t *testing.T) {
	random := rand.New(rand.NewPCG(73, 127))
	cause := errors.New("eligibility unavailable")
	for trial := range 160 {
		rules := make([]rulite.Rule[executionInput], random.IntN(65))
		for i := range rules {
			id := rulite.RuleID(fmt.Sprintf("rule/%d", i))
			priority := rulite.Priority(int32(random.Uint32()))
			switch i % 4 {
			case 0:
				priority = math.MinInt32
			case 1:
				priority = math.MaxInt32
			case 2:
				priority = 0
			}
			kind := random.IntN(4)
			rules[i] = rulite.NewRule[executionInput](id).Name("Offer").Tags(" pricing ", "pricing", "audit").Priority(priority).
				When(func(_ context.Context, input *executionInput) (bool, error) {
					if kind == 2 {
						return true, cause
					}
					return (input.value+i)%3 != 0, nil
				}).Then(func(_ context.Context, input *executionInput) error {
				input.value++
				input.calls = append(input.calls, string(id))
				if kind == 3 {
					return cause
				}
				return nil
			})
		}
		random.Shuffle(len(rules), func(i, j int) { rules[i], rules[j] = rules[j], rules[i] })
		set := mustCompile(t, rules...)
		convenient, reused := mustEngine(t, rules...), mustReuse(t, set)
		policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(trial % 3)).WithConditionErrors(rulite.ErrorMode(trial / 3 % 2)).WithActionErrors(rulite.ErrorMode(trial / 6 % 2))
		options := []rulite.FireOption{rulite.WithPolicy(policy)}
		initial := random.IntN(50)
		a, b := executionInput{value: initial}, executionInput{value: initial}
		x, errX := convenient.Fire(context.Background(), &a, options...)
		y, errY := reused.Fire(context.Background(), &b, options...)
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(x, y) || fmt.Sprint(errX) != fmt.Sprint(errY) || x.Explain().String() != y.Explain().String() {
			t.Fatalf("trial %d: construction changed execution", trial)
		}
		checkResultConsistency(t, y)
		views := set.Rules()
		for order, info := range views {
			view, _ := y.Rule(info.ID())
			if info.Order() != order || info.RegistrationIndex() != view.RegistrationIndex() || info.Priority() != view.Priority() || rules[info.RegistrationIndex()].ID() != info.ID() {
				t.Fatal("construction lost registration or compiled order")
			}
			if order > 0 {
				previous := views[order-1]
				if previous.Priority() < info.Priority() || previous.Priority() == info.Priority() && previous.RegistrationIndex() >= info.RegistrationIndex() {
					t.Fatal("order is not priority descending, registration ascending")
				}
			}
		}
		configured := mustReuse(t, set, options...)
		c := executionInput{value: initial}
		z, errZ := configured.Fire(context.Background(), &c)
		if !reflect.DeepEqual(z, x) || !reflect.DeepEqual(c, a) || fmt.Sprint(errZ) != fmt.Sprint(errX) {
			t.Fatal("engine defaults differ from per-call options")
		}
		c = executionInput{value: initial}
		traced, errZ := reused.Fire(context.Background(), &c, append(options, rulite.WithTrace())...)
		if traced.Explain().String() != x.Explain().String() || !reflect.DeepEqual(traced.Explain().Rules(), x.Explain().Rules()) || fmt.Sprint(errZ) != fmt.Sprint(errX) {
			t.Fatal("tracing changed construction parity")
		}
	}
}

func TestSharedRuleSetIndependentEngineDefaults(t *testing.T) {
	rules := []rulite.Rule[executionInput]{}
	for i := range 8 {
		rules = append(rules, rulite.NewRule[executionInput](rulite.RuleID(fmt.Sprintf("increment/%d", i))).Name("Increment").Tags("counter").When(func(_ context.Context, input *executionInput) (bool, error) { return input.value == i, nil }).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil }))
	}
	set := mustCompile(t, rules...)
	options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy()), rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)), rulite.WithTrace(), {}}
	first := mustReuse(t, set, options...)
	clear(options)
	all := mustReuse(t, set)
	retained, err := all.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	if err != nil {
		t.Fatal(err)
	}
	for _, concurrency := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("goroutines_%d", concurrency), func(t *testing.T) {
			var workers sync.WaitGroup
			for range concurrency {
				workers.Go(func() {
					for attempt := range 12 {
						engine, want, traced := all, 8, false
						if attempt%2 == 0 {
							engine, want, traced = first, 1, true
						}
						input := executionInput{}
						result, err := engine.Fire(context.Background(), &input)
						_, hasTrace := result.Trace()
						if err != nil || input.value != want || result.Counts().Fired != want || hasTrace != traced {
							t.Error("shared engines mixed defaults")
							return
						}
						input = executionInput{}
						result, err = engine.Fire(context.Background(), &input, rulite.WithPolicy(rulite.DefaultPolicy()))
						if err != nil || input.value != 8 || result.StopReason() != rulite.StopCompleted {
							t.Error("per-call policy failed to replace defaults")
							return
						}
						local := mustReuse(t, set)
						if _, err := local.Fire(context.Background(), &executionInput{}); err != nil {
							t.Error(err)
							return
						}
						for _, info := range set.Rules() {
							clear(info.Tags())
							indexed, ok := set.Rule(info.ID())
							x, _ := retained.Rule(info.ID())
							if !ok || indexed.Name() != "Increment" || x.Tags()[0] != "counter" {
								t.Error("metadata mutated concurrently")
								return
							}
							clear(x.Tags())
						}
						for _, x := range retained.Explain().Rules() {
							clear(x.Tags())
						}
						trace, _ := retained.Trace()
						for _, x := range trace.Rules() {
							clear(x.Tags())
						}
						checkResultConsistency(t, result)
					}
				})
			}
			workers.Wait()
		})
	}
}

func TestEnginePanicDefaultsAreIndependent(t *testing.T) {
	set := mustCompile(t, rulite.NewRule[int]("panic").When(func(context.Context, *int) (bool, error) { panic("condition unavailable") }).Then(func(context.Context, *int) error { return nil }))
	recovered := mustReuse(t, set)
	propagated := mustReuse(t, set, rulite.WithPanicMode(rulite.RecoverPanics), rulite.WithPanicMode(rulite.PropagatePanics))
	for _, engine := range []*rulite.Engine[int]{recovered, propagated} {
		result, err := engine.Fire(context.Background(), new(int), rulite.WithPanicMode(rulite.RecoverPanics))
		var panicErr *rulite.PanicError
		if !errors.As(err, &panicErr) || result.StopReason() != rulite.StopPanic {
			t.Fatal("panic override was not applied")
		}
	}
	func() {
		defer func() {
			if recover() != "condition unavailable" {
				t.Error("propagate default changed")
			}
		}()
		_, _ = propagated.Fire(context.Background(), new(int))
		t.Error("panic did not propagate")
	}()
	result, err := recovered.Fire(context.Background(), new(int))
	if err == nil || result.StopReason() != rulite.StopPanic {
		t.Fatal("another engine changed recover default")
	}
}
