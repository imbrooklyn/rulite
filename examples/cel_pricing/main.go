package main

import (
	"context"
	"fmt"
	"log"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
)

// Price is caller-owned business state. Monetary values use integer minor units.
type Price struct {
	// VIP marks customers eligible for the premium offer.
	VIP bool
	// Total is the order amount in integer minor units.
	Total int64
	// Discount is the currently applied percentage.
	Discount int
	// MaxDiscount caps the percentage when positive.
	MaxDiscount int
	// AppliedBy identifies the rule that last changed Discount.
	AppliedBy rulite.RuleID
	// Audited records completion of the ordinary Go audit action.
	Audited bool
}

func pricingEngine() (*rulite.Engine[Price], error) {
	compiler, err := cel.NewCompiler[Price]("input")
	if err != nil {
		return nil, err
	}
	eligible, err := compiler.Compile("input.VIP && input.Total >= 10000")
	if err != nil {
		return nil, err
	}
	needsCap, err := compiler.Compile("input.MaxDiscount > 0 && input.Discount > input.MaxDiscount")
	if err != nil {
		return nil, err
	}
	vip := rulite.NewRule[Price]("pricing/vip").Priority(100).When(eligible).
		Then(func(_ context.Context, p *Price) error {
			if p.Discount < 20 {
				p.Discount = 20
				p.AppliedBy = "pricing/vip"
			}
			return nil
		})
	cap := rulite.NewRule[Price]("pricing/cap").When(needsCap).
		Then(func(_ context.Context, p *Price) error {
			p.Discount = p.MaxDiscount
			p.AppliedBy = "pricing/cap"
			return nil
		})
	audit := rulite.NewRule[Price]("pricing/audit").Priority(-100).
		When(func(_ context.Context, p *Price) (bool, error) { return p.Discount > 0, nil }).
		Then(func(_ context.Context, p *Price) error { p.Audited = true; return nil })
	return rulite.NewEngine(vip, cap, audit)
}

func main() {
	engine, err := pricingEngine()
	if err != nil {
		log.Fatal(err)
	}
	price := Price{VIP: true, Total: 12000, MaxDiscount: 15}
	result, err := engine.Fire(context.Background(), &price, rulite.WithTrace())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Discount: %d%%; applied by: %s; audited: %t\n", price.Discount, price.AppliedBy, price.Audited)
	fmt.Print(result.Explain())
}
