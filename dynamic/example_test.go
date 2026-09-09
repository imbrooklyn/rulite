package dynamic_test

import (
	"context"
	"fmt"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
)

func ExampleNewRegistry() {
	type Price struct {
		VIP      bool
		Discount int
	}
	type Params struct {
		Percent int `json:"percent"`
	}
	conditions, err := cel.NewCompiler[Price]("input")
	if err != nil {
		panic(err)
	}
	actions := dynamic.NewRegistry[Price]()
	if err := actions.Register("pricing.apply_discount/v1", func(_ context.Context, p *Price, v Params) error { p.Discount = v.Percent; return nil }); err != nil {
		panic(err)
	}
	if err := actions.Freeze(); err != nil {
		panic(err)
	}
	set, err := dynamic.CompileJSON([]byte(`[{"id":"pricing/vip","when":"input.VIP","action":"pricing.apply_discount/v1","params":{"percent":20}}]`), conditions, actions)
	if err != nil {
		panic(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		panic(err)
	}
	input := Price{VIP: true}
	result, err := engine.Fire(context.Background(), &input)
	if err != nil {
		panic(err)
	}
	fmt.Println(input.Discount, result.Fired())
	// Output: 20 [pricing/vip]
}
