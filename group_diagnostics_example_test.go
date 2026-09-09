package rulite_test

import (
	"context"
	"fmt"

	"github.com/imbrooklyn/rulite"
)

func ExampleExplanation_Entries() {
	type Price struct{ Discount int }
	offer := func(id rulite.RuleID, discount int) rulite.Rule[Price] {
		return rulite.NewRule[Price](id).When(rulite.All[Price]()).Then(func(_ context.Context, price *Price) error { price.Discount = discount; return nil })
	}
	group := rulite.FirstMatchGroup("pricing/offers", offer("pricing/vip", 20), offer("pricing/base", 5))
	audit := rulite.NewRule[Price]("pricing/audit").When(func(_ context.Context, price *Price) (bool, error) { return price.Discount > 0, nil }).Then(func(context.Context, *Price) error { return nil })
	set, err := rulite.CompileEntries(group.Entry(), audit.Entry())
	if err != nil {
		panic(err)
	}
	var events []rulite.Event
	engine, err := rulite.NewEngineFromRuleSet(set, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		if _, ok := event.Group(); ok {
			events = append(events, event)
		}
		return nil
	})))
	if err != nil {
		panic(err)
	}
	result, err := engine.Fire(context.Background(), &Price{}, rulite.WithTrace())
	if err != nil {
		panic(err)
	}
	for _, entry := range result.Explain().Entries() {
		if rule, ok := entry.Rule(); ok {
			fmt.Printf("Rule %s: fired=%t\n", rule.ID(), rule.Fired())
			continue
		}
		group, _ := entry.Group()
		fmt.Printf("Group %s: end=%s, stop=%s\n", group.ID(), group.EndReason(), group.StopReason())
		for _, member := range entry.Members() {
			fmt.Printf("  %s: evaluated=%t\n", member.ID(), member.Evaluated())
		}
	}
	for _, event := range events {
		group, _ := event.Group()
		fmt.Printf("%s: end=%s\n", event.Kind(), group.EndReason())
	}
	trace, _ := result.Trace()
	groupView, _ := trace.Group("pricing/offers")
	fmt.Printf("Trace groups: %d; resolved=%t\n", len(trace.Groups()), groupView.Resolved())
	// Output:
	// Group pricing/offers: end=resolved, stop=none
	//   pricing/vip: evaluated=true
	//   pricing/base: evaluated=false
	// Rule pricing/audit: fired=true
	// group-resolved: end=none
	// group-finished: end=resolved
	// Trace groups: 1; resolved=true
}
