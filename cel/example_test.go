package cel_test

import (
	"context"
	"fmt"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
)

func ExampleNewCompiler() {
	type Price struct {
		VIP      bool
		Total    int64
		Discount int
	}
	compiler, err := cel.NewCompiler[Price]("input")
	if err != nil {
		panic(err)
	}
	eligible, err := compiler.Compile("input.VIP && input.Total >= 10000")
	if err != nil {
		panic(err)
	}
	rule := rulite.NewRule[Price]("pricing/vip").When(eligible).
		Then(func(_ context.Context, p *Price) error { p.Discount = 20; return nil })
	engine, err := rulite.NewEngine(rule)
	if err != nil {
		panic(err)
	}
	price := Price{VIP: true, Total: 12000}
	result, err := engine.Fire(context.Background(), &price)
	if err != nil {
		panic(err)
	}
	fmt.Println(price.Discount, result.Fired())
	// Output: 20 [pricing/vip]
}
