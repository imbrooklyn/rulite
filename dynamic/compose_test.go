package dynamic_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
)

func TestCompileRulesCompositionAndOwnership(t *testing.T) {
	definitions := []dynamic.Definition{definition(), definition()}
	definitions[0].ID, definitions[1].ID = "pricing/first", "pricing/second"
	definitions[0].Tags = []string{" original "}
	checks, actions := compiler(t), registry(t)
	rules, err := dynamic.CompileRules(definitions, checks, actions)
	if err != nil {
		t.Fatal(err)
	}
	// A concrete assignment also verifies the external generic return type.
	var members []rulite.Rule[price] = rules
	if len(members) != 2 || members[0].ID() != definitions[0].ID || members[1].ID() != definitions[1].ID {
		t.Fatal("registration order lost")
	}
	group := rulite.FirstFireGroup("offers", members...)
	audit := rulite.NewRule[price]("audit").Priority(-1).
		When(func(_ context.Context, p *price) (bool, error) { return p.Discount == 20, nil }).
		Then(func(_ context.Context, p *price) error { p.Steps = append(p.Steps, "audit"); return nil })
	set, err := rulite.CompileEntries(group.Entry(), audit.Entry())
	if err != nil {
		t.Fatal(err)
	}
	clear(rules)
	clear(definitions[0].Params)
	definitions[0].Tags[0] = "changed"
	e, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	p := price{VIP: true}
	r, err := e.Fire(context.Background(), &p)
	if err != nil || p.Discount != 20 || !reflect.DeepEqual(p.Steps, []string{"audit"}) || r.Counts().Fired != 2 {
		t.Fatal("composition or ownership changed")
	}
	selected, _ := r.Group("offers")
	id, ok := selected.SelectedRule()
	first, _ := set.Rule("pricing/first")
	if !ok || id != "pricing/first" || !reflect.DeepEqual(first.Tags(), []string{"original"}) {
		t.Fatal("selection or metadata changed")
	}
	duplicate := rulite.FirstFireGroup("other", group.Members()...)
	if bad, err := rulite.CompileEntries(group.Entry(), duplicate.Entry()); bad != nil || !errors.Is(err, rulite.ErrDuplicateRuleID) {
		t.Fatal("combined identity validation bypassed")
	}
}

func TestCompileRulesFailureIsComplete(t *testing.T) {
	c, r := compiler(t), registry(t)
	for _, stage := range []string{"validate", "condition", "action"} {
		ds := []dynamic.Definition{definition(), definition()}
		ds[1].ID = "second"
		switch stage {
		case "validate":
			ds[1].ID = ds[0].ID
		case "condition":
			ds[1].When = "("
		case "action":
			ds[1].Action = "missing.action/v1"
		}
		rules, err := dynamic.CompileRules(ds, c, r)
		if rules != nil {
			t.Fatal("partial rules returned")
		}
		index := 1
		if stage == "validate" {
			index = -1
		}
		requireCompileError(t, err, stage, index, nil)
	}
	if rules, err := dynamic.CompileRules[price](nil, nil, nil); rules != nil || !errors.Is(err, dynamic.ErrInvalidRegistry) {
		t.Fatal("registry preflight changed")
	}
	if rules, err := dynamic.CompileRules(nil, (*cel.Compiler[price])(nil), r); rules != nil || !errors.Is(err, cel.ErrInvalidCompiler) {
		t.Fatal("empty compiler validation skipped")
	}
	if rules, err := dynamic.CompileRules(nil, c, dynamic.NewRegistry[price]()); rules != nil || !errors.Is(err, dynamic.ErrRegistryNotFrozen) {
		t.Fatal("mutable registry accepted")
	}
	rules, err := dynamic.CompileRules[price](nil, c, r)
	if err != nil || len(rules) != 0 {
		t.Fatal("empty composition rejected")
	}
}
