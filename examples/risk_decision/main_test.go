package main

import (
	"context"
	"errors"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func TestRiskDecisions(t *testing.T) {
	engine, err := riskEngine()
	if err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("screening evidence unavailable")
	for _, tc := range []struct {
		name      string
		input     Assessment
		decision  string
		id        rulite.RuleID
		evaluated int
	}{
		{"reject_precedes_review", Assessment{Sanctioned: true, VelocityExceeded: true}, "reject", "risk/reject", 1},
		{"velocity_review", Assessment{TrustedDevice: true, VelocityExceeded: true}, "review", "risk/review", 2},
		{"new_account_review", Assessment{NewAccount: true}, "review", "risk/review", 2},
		{"high_value_review", Assessment{AmountCents: 250_000}, "review", "risk/review", 2},
		{"trusted_allow", Assessment{TrustedDevice: true, NewAccount: true, AmountCents: 300_000}, "allow", "risk/allow", 3},
		{"ordinary_allow", Assessment{}, "allow", "risk/allow", 3},
		{"missing_evidence", Assessment{EvidenceError: unavailable, Decision: "allow", DecidedBy: "risk/allow"}, "review", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			result, err := assess(context.Background(), engine, &input)
			if !errors.Is(err, input.EvidenceError) || input.Decision != tc.decision || input.DecidedBy != tc.id || result.Evaluated() != tc.evaluated {
				t.Fatalf("assessment=%+v result=%+v error=%v", input, result.Counts(), err)
			}
			trace, ok := result.Trace()
			if !ok || len(trace.Rules()) != 3 {
				t.Fatal("missing complete trace")
			}
			for _, rule := range trace.Rules() {
				fromResult, _ := result.Rule(rule.ID())
				if rule.State() != fromResult.State() || rule.Matched() != fromResult.Matched() {
					t.Fatal("trace differs from result")
				}
				if rule.Evaluated() {
					if _, ok := rule.ConditionTree(); !ok {
						t.Fatal("missing nested condition evidence")
					}
				}
			}
			if input.EvidenceError != nil {
				allow, _ := result.Rule("risk/allow")
				failures := result.Failures()
				if result.StopReason() != rulite.StopConditionError || result.Counts().Fired != 0 || allow.Evaluated() || len(failures) != 1 || failures[0].Continued() || failures[0].Phase() != rulite.ConditionPhase {
					t.Fatal("unavailable evidence bypassed fail-safe policy")
				}
			} else if result.StopReason() != rulite.StopFirstMatch || result.Counts().Fired != 1 {
				t.Fatal("decision was not exclusive")
			}
		})
	}
}
