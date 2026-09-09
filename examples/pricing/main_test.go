package main

import (
	"context"
	"fmt"
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
		selected  rulite.RuleID
	}{
		{"overlapping_offers", Price{VIP: true, NewCustomer: true, Currency: "USD", TotalCents: 120_000}, 20, "pricing/vip", 2, "pricing/vip"},
		{"new_customer", Price{NewCustomer: true}, 10, "pricing/new-customer", 2, "pricing/new-customer"},
		{"priority_precedes_discount_amount", Price{NewCustomer: true, Currency: "EUR", TotalCents: 100_000}, 10, "pricing/new-customer", 2, "pricing/new-customer"},
		{"high_value_eur", Price{Currency: "EUR", TotalCents: 100_000}, 15, "pricing/high-value", 2, "pricing/high-value"},
		{"unsupported_currency", Price{Currency: "GBP", TotalCents: 100_000}, 0, "", 1, ""},
		{"below_threshold", Price{Currency: "USD", TotalCents: 99_999}, 0, "", 1, ""},
		{"blocked", Price{VIP: true, NewCustomer: true, Blocked: true, Currency: "USD", TotalCents: 120_000}, 0, "", 0, ""},
		{"later_cap_owns_final_value", Price{VIP: true, MaxDiscount: 15}, 15, "pricing/cap", 3, "pricing/vip"},
		{"successful_offer_preserves_stronger_value", Price{VIP: true, Discount: 30, AppliedBy: "pricing/manual"}, 30, "pricing/manual", 2, "pricing/vip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			result, err := engine.Fire(context.Background(), &input, rulite.WithTrace())
			if err != nil || input.Discount != tc.discount || input.AppliedBy != tc.appliedBy || result.Counts().Fired != tc.fired {
				t.Fatalf("price=%+v result=%+v error=%v", input, result.Counts(), err)
			}
			wantAudit := ""
			if !input.Blocked {
				wantAudit = fmt.Sprintf("Discount: %d%%; applied by: %s", tc.discount, tc.appliedBy)
			}
			group, _ := result.Group("pricing/offers")
			selected, ok := group.SelectedRule()
			if input.Audit != wantAudit || result.StopReason() != rulite.StopCompleted || selected != tc.selected || ok != (tc.selected != "") {
				t.Fatal("local selection or audit lost business provenance")
			}
			wantEnd := rulite.GroupEndExhausted
			if tc.selected != "" {
				wantEnd = rulite.GroupEndResolved
			}
			if group.EndReason() != wantEnd || group.StopReason() != rulite.StopNone || result.Counts().Total != 5 {
				t.Fatal("group end or executable count changed")
			}
			var ids []rulite.RuleID
			for _, view := range result.Explain().Rules() {
				ids = append(ids, view.ID())
				fromResult, ok := result.Rule(view.ID())
				if !ok || fromResult.State() != view.State() {
					t.Fatal("inconsistent explanation")
				}
			}
			if !slices.Equal(ids, []rulite.RuleID{"pricing/vip", "pricing/new-customer", "pricing/high-value", "pricing/cap", "pricing/audit"}) {
				t.Fatalf("order=%v", ids)
			}
			trace, _ := result.Trace()
			for _, rule := range trace.Rules() {
				if !rule.Evaluated() && (rule.NotEvaluatedReason() != rulite.NotEvaluatedGroupResolved || rule.ConditionDuration() != 0 || rule.ActionDuration() != 0) {
					t.Fatal("uncalled offer acquired observations")
				}
			}
		})
	}
}

func TestGlobalFirstMatchStopsBeforeCapAndAudit(t *testing.T) {
	engine, err := pricingEngine()
	if err != nil {
		t.Fatal(err)
	}
	input := Price{VIP: true, MaxDiscount: 15}
	result, err := engine.Fire(context.Background(), &input, rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstMatch)))
	if err != nil || result.StopReason() != rulite.StopFirstMatch || input.Discount != 20 || input.AppliedBy != "pricing/vip" || input.Audit != "" || result.Evaluated() != 1 {
		t.Fatal("global selection continued into later business rules")
	}
	for _, id := range []rulite.RuleID{"pricing/new-customer", "pricing/high-value", "pricing/cap", "pricing/audit"} {
		rule, _ := result.Rule(id)
		if rule.NotEvaluatedReason() != rulite.NotEvaluatedExecutionStopped {
			t.Fatal("global stop became a local group hole")
		}
	}
}
