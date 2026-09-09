package rulite

import (
	"fmt"
	"strings"
)

// Explanation is an immutable view of Result's canonical ledger. It is
// available without Trace and never reevaluates conditions. Structural
// semantics are supported in v0.x; String's exact formatting may change.
type Explanation struct{ result Result }

// Rules returns independent execution views for every rule in compiled order.
func (e Explanation) Rules() []RuleExecution {
	if e.result.metadata == nil {
		return nil
	}
	rules := make([]RuleExecution, e.result.counts.Total)
	for order := range rules {
		rules[order] = e.result.ruleAt(order)
	}
	return rules
}

// String describes the global stop, counts, and every rule's outcome.
// It does not include input, action parameters, panic values, or stacks.
// Business error messages are included as supplied by the callback.
// The text is intended for people, not parsing or character-level compatibility.
func (e Explanation) String() string {
	if !e.result.Executed() {
		return "execution: not-started\nstop reason: none\n"
	}
	var text strings.Builder
	r := e.result
	fmt.Fprintf(&text, "execution: %s\n", r.stop)
	c := r.counts
	fmt.Fprintf(&text, "summary: total=%d evaluated=%d not-evaluated=%d matched=%d unmatched=%d fired=%d skipped=%d failed=%d condition-failed=%d action-failed=%d panic-recovered=%d\n",
		c.Total, c.Evaluated, c.NotEvaluated, c.Matched, c.Unmatched, c.Fired, c.Skipped, c.Failed, c.ConditionFailed, c.ActionFailed, c.PanicRecovered)
	for _, group := range r.Groups() {
		selected, _ := group.SelectedRule()
		fmt.Fprintf(&text, "\ngroup %s kind=%s top-level-order=%d registration-index=%d priority=%d state=%s selected-rule=%s\n", group.ID(), group.Kind(), group.Order(), group.RegistrationIndex(), group.Priority(), group.State(), selected)
	}
	for order := 0; order < c.Total; order++ {
		x := r.ruleAt(order)
		fmt.Fprintf(&text, "\nrule %s order=%d registration-index=%d priority=%d\n", x.ID(), x.Order(), x.RegistrationIndex(), x.Priority())
		if id, ok := x.GroupID(); ok {
			member, _ := x.MemberIndex()
			fmt.Fprintf(&text, "  group=%s top-level-order=%d member-index=%d\n", id, x.TopLevelOrder(), member)
		} else if len(r.metadata.groups) > 0 {
			fmt.Fprintf(&text, "  top-level-order=%d\n", x.TopLevelOrder())
		}
		fmt.Fprintf(&text, "  evaluated=%t matched=%t action-started=%t action-success=%t state=%s\n", x.Evaluated(), x.Matched(), x.ActionStarted(), x.Fired(), ruleStateText(x.State()))
		if failure, ok := x.Error().(Failure); ok {
			fmt.Fprintf(&text, "  failure: %s; continued=%t\n", failure.Error(), failure.Continued())
		}
		if x.SkipReason() == SkipContextDone {
			text.WriteString("  skip reason: context-done\n")
		}
		if x.NotEvaluatedReason() == NotEvaluatedExecutionStopped {
			text.WriteString("  not-evaluated reason: execution-stopped\n")
		}
		if x.NotEvaluatedReason() == NotEvaluatedGroupResolved {
			text.WriteString("  not-evaluated reason: group-resolved\n")
		}
	}
	return text.String()
}

func ruleStateText(state RuleState) string {
	switch state {
	case RuleNotEvaluated:
		return "not-evaluated"
	case RuleUnmatched:
		return "unmatched"
	case RuleFired:
		return "fired"
	case RuleFailed:
		return "failed"
	case RuleSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}
