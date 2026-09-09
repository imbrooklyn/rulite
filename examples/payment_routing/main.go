// Payment routing demonstrates provider fallback using deterministic local stubs.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/imbrooklyn/rulite"
)

type Payment struct {
	Card, Wallet, Bank bool
	Provider           string
	Attempts           []string
	ProviderErrors     map[string]error
	Audited            bool
}

func routingEngine() (*rulite.Engine[Payment], error) {
	provider := func(name string, priority rulite.Priority, eligible rulite.Condition[Payment]) rulite.Rule[Payment] {
		return rulite.NewRule[Payment](rulite.RuleID("payment/" + name)).Priority(priority).When(eligible).Then(func(_ context.Context, p *Payment) error {
			// Replace this local stub with an idempotent provider operation. A failed
			// attempt can already have side effects; the engine never rolls them back.
			p.Attempts = append(p.Attempts, name)
			if err := p.ProviderErrors[name]; err != nil {
				return err
			}
			p.Provider = name
			return nil
		})
	}
	card := func(_ context.Context, p *Payment) (bool, error) { return p.Card, nil }
	stripe := provider("stripe", 100, card)
	adyen := provider("adyen", 80, card)
	paypal := provider("paypal", 60, func(_ context.Context, p *Payment) (bool, error) { return p.Wallet, nil })
	bank := provider("bank-transfer", 0, func(_ context.Context, p *Payment) (bool, error) { return p.Bank, nil })
	audit := rulite.NewRule[Payment]("payment/audit").Priority(-100).When(func(_ context.Context, p *Payment) (bool, error) { return p.Provider != "", nil }).Then(func(_ context.Context, p *Payment) error { p.Audited = true; return nil })
	providers := rulite.FirstFireGroup("payment/providers", bank, paypal, adyen, stripe)
	set, err := rulite.CompileEntries(audit.Entry(), providers.Entry())
	if err != nil {
		return nil, err
	}
	return rulite.NewEngineFromRuleSet(set,
		rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)))
}

func route(ctx context.Context, engine *rulite.Engine[Payment], payment *Payment) (rulite.Result, error) {
	// Local provider selection leaves EvaluateAll free to reach the audit rule.
	return engine.Fire(ctx, payment)
}

func main() {
	engine, err := routingEngine()
	if err != nil {
		log.Fatal(err)
	}
	unavailable := errors.New("Stripe unavailable")
	payment := Payment{Card: true, Wallet: true, Bank: true, ProviderErrors: map[string]error{"stripe": unavailable}}
	result, err := route(context.Background(), engine, &payment)
	// Fallback success does not erase earlier errors. Inspect both the result and error.
	if err != nil {
		fmt.Printf("Observed errors: %v\n", err)
	}
	group, _ := result.Group("payment/providers")
	audit, _ := result.Rule("payment/audit")
	fmt.Printf("Selected provider: %s; attempts: %v; audit rule fired: %t\n", payment.Provider, payment.Attempts, audit.Fired())
	fmt.Printf("Provider group: %s; execution: %s\n", group.EndReason(), result.StopReason())
	fmt.Print(result.Explain())
}
