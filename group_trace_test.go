package rulite_test

import (
	"context"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func TestGroupTraceSeparatesMemberHolesAndChildShortCircuit(t *testing.T) {
	for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
		forbidden := func(context.Context, *executionInput) (bool, error) {
			t.Fatal("uncalled condition executed")
			return false, nil
		}
		condition := rulite.All(rulite.Any(constantCondition(false, nil), constantCondition(true, nil), forbidden), rulite.Not(constantCondition(false, nil)))
		winner := rulite.NewRule[executionInput]("winner").When(condition).Then(successfulAction)
		uncalled := rulite.NewRule[executionInput]("uncalled").When(rulite.All(forbidden)).Then(successfulAction)
		group := rulite.FirstMatchGroup("offers", winner, uncalled)
		if kind == rulite.GroupFirstFire {
			group = rulite.FirstFireGroup("offers", winner, uncalled)
		}
		audit := rulite.NewRule[executionInput]("audit").When(rulite.Any[executionInput]()).Then(successfulAction)
		result, err := mustReuse(t, mustEntries(t, group.Entry(), audit.Entry())).Fire(context.Background(), &executionInput{}, rulite.WithTrace())
		if err != nil {
			t.Fatal(err)
		}
		trace, _ := result.Trace()
		rules := trace.Rules()
		tree, ok := rules[0].ConditionTree()
		if !ok || tree.Kind() != rulite.ConditionAll {
			t.Fatal("selected member lost its tree")
		}
		short := tree.Children()[0].Children()[2]
		if short.Outcome() != rulite.ConditionOutcomeNotEvaluated || short.NotEvaluatedReason() != rulite.ConditionNotEvaluatedShortCircuit {
			t.Fatal("child short-circuit became a group hole")
		}
		if _, ok := rules[1].ConditionTree(); ok || rules[1].NotEvaluatedReason() != rulite.NotEvaluatedGroupResolved || rules[1].ConditionDuration() != 0 || rules[1].ActionDuration() != 0 {
			t.Fatal("uncalled member has callback observations")
		}
		if tree, ok := rules[2].ConditionTree(); !ok || tree.Kind() != rulite.ConditionAny || rules[2].State() != rulite.RuleUnmatched {
			t.Fatal("later rule lost its tree after a hole")
		}
		clear(tree.Children()[0].Children())
		if tree.Children()[0].Children()[2].NotEvaluatedReason() != rulite.ConditionNotEvaluatedShortCircuit {
			t.Fatal("nested tree storage exposed")
		}
		checkResultConsistency(t, result)
	}
}
