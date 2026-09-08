// Risk decisions use a conservative application default when evidence is unavailable.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/imbrooklyn/rulite"
)

type Assessment struct {
	Sanctioned, TrustedDevice, NewAccount, VelocityExceeded bool
	AmountCents                                             int64
	EvidenceError                                           error
	Decision                                                string
	DecidedBy                                               rulite.RuleID
}

func riskEngine() (*rulite.Engine[Assessment], error) {
	evidence := func(_ context.Context, a *Assessment) (bool, error) { return true, a.EvidenceError }
	sanctioned := func(_ context.Context, a *Assessment) (bool, error) { return a.Sanctioned, nil }
	trusted := func(_ context.Context, a *Assessment) (bool, error) { return a.TrustedDevice, nil }
	decide := func(id rulite.RuleID, priority rulite.Priority, condition rulite.Condition[Assessment], decision string) rulite.Rule[Assessment] {
		return rulite.NewRule[Assessment](id).Priority(priority).When(condition).Then(func(_ context.Context, a *Assessment) error { a.Decision, a.DecidedBy = decision, id; return nil })
	}
	reject := decide("risk/reject", 100, rulite.All(evidence, sanctioned), "reject")
	review := decide("risk/review", 50, rulite.All(evidence, rulite.Not(sanctioned), rulite.Any(
		func(_ context.Context, a *Assessment) (bool, error) { return a.VelocityExceeded, nil },
		rulite.All(rulite.Not(trusted), rulite.Any(
			func(_ context.Context, a *Assessment) (bool, error) { return a.NewAccount, nil },
			func(_ context.Context, a *Assessment) (bool, error) { return a.AmountCents >= 250_000, nil },
		)),
	)), "review")
	allow := decide("risk/allow", 0, rulite.All(evidence, rulite.Not(sanctioned)), "allow")
	return rulite.NewEngine(allow, review, reject)
}

func assess(ctx context.Context, engine *rulite.Engine[Assessment], input *Assessment) (rulite.Result, error) {
	// The application owns this fail-safe default. The default condition-error
	// policy stops execution, so unavailable evidence cannot reach the allow rule.
	input.Decision, input.DecidedBy = "review", ""
	policy := rulite.DefaultPolicy().WithStop(rulite.StopOnFirstMatch)
	return engine.Fire(ctx, input, rulite.WithPolicy(policy), rulite.WithTrace())
}

func main() {
	engine, err := riskEngine()
	if err != nil {
		log.Fatal(err)
	}
	for _, input := range []Assessment{
		{Sanctioned: true},
		{NewAccount: true, AmountCents: 300_000},
		{TrustedDevice: true},
		{EvidenceError: errors.New("screening evidence unavailable")},
	} {
		result, err := assess(context.Background(), engine, &input)
		fmt.Printf("Decision: %s; decided by: %s; error: %v\n", input.Decision, input.DecidedBy, err)
		fmt.Print(result.Explain())
		trace, _ := result.Trace()
		for _, rule := range trace.Rules() {
			if tree, ok := rule.ConditionTree(); ok {
				fmt.Printf("Trace %s: root outcome=%d, children=%d\n", rule.ID(), tree.Outcome(), len(tree.Children()))
			}
		}
	}
}
