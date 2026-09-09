package rulite_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/imbrooklyn/rulite"
)

func ExampleCompileEntries() {
	type Payment struct {
		Attempts  int
		AppliedBy rulite.RuleID
		Audited   bool
	}
	unavailable := errors.New("primary provider unavailable")
	provider := func(id rulite.RuleID, failure error) rulite.Rule[Payment] {
		return rulite.NewRule[Payment](id).When(rulite.All[Payment]()).Then(func(_ context.Context, p *Payment) error {
			p.Attempts++ // Failed attempts can already have external effects.
			if failure != nil {
				return failure
			}
			p.AppliedBy = id
			return nil
		})
	}
	primary := provider("payment/primary", unavailable)
	backup := provider("payment/backup", nil)
	unused := provider("payment/unused", nil)
	providers := rulite.FirstFireGroup("payment/providers", primary, backup, unused)
	audit := rulite.NewRule[Payment]("payment/audit").When(func(_ context.Context, p *Payment) (bool, error) {
		return p.AppliedBy != "", nil
	}).Then(func(_ context.Context, p *Payment) error { p.Audited = true; return nil })
	set, err := rulite.CompileEntries(providers.Entry(), audit.Entry())
	if err != nil {
		panic(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set,
		rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)))
	if err != nil {
		panic(err)
	}
	payment := Payment{}
	result, err := engine.Fire(context.Background(), &payment)
	group, _ := result.Group("payment/providers")
	selected, _ := group.SelectedRule()
	member, _ := result.Rule("payment/unused")
	fmt.Printf("Selected: %s; attempts: %d; audited: %t\n", selected, payment.Attempts, payment.Audited)
	fmt.Printf("Group: %s; execution: %s; earlier error retained: %t\n", group.State(), result.StopReason(), errors.Is(err, unavailable))
	fmt.Printf("Unused evaluated: %t; group resolved: %t\n", member.Evaluated(), member.NotEvaluatedReason() == rulite.NotEvaluatedGroupResolved)
	// Output:
	// Selected: payment/backup; attempts: 2; audited: true
	// Group: resolved; execution: completed; earlier error retained: true
	// Unused evaluated: false; group resolved: true
}
