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
		name      string
		input     Payment
		provider  string
		attempts  []string
		failures  int
		evaluated int
	}{
		{"stripe", Payment{Card: true}, "stripe", []string{"stripe"}, 0, 2},
		{"adyen_fallback", Payment{Card: true, ProviderErrors: map[string]error{"stripe": unavailable}}, "adyen", []string{"stripe", "adyen"}, 1, 3},
		{"paypal_fallback", Payment{Card: true, Wallet: true, ProviderErrors: map[string]error{"stripe": unavailable, "adyen": unavailable}}, "paypal", []string{"stripe", "adyen", "paypal"}, 2, 4},
		{"bank_transfer", Payment{Bank: true}, "bank-transfer", []string{"bank-transfer"}, 0, 5},
		{"bank_fallback", Payment{Card: true, Wallet: true, Bank: true, ProviderErrors: map[string]error{"stripe": unavailable, "adyen": unavailable, "paypal": unavailable}}, "bank-transfer", []string{"stripe", "adyen", "paypal", "bank-transfer"}, 3, 5},
		{"exhausted", Payment{Card: true, Wallet: true, Bank: true, ProviderErrors: map[string]error{"stripe": unavailable, "adyen": unavailable, "paypal": unavailable, "bank-transfer": unavailable}}, "", []string{"stripe", "adyen", "paypal", "bank-transfer"}, 4, 5},
		{"no_eligible_provider", Payment{}, "", nil, 0, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payment := tc.input
			result, err := route(context.Background(), engine, &payment)
			if payment.Provider != tc.provider || !slices.Equal(payment.Attempts, tc.attempts) || result.Counts().Failed != tc.failures || (err != nil) != (tc.failures > 0) || tc.failures > 0 && !errors.Is(err, unavailable) {
				t.Fatalf("payment=%+v counts=%+v error=%v", payment, result.Counts(), err)
			}
			if payment.Audited != (tc.provider != "") || result.StopReason() != rulite.StopCompleted || result.Counts().Total != 5 || result.Evaluated() != tc.evaluated {
				t.Fatal("local selection lost the audit or execution counts")
			}
			group, ok := result.Group("payment/providers")
			selected, selectedOK := group.SelectedRule()
			if !ok || !group.Entered() || group.StopReason() != rulite.StopNone || selectedOK != (tc.provider != "") {
				t.Fatal("provider group lost its local outcome")
			}
			audit, _ := result.Rule("payment/audit")
			if tc.provider != "" {
				if selected != rulite.RuleID("payment/"+tc.provider) || group.EndReason() != rulite.GroupEndResolved || result.Counts().Fired != 2 || !audit.Fired() {
					t.Fatal("successful provider did not resolve locally before audit")
				}
			} else if group.State() != rulite.GroupExhausted || group.EndReason() != rulite.GroupEndExhausted || result.Counts().Fired != 0 || audit.State() != rulite.RuleUnmatched {
				t.Fatal("exhausted routing did not complete")
			}
			for _, member := range result.Explain().Entries()[0].Members() {
				if !member.Evaluated() && member.NotEvaluatedReason() != rulite.NotEvaluatedGroupResolved {
					t.Fatal("unused provider lost its local reason")
				}
			}
			for i, failure := range result.Failures() {
				if failure.RuleID() != rulite.RuleID("payment/"+tc.attempts[i]) || failure.Phase() != rulite.ActionPhase || !failure.Continued() {
					t.Fatal("fallback erased or reordered failed attempts")
				}
			}
		})
	}
}

func TestGlobalSelectionStopsBeforeAudit(t *testing.T) {
	engine, err := routingEngine()
	if err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("provider unavailable")
	for _, stop := range []rulite.StopMode{rulite.StopOnFirstMatch, rulite.StopOnFirstFire} {
		input := Payment{Card: true, Wallet: true, Bank: true, ProviderErrors: map[string]error{"stripe": unavailable}}
		policy := rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError).WithStop(stop)
		result, err := engine.Fire(context.Background(), &input, rulite.WithPolicy(policy))
		wantAttempts, wantProvider, wantStop := []string{"stripe"}, "", rulite.StopFirstMatch
		if stop == rulite.StopOnFirstFire {
			wantAttempts, wantProvider, wantStop = []string{"stripe", "adyen"}, "adyen", rulite.StopFirstFire
		}
		audit, _ := result.Rule("payment/audit")
		group, _ := result.Group("payment/providers")
		if !errors.Is(err, unavailable) || !slices.Equal(input.Attempts, wantAttempts) || input.Provider != wantProvider || input.Audited || audit.NotEvaluatedReason() != rulite.NotEvaluatedExecutionStopped || result.StopReason() != wantStop || group.EndReason() != rulite.GroupEndExecutionStopped || group.StopReason() != wantStop || group.Resolved() != (wantProvider != "") {
			t.Fatal("global selection became local advancement")
		}
	}
}

func TestDefaultErrorPolicyStopsProviderFallback(t *testing.T) {
	engine, err := routingEngine()
	if err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("provider unavailable")
	input := Payment{Card: true, ProviderErrors: map[string]error{"stripe": unavailable}}
	result, err := engine.Fire(context.Background(), &input, rulite.WithPolicy(rulite.DefaultPolicy()))
	audit, _ := result.Rule("payment/audit")
	if !errors.Is(err, unavailable) || !slices.Equal(input.Attempts, []string{"stripe"}) || input.Provider != "" || input.Audited || result.StopReason() != rulite.StopActionError || result.Failures()[0].Continued() || audit.Evaluated() {
		t.Fatal("default error stop allowed another provider or audit")
	}
}
