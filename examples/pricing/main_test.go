package main

import (
	"context"
	"slices"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func TestPricing(t *testing.T) {
	engine, err := pricingEngine()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		input     Price
		discount  int
		appliedBy rulite.RuleID
		fired     int
	}{
		{"overlapping_offers", Price{VIP: true, NewCustomer: true, Currency: "USD", TotalCents: 120_000}, 20, "pricing/vip", 3},
		{"new_customer", Price{NewCustomer: true}, 10, "pricing/new-customer", 1},
		{"high_value_eur", Price{Currency: "EUR", TotalCents: 100_000}, 15, "pricing/high-value", 1},
		{"unsupported_currency", Price{Currency: "GBP", TotalCents: 100_000}, 0, "", 0},
		{"below_threshold", Price{Currency: "USD", TotalCents: 99_999}, 0, "", 0},
		{"blocked", Price{VIP: true, NewCustomer: true, Blocked: true, Currency: "USD", TotalCents: 120_000}, 0, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			result, err := engine.Fire(context.Background(), &input)
			if err != nil || input.Discount != tc.discount || input.AppliedBy != tc.appliedBy || result.Counts().Fired != tc.fired {
				t.Fatalf("price=%+v result=%+v error=%v", input, result.Counts(), err)
			}
			var ids []rulite.RuleID
			for _, view := range result.Explain().Rules() {
				ids = append(ids, view.ID())
				fromResult, ok := result.Rule(view.ID())
				if !ok || fromResult.State() != view.State() {
					t.Fatal("inconsistent explanation")
				}
			}
			if !slices.Equal(ids, []rulite.RuleID{"pricing/vip", "pricing/new-customer", "pricing/high-value"}) {
				t.Fatalf("order=%v", ids)
			}
		})
	}
}
