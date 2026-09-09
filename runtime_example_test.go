package rulite_test

import (
	"context"
	"fmt"

	"github.com/imbrooklyn/rulite"
)

func ExampleRuntime() {
	type Price struct{ Discount int }
	build := func(version rulite.RuleSetVersion, discount int) (*rulite.Engine[Price], error) {
		rule := rulite.NewRule[Price]("pricing/offer").
			When(func(context.Context, *Price) (bool, error) { return true, nil }).
			Then(func(_ context.Context, p *Price) error { p.Discount = discount; return nil })
		set, err := rulite.Compile(rule)
		if err != nil {
			return nil, err
		}
		set, err = set.WithIdentity(version, "")
		if err != nil {
			return nil, err
		}
		return rulite.NewEngineFromRuleSet(set)
	}
	initial, err := build("pricing/v1", 10)
	if err != nil {
		panic(err)
	}
	runtime, err := rulite.NewRuntime(initial)
	if err != nil {
		panic(err)
	}
	// The caller finishes source acquisition and compilation before Publish.
	next, err := build("pricing/v2", 20)
	if err != nil {
		panic(err)
	}
	if _, err := runtime.Publish(next); err != nil {
		panic(err)
	}
	price := Price{}
	result, err := runtime.Fire(context.Background(), &price)
	if err != nil {
		panic(err)
	}
	fmt.Println(price.Discount, result.Snapshot().Version(), result.Snapshot().Revision())
	// Output: 20 pricing/v2 2
}
