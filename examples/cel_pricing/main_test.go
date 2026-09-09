package main

import (
	"context"
	"testing"
)

func TestPricing(t *testing.T) {
	engine, err := pricingEngine()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		input           Price
		discount, fired int
		writer          string
		audited         bool
	}{
		{"vip", Price{VIP: true, Total: 12000}, 20, 2, "pricing/vip", true},
		{"cap", Price{VIP: true, Total: 12000, MaxDiscount: 15}, 15, 3, "pricing/cap", true},
		{"miss", Price{Total: 12000}, 0, 0, "", false},
		{"preserve", Price{VIP: true, Total: 12000, Discount: 30, AppliedBy: "pricing/previous"}, 30, 2, "pricing/previous", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := engine.Fire(context.Background(), &tc.input)
			if err != nil || result.Counts().Fired != tc.fired || tc.input.Discount != tc.discount || string(tc.input.AppliedBy) != tc.writer || tc.input.Audited != tc.audited {
				t.Fatalf("pricing result: %+v, %v", tc.input, err)
			}
		})
	}
}
