package rulite_test

import (
	"context"
	"fmt"

	"github.com/imbrooklyn/rulite"
)

func ExampleCompile() {
	type Price struct {
		VIP       bool
		Discount  int
		AppliedBy rulite.RuleID
	}
	offer := func(id rulite.RuleID, priority rulite.Priority, discount int) rulite.Rule[Price] {
		return rulite.NewRule[Price](id).Priority(priority).Name("VIP offer").Tags("pricing", "loyalty").
			When(func(_ context.Context, p *Price) (bool, error) { return p.VIP, nil }).
			Then(func(_ context.Context, p *Price) error {
				if discount > p.Discount {
					p.Discount, p.AppliedBy = discount, id
				}
				return nil
			})
	}
	vip, standard := offer("pricing/vip", 100, 20), offer("pricing/standard", 0, 10)
	convenient, err := rulite.NewEngine(standard, vip)
	if err != nil {
		panic(err)
	}
	set, err := rulite.Compile(standard, vip)
	if err != nil {
		panic(err)
	}
	all, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		panic(err)
	}
	first, err := rulite.NewEngineFromRuleSet(set,
		rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)))
	if err != nil {
		panic(err)
	}
	for _, engine := range []*rulite.Engine[Price]{convenient, all, first} {
		price := Price{VIP: true}
		result, err := engine.Fire(context.Background(), &price)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Discount: %d%%; applied by: %s; fired: %v\n", price.Discount, price.AppliedBy, result.Fired())
	}
	info, _ := set.Rule("pricing/vip")
	fmt.Printf("%s: %s; tags: %v; order: %d; registration: %d\n", info.ID(), info.Name(), info.Tags(), info.Order(), info.RegistrationIndex())
	// Output:
	// Discount: 20%; applied by: pricing/vip; fired: [pricing/vip pricing/standard]
	// Discount: 20%; applied by: pricing/vip; fired: [pricing/vip pricing/standard]
	// Discount: 20%; applied by: pricing/vip; fired: [pricing/vip]
	// pricing/vip: VIP offer; tags: [pricing loyalty]; order: 0; registration: 1
}
