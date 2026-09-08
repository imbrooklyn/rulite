// Pricing selects the best eligible discount and records its business provenance.
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
	return rulite.NewEngine(highValue, newcomer, vip)
}

func main() {
	engine, err := pricingEngine()
	if err != nil {
		log.Fatal(err)
	}
	price := Price{VIP: true, NewCustomer: true, Currency: "USD", TotalCents: 120_000}
	result, err := engine.Fire(context.Background(), &price)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Discount: %d%%; applied by: %s\n", price.Discount, price.AppliedBy)
	fmt.Printf("Successful actions: %v\n", result.Fired())
	fmt.Print(result.Explain())
}
