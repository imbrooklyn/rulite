package rulite_test

import (
	"context"
	"fmt"

	"github.com/imbrooklyn/rulite"
)

type Price struct {
	VIP      bool
	Discount int
}

func ExampleNewRule() {
	vip := rulite.NewRule[Price]("pricing/vip").Priority(100).
		When(func(_ context.Context, p *Price) (bool, error) {
			return p.VIP, nil
		}).Then(func(_ context.Context, p *Price) error {
		p.Discount = 20
		return nil
	})
	engine, err := rulite.NewEngine(vip)
	if err != nil {
		panic(err)
	}
	price := Price{VIP: true}
	result, err := engine.Fire(context.Background(), &price)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Discount: %d%%\n", price.Discount)
	fmt.Print(result.Explain())
	// Output:
	// Discount: 20%
	// execution: completed
	// summary: total=1 evaluated=1 not-evaluated=0 matched=1 unmatched=0 fired=1 skipped=0 failed=0 condition-failed=0 action-failed=0 panic-recovered=0
	//
	// rule pricing/vip order=0 registration-index=0 priority=100
	//   evaluated=true matched=true action-started=true action-success=true state=fired
}
