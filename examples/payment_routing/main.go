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
	return rulite.NewEngine(bank, paypal, adyen, stripe, audit)
}

func route(ctx context.Context, engine *rulite.Engine[Payment], payment *Payment) (rulite.Result, error) {
	// This example uses global selection, which stops before the audit rule.
	// For an audit within the same Fire, use FirstFireGroup with CompileEntries
	// and retain EvaluateAll as the global stop mode.
	policy := rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire).WithActionErrors(rulite.ContinueOnError)
	return engine.Fire(ctx, payment, rulite.WithPolicy(policy))
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
	fmt.Printf("Selected provider: %s; attempts: %v; audit rule fired: %t\n", payment.Provider, payment.Attempts, payment.Audited)
	fmt.Print(result.Explain())
}
