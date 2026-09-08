package rulite_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/imbrooklyn/rulite"
)

func ExampleWithObserver() {
	type Price struct {
		VIP      bool
		Discount int
	}
	vip := rulite.NewRule[Price]("pricing/vip").Name("VIP discount").
		When(func(_ context.Context, price *Price) (bool, error) { return price.VIP, nil }).
		Then(func(_ context.Context, price *Price) error { price.Discount = 20; return nil })
	set, err := rulite.Compile(vip)
	if err != nil {
		panic(err)
	}
	// This collector is used sequentially. Shared collectors need synchronization.
	var events []rulite.Event
	exportErr := errors.New("export unavailable")
	observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		events = append(events, event)
		if event.Kind() == rulite.EventExecutionFinished {
			return exportErr
		}
		return nil
	})
	engine, err := rulite.NewEngineFromRuleSet(set, rulite.WithObserver(observer))
	if err != nil {
		panic(err)
	}
	price := Price{VIP: true}
	result, err := engine.Fire(context.Background(), &price)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Discount: %d%%; fired: %v\n", price.Discount, result.Fired())
	for _, event := range events {
		if event.Kind() == rulite.EventRuleFired {
			info, _ := event.Rule()
			fmt.Printf("Observed: %s (%s)\n", info.ID(), info.Name())
		}
	}
	for _, diagnostic := range result.Diagnostics() {
		fmt.Printf("Diagnostic at %s; export error: %t\n", diagnostic.Event().Kind(), errors.Is(diagnostic, exportErr))
	}
	// Output:
	// Discount: 20%; fired: [pricing/vip]
	// Observed: pricing/vip (VIP discount)
	// Diagnostic at execution-finished; export error: true
}
