package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
)

//go:embed rules.json
var pricingDefinitions []byte

// Price is caller-owned pricing state. Amounts use integer minor units.
type Price struct {
	// VIP marks customers eligible for the premium offer.
	VIP bool
	// Total is the amount in integer minor units.
	Total int64
	// Discount is the current integer percentage.
	Discount int
	// MaxDiscount caps a discount when positive.
	MaxDiscount int
	// AppliedBy identifies the rule that last wrote Discount.
	AppliedBy rulite.RuleID
	// Audit contains independently owned labels added by the audit action.
	Audit []string
}

type discountParams struct {
	Percent int `json:"percent"`
}
type auditParams struct {
	Labels []string `json:"labels"`
}

func pricingEngine(source []byte) (*rulite.Engine[Price], error) {
	conditions, err := cel.NewCompiler[Price]("input", cel.WithCostLimit(1000))
	if err != nil {
		return nil, err
	}
	actions := dynamic.NewRegistry[Price]()
	if err := actions.Register("pricing.vip_discount/v1", func(_ context.Context, p *Price, v discountParams) error {
		if p.Discount < v.Percent {
			p.Discount = v.Percent
			p.AppliedBy = "pricing/vip"
		}
		return nil
	}, func(v discountParams) error {
		if v.Percent < 1 || v.Percent > 100 {
			return errors.New("discount percent must be between 1 and 100")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := actions.Register[struct{}]("pricing.cap/v1", func(_ context.Context, p *Price, _ struct{}) error {
		p.Discount = p.MaxDiscount
		p.AppliedBy = "pricing/cap"
		return nil
	}); err != nil {
		return nil, err
	}
	if err := actions.Register("pricing.audit/v1", func(_ context.Context, p *Price, v auditParams) error {
		// Copy shared read-only parameters into caller-owned mutable state.
		p.Audit = append(p.Audit, v.Labels...)
		return nil
	}, func(v auditParams) error {
		if len(v.Labels) == 0 || len(v.Labels) > 4 {
			return errors.New("audit requires one to four labels")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := actions.Freeze(); err != nil {
		return nil, err
	}
	set, err := dynamic.CompileJSON(source, conditions, actions)
	if err != nil {
		return nil, err
	}
	return rulite.NewEngineFromRuleSet(set)
}

func main() {
	engine, err := pricingEngine(pricingDefinitions)
	if err != nil {
		log.Fatal(err)
	}
	price := Price{VIP: true, Total: 12000, MaxDiscount: 15}
	result, err := engine.Fire(context.Background(), &price, rulite.WithTrace())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Discount: %d%%; applied by: %s; audit: %v\n", price.Discount, price.AppliedBy, price.Audit)
	fmt.Print(result.Explain())
}
