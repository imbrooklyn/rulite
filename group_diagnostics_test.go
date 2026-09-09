package rulite_test

import (
	"reflect"
	"testing"

	"github.com/imbrooklyn/rulite"
)

// All diagnostics are compared with the captured ledger, including every hole.
// The independent group-program oracle separately validates execution semantics.
func checkGroupViews(t testing.TB, result rulite.Result) {
	t.Helper()
	var flattened []rulite.RuleExecution
	var groups []rulite.GroupResult
	entries := result.Explain().Entries()
	for order, entry := range entries {
		if entry.Order() != order {
			t.Fatal("top-level order changed")
		}
		rule, isRule := entry.Rule()
		group, isGroup := entry.Group()
		if isRule == isGroup {
			t.Fatal("entry must be exactly one rule or group")
		}
		if isRule {
			if rule.TopLevelOrder() != order || len(entry.Members()) != 0 {
				t.Fatal("top-level rule has members or wrong order")
			}
			flattened = append(flattened, rule)
			continue
		}
		fromResult, ok := result.Group(group.ID())
		if !ok || !reflect.DeepEqual(group, fromResult) || group.Order() != order {
			t.Fatal("nested group differs from Result")
		}
		groups = append(groups, group)
		members := entry.Members()
		selected, selectedOK := group.SelectedRule()
		if selectedOK != group.Resolved() || group.Entered() != (group.State() != rulite.GroupNotEntered) {
			t.Fatal("group entry or selection facts disagree")
		}
		if selectedOK {
			chosen, ok := result.Rule(selected)
			id, member := chosen.GroupID()
			if !ok || !member || id != group.ID() || !chosen.Matched() || group.Kind() == rulite.GroupFirstFire && !chosen.Fired() {
				t.Fatal("group selected without its criterion")
			}
		}
		if group.EndReason() == rulite.GroupEndExecutionStopped {
			if group.StopReason() != result.StopReason() || !result.Stopped() {
				t.Fatal("global termination lost its reason")
			}
		} else if group.StopReason() != rulite.StopNone || group.EndReason() == rulite.GroupEndNone {
			t.Fatal("completed group has an incorrect terminal reason")
		}
		switch group.State() {
		case rulite.GroupNotEntered, rulite.GroupInterrupted:
			if group.EndReason() != rulite.GroupEndExecutionStopped {
				t.Fatal("unentered/interrupted group looked completed")
			}
		case rulite.GroupExhausted:
			if group.EndReason() != rulite.GroupEndExhausted {
				t.Fatal("exhausted group end differs")
			}
		case rulite.GroupResolved:
			if group.EndReason() != rulite.GroupEndResolved && group.EndReason() != rulite.GroupEndExecutionStopped {
				t.Fatal("resolved group end differs")
			}
		default:
			t.Fatal("unknown group state")
		}
		seenSelection := false
		for _, member := range members {
			id, ok := member.GroupID()
			if !ok || id != group.ID() || member.TopLevelOrder() != order || member.RegistrationIndex() != group.RegistrationIndex() {
				t.Fatal("nested member lost coordinates")
			}
			if group.State() == rulite.GroupNotEntered && member.NotEvaluatedReason() != rulite.NotEvaluatedExecutionStopped || group.State() == rulite.GroupExhausted && !member.Evaluated() {
				t.Fatal("group state disagrees with member evaluation")
			}
			if seenSelection {
				want := rulite.NotEvaluatedExecutionStopped
				if group.EndReason() == rulite.GroupEndResolved {
					want = rulite.NotEvaluatedGroupResolved
				}
				if member.Evaluated() || member.NotEvaluatedReason() != want {
					t.Fatal("selected suffix has the wrong hole reason")
				}
			}
			seenSelection = seenSelection || member.ID() == selected
		}
		flattened = append(flattened, members...)
		clear(members)
		if len(entry.Members()) != 0 && entry.Members()[0].ID() == "" {
			t.Fatal("nested member slice exposed storage")
		}
	}
	rules := result.Explain().Rules()
	if len(flattened) != len(rules) || len(groups) != len(result.Groups()) {
		t.Fatal("hierarchy omitted definitions")
	}
	for i := range flattened {
		if !reflect.DeepEqual(flattened[i], rules[i]) {
			t.Fatal("hierarchy disagrees with flat rule facts")
		}
	}
	for i, group := range groups {
		if !reflect.DeepEqual(group, result.Groups()[i]) || !reflect.DeepEqual(group, result.Explain().Groups()[i]) {
			t.Fatal("group ordering or facts drifted")
		}
	}
	clear(entries)
	if len(result.Explain().Entries()) != 0 {
		first := result.Explain().Entries()[0]
		_, rule := first.Rule()
		_, group := first.Group()
		if !rule && !group {
			t.Fatal("entry slice exposed storage")
		}
	}
	if trace, ok := result.Trace(); ok {
		if !reflect.DeepEqual(trace.Groups(), result.Groups()) {
			t.Fatal("trace group facts disagree")
		}
		for _, group := range groups {
			fromTrace, found := trace.Group(group.ID())
			if !found || !reflect.DeepEqual(fromTrace, group) {
				t.Fatal("trace group lookup differs")
			}
		}
		clear(trace.Groups())
	}
}

func TestGroupDiagnosticZeroViews(t *testing.T) {
	var entry rulite.EntryExplanation
	_, rule := entry.Rule()
	_, group := entry.Group()
	var trace rulite.Trace
	_, found := trace.Group("unknown")
	var event rulite.Event
	view, present := event.Group()
	if rule || group || found || present || entry.Order() != 0 || len(entry.Members()) != 0 || len(trace.Groups()) != 0 || view.EndReason() != rulite.GroupEndNone || view.StopReason() != rulite.StopNone {
		t.Fatal("zero diagnostic contains facts")
	}
	for value, name := range []string{"none", "resolved", "exhausted", "execution-stopped"} {
		if rulite.GroupEndReason(value).String() != name {
			t.Fatal("group end reason name changed")
		}
	}
	if rulite.GroupEndReason(255).String() != "unknown" || rulite.EventGroupResolved.String() != "group-resolved" || rulite.EventGroupFinished.String() != "group-finished" {
		t.Fatal("diagnostic kind names changed")
	}
	checkResultConsistency(t, rulite.Result{})
}
