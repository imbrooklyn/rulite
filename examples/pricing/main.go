// Pricing selects the highest-priority eligible offer and records field provenance.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/imbrooklyn/rulite"
)

type Price struct {
	VIP, NewCustomer, Blocked bool
	Currency                  string
	TotalCents                int64
	Discount                  int
	AppliedBy                 rulite.RuleID
	// MaxDiscount optionally caps the selected or preexisting discount; zero means no cap.
	MaxDiscount int
	// Audit records the final discount and its field writer after local selection.
	Audit string
}

func pricingEngine() (*rulite.Engine[Price], error) {
	notBlocked := rulite.Not(func(_ context.Context, p *Price) (bool, error) { return p.Blocked, nil })
	offer := func(id rulite.RuleID, priority rulite.Priority, condition rulite.Condition[Price], discount int) rulite.Rule[Price] {
		return rulite.NewRule[Price](id).Priority(priority).When(rulite.All(notBlocked, condition)).Then(func(_ context.Context, p *Price) error {
			// Fired proves a successful return. AppliedBy changes only when this field changes.
			if discount > p.Discount {
				p.Discount, p.AppliedBy = discount, id
			}
			return nil
		})
	}
	vip := offer("pricing/vip", 100, func(_ context.Context, p *Price) (bool, error) { return p.VIP, nil }, 20)
	newcomer := offer("pricing/new-customer", 80, func(_ context.Context, p *Price) (bool, error) { return p.NewCustomer, nil }, 10)
	highValue := offer("pricing/high-value", 50, rulite.All(
		func(_ context.Context, p *Price) (bool, error) { return p.TotalCents >= 100_000, nil },
		rulite.Any(
			func(_ context.Context, p *Price) (bool, error) { return p.Currency == "USD", nil },
			func(_ context.Context, p *Price) (bool, error) { return p.Currency == "EUR", nil },
		),
	), 15)
	offers := rulite.FirstMatchGroup("pricing/offers", highValue, newcomer, vip).WithPriority(100)
	capDiscount := rulite.NewRule[Price]("pricing/cap").When(rulite.All(notBlocked,
		func(_ context.Context, p *Price) (bool, error) {
			return p.MaxDiscount > 0 && p.Discount > p.MaxDiscount, nil
		},
	)).Then(func(_ context.Context, p *Price) error {
		p.Discount, p.AppliedBy = p.MaxDiscount, "pricing/cap"
		return nil
	})
	audit := rulite.NewRule[Price]("pricing/audit").Priority(-100).When(notBlocked).Then(func(_ context.Context, p *Price) error {
		p.Audit = fmt.Sprintf("Discount: %d%%; applied by: %s", p.Discount, p.AppliedBy)
		return nil
	})
	set, err := rulite.CompileEntries(audit.Entry(), capDiscount.Entry(), offers.Entry())
	if err != nil {
		return nil, err
	}
	return rulite.NewEngineFromRuleSet(set)
}

func main() {
	engine, err := pricingEngine()
	if err != nil {
		log.Fatal(err)
	}
	price := Price{VIP: true, NewCustomer: true, Currency: "USD", TotalCents: 120_000, MaxDiscount: 15}
	result, err := engine.Fire(context.Background(), &price)
	if err != nil {
		log.Fatal(err)
	}
	group, _ := result.Group("pricing/offers")
	selected, _ := group.SelectedRule()
	fmt.Printf("Selected offer: %s\n", selected)
	fmt.Println(price.Audit)
	fmt.Printf("Successful actions: %v\n", result.Fired())
	fmt.Print(result.Explain())
}
