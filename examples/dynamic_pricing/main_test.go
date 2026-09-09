package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/imbrooklyn/rulite/dynamic"
)

func TestDynamicPricing(t *testing.T) {
	engine, err := pricingEngine(pricingDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		input           Price
		discount, fired int
		writer          string
	}{
		{"vip", Price{VIP: true, Total: 12000}, 20, 2, "pricing/vip"},
		{"cap", Price{VIP: true, Total: 12000, MaxDiscount: 15}, 15, 3, "pricing/cap"},
		{"miss", Price{Total: 12000}, 0, 0, ""},
		{"preserve", Price{VIP: true, Total: 12000, Discount: 30, AppliedBy: "pricing/previous"}, 30, 2, "pricing/previous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := engine.Fire(context.Background(), &tc.input)
			if err != nil || tc.input.Discount != tc.discount || result.Counts().Fired != tc.fired || string(tc.input.AppliedBy) != tc.writer {
				t.Fatalf("unexpected pricing outcome: %+v %v", tc.input, err)
			}
			if tc.discount > 0 {
				if len(tc.input.Audit) != 1 || tc.input.Audit[0] != "pricing-reviewed" {
					t.Fatal("audit missing")
				}
				tc.input.Audit[0] = "caller edit"
			}
		})
	}
	for _, tc := range []struct {
		source []byte
		cause  error
	}{
		{bytes.ReplaceAll(pricingDefinitions, []byte(`"percent": 20`), []byte(`"percent": 101`)), dynamic.ErrInvalidParams},
		{bytes.ReplaceAll(pricingDefinitions, []byte(`pricing.vip_discount/v1`), []byte(`pricing.unregistered/v1`)), dynamic.ErrUnknownAction},
	} {
		if engine, err := pricingEngine(tc.source); engine != nil || !errors.Is(err, tc.cause) {
			t.Fatal("invalid configuration published an engine")
		}
	}
}
