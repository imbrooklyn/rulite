package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func TestProviderFallback(t *testing.T) {
	engine, err := routingEngine()
	if err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("provider unavailable")
	for _, tc := range []struct {
		name     string
		input    Payment
		provider string
		attempts []string
		failures int
	}{
		{"stripe", Payment{Card: true}, "stripe", []string{"stripe"}, 0},
		{"adyen_fallback", Payment{Card: true, ProviderErrors: map[string]error{"stripe": unavailable}}, "adyen", []string{"stripe", "adyen"}, 1},
		{"paypal_fallback", Payment{Card: true, Wallet: true, ProviderErrors: map[string]error{"stripe": unavailable, "adyen": unavailable}}, "paypal", []string{"stripe", "adyen", "paypal"}, 2},
		{"bank_transfer", Payment{Bank: true}, "bank-transfer", []string{"bank-transfer"}, 0},
		{"exhausted", Payment{Card: true, Wallet: true, Bank: true, ProviderErrors: map[string]error{"stripe": unavailable, "adyen": unavailable, "paypal": unavailable, "bank-transfer": unavailable}}, "", []string{"stripe", "adyen", "paypal", "bank-transfer"}, 4},
		{"no_eligible_provider", Payment{}, "", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payment := tc.input
			result, err := route(context.Background(), engine, &payment)
			if payment.Provider != tc.provider || !slices.Equal(payment.Attempts, tc.attempts) || result.Counts().Failed != tc.failures || (err != nil) != (tc.failures > 0) || tc.failures > 0 && !errors.Is(err, unavailable) {
				t.Fatalf("payment=%+v counts=%+v error=%v", payment, result.Counts(), err)
			}
			if payment.Audited {
				t.Fatal("global selection unexpectedly executed audit action")
			}
			audit, _ := result.Rule("payment/audit")
			if tc.provider != "" {
				if result.StopReason() != rulite.StopFirstFire || result.Counts().Fired != 1 || audit.NotEvaluatedReason() != rulite.NotEvaluatedExecutionStopped {
					t.Fatal("successful provider did not stop globally")
				}
			} else if result.StopReason() != rulite.StopCompleted || result.Counts().Fired != 0 || audit.State() != rulite.RuleUnmatched {
				t.Fatal("exhausted routing did not complete")
			}
		})
	}
}
