package rulite_test

import (
	"context"
	"testing"

	"github.com/imbrooklyn/rulite"
)

type pricingState struct {
	total    int
	discount int
}

func eligible(_ context.Context, state *pricingState) (bool, error) {
	return state.total >= 100, nil
}

func applyDiscount(_ context.Context, state *pricingState) error {
	state.discount = 10
	return nil
}

func TestStagedBuilder(t *testing.T) {
	var condition rulite.Condition[pricingState] = eligible
	var action rulite.Action[pricingState] = applyDiscount
	var builder rulite.RuleBuilder[pricingState] = rulite.NewRule[pricingState]("pricing/discount")
	var actionBuilder rulite.ActionBuilder[pricingState] = builder.Priority(100).When(condition)
	var rule rulite.Rule[pricingState] = actionBuilder.Then(action)
	var engine *rulite.Engine[pricingState]
	var err error
	engine, err = rulite.NewEngine(rule)
	if err != nil || engine == nil {
		t.Fatalf("NewEngine() = %v, %v; want a valid engine", engine, err)
	}
	if rule.ID() != "pricing/discount" || rule.Priority() != 100 {
		t.Fatalf("rule getters = %q, %d; want pricing/discount, 100", rule.ID(), rule.Priority())
	}

	chained := rulite.NewRule[pricingState]("pricing/chained").
		Priority(50).
		When(rulite.All(eligible, rulite.Not(rulite.Any(
			func(_ context.Context, state *pricingState) (bool, error) {
				return state.discount > 0, nil
			},
		)))).
		Then(applyDiscount)
	if _, err := rulite.NewEngine(rule, chained); err != nil {
		t.Fatalf("NewEngine() with composed conditions: %v", err)
	}
}

func TestRuleAndBuilderValueSemantics(t *testing.T) {
	base := rulite.NewRule[pricingState]("pricing/discount")
	high := base.Priority(100)
	low := high.Priority(-10)
	stage := high.When(eligible)
	original := stage.Then(applyDiscount)
	copyOfRule := original

	cases := []struct {
		name string
		rule rulite.Rule[pricingState]
		want rulite.Priority
	}{
		{"default", base.When(eligible).Then(applyDiscount), rulite.DefaultPriority},
		{"high", original, 100},
		{"last_priority_wins", low.When(eligible).Then(applyDiscount), -10},
		{"reused_action_stage", stage.Then(nil), 100},
		{"copied_rule", copyOfRule, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rule.ID() != "pricing/discount" || tc.rule.Priority() != tc.want {
				t.Fatalf("rule getters = %q, %d; want pricing/discount, %d", tc.rule.ID(), tc.rule.Priority(), tc.want)
			}
		})
	}
	original = rulite.Rule[pricingState]{}
	if copyOfRule.ID() != "pricing/discount" || copyOfRule.Priority() != 100 {
		t.Fatal("replacing a rule changed its copy")
	}
	if original.ID() != "" || original.Priority() != rulite.DefaultPriority {
		t.Fatal("zero rule has unexpected getters")
	}
	if _, err := rulite.NewEngine(copyOfRule); err != nil {
		t.Fatalf("reusing the action stage changed the completed rule: %v", err)
	}
}

func TestInvalidBuilderDataIsDeferred(t *testing.T) {
	var zeroBuilder rulite.RuleBuilder[pricingState]
	var zeroActionBuilder rulite.ActionBuilder[pricingState]
	rules := []rulite.Rule[pricingState]{
		rulite.NewRule[pricingState]("").When(nil).Then(nil),
		zeroBuilder.When(eligible).Then(applyDiscount),
		zeroActionBuilder.Then(applyDiscount),
		{},
	}
	for index, rule := range rules {
		if engine, err := rulite.NewEngine(rule); engine != nil || err == nil {
			t.Errorf("rule %d: NewEngine() = %v, %v; want nil engine and validation error", index, engine, err)
		}
	}
}

func TestEmptyEngine(t *testing.T) {
	engine, err := rulite.NewEngine[pricingState]()
	if engine == nil || err != nil {
		t.Fatalf("NewEngine[pricingState]() = %v, %v; want a valid empty engine", engine, err)
	}
}
