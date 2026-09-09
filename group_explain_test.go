package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite"
)

func TestGroupExplanationGolden(t *testing.T) {
	// Structural assertions are authoritative; this rendering is a readability fixture.
	cause := errors.New("provider unavailable")
	base := rulite.DefaultPolicy()
	continued := base.WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)
	cases := []struct {
		name   string
		kind   rulite.GroupKind
		codes  []byte
		policy rulite.ExecutionPolicy
		stop   rulite.StopReason
	}{
		{"completed", rulite.GroupFirstFire, []byte{0, 2, 3, 1, 1}, continued, rulite.StopCompleted},
		{"exhausted", rulite.GroupFirstFire, []byte{0, 3, 3}, continued, rulite.StopCompleted},
		{"first_match", rulite.GroupFirstMatch, []byte{3, 1}, continued.WithStop(rulite.StopOnFirstMatch), rulite.StopFirstMatch},
		{"first_fire", rulite.GroupFirstFire, []byte{3, 1, 1}, continued.WithStop(rulite.StopOnFirstFire), rulite.StopFirstFire},
		{"condition_error", rulite.GroupFirstFire, []byte{2, 1}, base, rulite.StopConditionError},
		{"action_error", rulite.GroupFirstMatch, []byte{3, 1}, base, rulite.StopActionError},
		{"context_skipped", rulite.GroupFirstMatch, []byte{1, 1}, base, rulite.StopContextCanceled},
		{"context_after_fire", rulite.GroupFirstFire, []byte{1, 1}, base.WithStop(rulite.StopOnFirstFire), rulite.StopContextCanceled},
		{"deadline", rulite.GroupFirstFire, []byte{1, 1}, base, rulite.StopContextDeadlineExceeded},
		{"condition_panic", rulite.GroupFirstFire, []byte{4, 1}, continued, rulite.StopPanic},
		{"action_panic", rulite.GroupFirstMatch, []byte{5, 1}, continued, rulite.StopPanic},
		{"earlier_hole_then_stop", rulite.GroupFirstMatch, []byte{1, 1}, base, rulite.StopConditionError},
	}
	var output strings.Builder
	fmt.Fprintf(&output, "=== not_started ===\n%s\n", rulite.Result{}.Explain())
	stops := map[rulite.StopReason]bool{rulite.StopNone: true}
	states := map[rulite.RuleState]bool{}
	for _, tc := range cases {
		ctx, cancel := context.WithCancelCause(context.Background())
		var runContext context.Context = ctx
		var release context.CancelFunc
		if tc.name == "deadline" {
			runContext, release = context.WithDeadlineCause(ctx, time.Unix(0, 0), errors.New("request expired"))
		}
		members := observationRules(tc.codes, cause)
		if tc.name == "context_skipped" {
			members[0] = rulite.NewRule[executionInput]("rule/0").When(func(context.Context, *executionInput) (bool, error) {
				cancel(errors.New("request withdrawn"))
				return true, nil
			}).Then(successfulAction)
		}
		if tc.name == "context_after_fire" {
			members[0] = rulite.NewRule[executionInput]("rule/0").When(constantCondition(true, nil)).Then(func(context.Context, *executionInput) error { cancel(errors.New("request withdrawn")); return nil })
		}
		group := rulite.FirstMatchGroup("providers", members...)
		if tc.kind == rulite.GroupFirstFire {
			group = rulite.FirstFireGroup("providers", members...)
		}
		auditCondition := constantCondition(true, nil)
		if tc.name == "earlier_hole_then_stop" {
			auditCondition = constantCondition(true, cause)
		}
		audit := rulite.NewRule[executionInput]("audit").When(auditCondition).Then(successfulAction)
		set := mustEntries(t, rulite.FirstMatchGroup[executionInput]("empty").Entry(), group.Entry(), audit.Entry(), rulite.FirstFireGroup[executionInput]("later").Entry())
		var events []rulite.Event
		observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error { events = append(events, event); return nil })
		result, _ := mustReuse(t, set, rulite.WithPolicy(tc.policy)).Fire(runContext, &executionInput{}, rulite.WithTrace(), rulite.WithObserver(observer))
		cancel(nil)
		if release != nil {
			release()
		}
		if result.StopReason() != tc.stop {
			t.Fatalf("%s: stop=%s; want %s", tc.name, result.StopReason(), tc.stop)
		}
		stops[result.StopReason()] = true
		for _, rule := range result.Explain().Rules() {
			states[rule.State()] = true
		}
		checkResultConsistency(t, result)
		facts := expectedEvents(result)
		if len(events) != len(facts) {
			t.Fatal("group stream omitted lifecycle facts")
		}
		fmt.Fprintf(&output, "=== %s ===\n%s", tc.name, result.Explain())
		output.WriteString("events:\n")
		for i, event := range events {
			checkObservedEvent(t, event, facts[i], result)
			fmt.Fprintf(&output, "  %s", event.Kind())
			if rule, ok := event.Rule(); ok {
				fmt.Fprintf(&output, " rule=%s", rule.ID())
			}
			if group, ok := event.Group(); ok {
				selected, _ := group.SelectedRule()
				fmt.Fprintf(&output, " group=%s state=%s selected=%s end=%s stop=%s", group.ID(), group.State(), selected, group.EndReason(), group.StopReason())
			}
			output.WriteByte('\n')
		}
		output.WriteByte('\n')
	}
	if len(stops) != 9 || len(states) != 5 {
		t.Fatal("golden does not cover every stop and rule state")
	}
	got := strings.TrimSuffix(output.String(), "\n")
	want, err := os.ReadFile("testdata/groups.golden")
	if err != nil || got != string(want) {
		t.Fatalf("group rendering differs (%v)\nBEGIN GROUP GOLDEN\n%sEND GROUP GOLDEN", err, got)
	}
}
