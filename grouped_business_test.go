package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite"
)

type businessReviewInput struct {
	provider  string
	attempts  []rulite.RuleID
	discount  int
	appliedBy rulite.RuleID
	audit     string
	decision  string
}

type businessReviewFaults struct {
	beforeBackup, afterBackup, afterAdjustment func()
}

// This fixed consumer fixture has 100 executable rules and 42 top-level entries.
// Registered order intentionally differs from both top-level and local order.
func businessReviewSet(t *testing.T, evidence, primary error, faults businessReviewFaults) *rulite.RuleSet[businessReviewInput] {
	t.Helper()
	noAction := func(context.Context, *businessReviewInput) error { t.Fatal("an unselected action ran"); return nil }
	never := func(context.Context, *businessReviewInput) (bool, error) { return false, nil }
	always := rulite.All[businessReviewInput]()
	unused := func(id rulite.RuleID) rulite.Rule[businessReviewInput] {
		return rulite.NewRule[businessReviewInput](id).When(always).Then(noAction)
	}
	var providers, offers []rulite.Rule[businessReviewInput]
	for i := range 26 {
		providers = append(providers, unused(rulite.RuleID(fmt.Sprintf("payment/unused/%02d", i))))
	}
	backup := rulite.NewRule[businessReviewInput]("payment/backup").Priority(100).When(func(context.Context, *businessReviewInput) (bool, error) {
		if faults.beforeBackup != nil {
			faults.beforeBackup()
		}
		return true, nil
	}).Then(func(_ context.Context, input *businessReviewInput) error {
		input.attempts = append(input.attempts, "payment/backup")
		input.provider = "backup"
		if faults.afterBackup != nil {
			faults.afterBackup()
		}
		return nil
	})
	primaryRule := rulite.NewRule[businessReviewInput]("payment/primary").Priority(200).When(always).Then(func(_ context.Context, input *businessReviewInput) error {
		input.attempts = append(input.attempts, "payment/primary")
		return primary
	})
	evidenceRule := rulite.NewRule[businessReviewInput]("payment/evidence").Priority(300).When(func(context.Context, *businessReviewInput) (bool, error) { return true, evidence }).Then(noAction)
	ineligible := rulite.NewRule[businessReviewInput]("payment/ineligible").Priority(400).When(never).Then(noAction)
	providers = append(providers, backup, primaryRule, evidenceRule, ineligible)
	for i := range 28 {
		offers = append(offers, unused(rulite.RuleID(fmt.Sprintf("pricing/unused/%02d", i))))
	}
	offer := rulite.NewRule[businessReviewInput]("pricing/vip").Priority(300).When(func(_ context.Context, input *businessReviewInput) (bool, error) {
		return input.provider == "backup", nil
	}).Then(func(_ context.Context, input *businessReviewInput) error {
		input.discount, input.appliedBy = 20, "pricing/vip"
		return nil
	})
	noOffer := rulite.NewRule[businessReviewInput]("pricing/ineligible").Priority(400).When(never).Then(noAction)
	offers = append(offers, offer, noOffer)
	adjustment := rulite.NewRule[businessReviewInput]("pricing/adjustment").When(func(_ context.Context, input *businessReviewInput) (bool, error) {
		return input.discount == 20 && input.appliedBy == "pricing/vip", nil
	}).Then(func(_ context.Context, input *businessReviewInput) error {
		input.discount, input.appliedBy = 25, "pricing/adjustment"
		if faults.afterAdjustment != nil {
			faults.afterAdjustment()
		}
		return nil
	})
	audit := rulite.NewRule[businessReviewInput]("checkout/audit").Priority(-10).When(func(_ context.Context, input *businessReviewInput) (bool, error) {
		return input.provider != "" && input.discount == 25, nil
	}).Then(func(_ context.Context, input *businessReviewInput) error {
		input.audit = fmt.Sprintf("provider=%s discount=%d applied-by=%s", input.provider, input.discount, input.appliedBy)
		input.decision = "ready"
		return nil
	})
	entries := []rulite.Entry[businessReviewInput]{adjustment.Entry(), audit.Entry(), rulite.FirstMatchGroup("pricing/offers", offers...).WithPriority(50).Entry(), rulite.FirstFireGroup("payment/providers", providers...).WithPriority(100).Entry()}
	for i := range 20 {
		entries = append(entries, rulite.NewRule[businessReviewInput](rulite.RuleID(fmt.Sprintf("eligibility/%02d", i))).Priority(200).When(never).Then(noAction).Entry())
	}
	for i := range 18 {
		entries = append(entries, rulite.NewRule[businessReviewInput](rulite.RuleID(fmt.Sprintf("review/%02d", i))).Priority(-20).When(never).Then(noAction).Entry())
	}
	return mustEntries(t, entries...)
}

// The expected sequence describes this fixture, without consulting compiled views.
func businessReviewOrder() []rulite.RuleID {
	var ids []rulite.RuleID
	for i := range 20 {
		ids = append(ids, rulite.RuleID(fmt.Sprintf("eligibility/%02d", i)))
	}
	ids = append(ids, "payment/ineligible", "payment/evidence", "payment/primary", "payment/backup")
	for i := range 26 {
		ids = append(ids, rulite.RuleID(fmt.Sprintf("payment/unused/%02d", i)))
	}
	ids = append(ids, "pricing/ineligible", "pricing/vip")
	for i := range 28 {
		ids = append(ids, rulite.RuleID(fmt.Sprintf("pricing/unused/%02d", i)))
	}
	ids = append(ids, "pricing/adjustment", "checkout/audit")
	for i := range 18 {
		ids = append(ids, rulite.RuleID(fmt.Sprintf("review/%02d", i)))
	}
	return ids
}

func TestGroupedBusinessReview(t *testing.T) {
	evidence, primary, canceled, exportError := errors.New("eligibility evidence unavailable"), errors.New("primary provider unavailable"), errors.New("checkout withdrawn"), errors.New("export unavailable")
	payload := &struct{ code int }{7}
	continued := rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)
	complete := rulite.Counts{Total: 100, Evaluated: 46, NotEvaluated: 54, Unmatched: 40, Matched: 5, Fired: 4, Failed: 2, ConditionFailed: 1, ActionFailed: 1}
	primaryStop := rulite.Counts{Total: 100, Evaluated: 23, NotEvaluated: 77, Unmatched: 21, Matched: 1, Failed: 2, ConditionFailed: 1, ActionFailed: 1}
	backupSuccess := rulite.Counts{Total: 100, Evaluated: 24, NotEvaluated: 76, Unmatched: 21, Matched: 2, Fired: 1, Failed: 2, ConditionFailed: 1, ActionFailed: 1}
	cases := []struct {
		name   string
		policy rulite.ExecutionPolicy
		stop   rulite.StopReason
		last   int
		backup rulite.RuleState
		counts rulite.Counts
	}{
		{"completed", continued, rulite.StopCompleted, 99, rulite.RuleFired, complete},
		{"default_fail_safe", rulite.DefaultPolicy(), rulite.StopConditionError, 21, rulite.RuleNotEvaluated, rulite.Counts{Total: 100, Evaluated: 22, NotEvaluated: 78, Unmatched: 21, Failed: 1, ConditionFailed: 1}},
		{"action_stop", rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError), rulite.StopActionError, 22, rulite.RuleNotEvaluated, primaryStop},
		{"global_first_match", continued.WithStop(rulite.StopOnFirstMatch), rulite.StopFirstMatch, 22, rulite.RuleNotEvaluated, primaryStop},
		{"global_first_fire", continued.WithStop(rulite.StopOnFirstFire), rulite.StopFirstFire, 23, rulite.RuleFired, backupSuccess},
		{"context_before_action", continued, rulite.StopContextCanceled, 23, rulite.RuleSkipped, rulite.Counts{Total: 100, Evaluated: 24, NotEvaluated: 76, Unmatched: 21, Matched: 2, Skipped: 1, Failed: 2, ConditionFailed: 1, ActionFailed: 1}},
		{"context_after_fire", continued.WithStop(rulite.StopOnFirstFire), rulite.StopContextCanceled, 23, rulite.RuleFired, backupSuccess},
		{"condition_panic", continued, rulite.StopPanic, 23, rulite.RuleFailed, rulite.Counts{Total: 100, Evaluated: 24, NotEvaluated: 76, Unmatched: 21, Matched: 1, Failed: 3, ConditionFailed: 2, ActionFailed: 1, PanicRecovered: 1}},
		{"action_panic_and_context", continued, rulite.StopPanic, 23, rulite.RuleFailed, rulite.Counts{Total: 100, Evaluated: 24, NotEvaluated: 76, Unmatched: 21, Matched: 2, Failed: 3, ConditionFailed: 1, ActionFailed: 2, PanicRecovered: 1}},
		{"late_context", continued, rulite.StopContextCanceled, 80, rulite.RuleFired, rulite.Counts{Total: 100, Evaluated: 27, NotEvaluated: 73, Unmatched: 22, Matched: 4, Fired: 3, Failed: 2, ConditionFailed: 1, ActionFailed: 1}},
		{"deadline_before_entry", continued, rulite.StopContextDeadlineExceeded, -1, rulite.RuleNotEvaluated, rulite.Counts{Total: 100, NotEvaluated: 100}},
		{"observer_error", continued, rulite.StopCompleted, 99, rulite.RuleFired, complete},
		{"observer_panic", continued, rulite.StopCompleted, 99, rulite.RuleFired, complete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			var runContext context.Context = ctx
			if tc.name == "deadline_before_entry" {
				deadlineContext, release := context.WithDeadlineCause(ctx, time.Unix(0, 0), canceled)
				defer release()
				runContext = deadlineContext
			}
			faults := businessReviewFaults{}
			switch tc.name {
			case "context_before_action":
				faults.beforeBackup = func() { cancel(canceled) }
			case "context_after_fire":
				faults.afterBackup = func() { cancel(canceled) }
			case "condition_panic":
				faults.beforeBackup = func() { panic(payload) }
			case "action_panic_and_context":
				faults.afterBackup = func() { cancel(canceled); panic(payload) }
			case "late_context":
				faults.afterAdjustment = func() { cancel(canceled) }
			}
			set := businessReviewSet(t, evidence, primary, faults)
			engine := mustReuse(t, set, rulite.WithPolicy(tc.policy))
			var events []rulite.Event
			observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
				events = append(events, event)
				group, ok := event.Group()
				if ok && group.ID() == "payment/providers" && event.Kind() == rulite.EventGroupResolved {
					if tc.name == "observer_error" {
						return exportError
					}
					if tc.name == "observer_panic" {
						panic(payload)
					}
				}
				return nil
			})
			input := businessReviewInput{decision: "review"}
			result, err := engine.Fire(runContext, &input, rulite.WithTrace(), rulite.WithObserver(observer))
			if result.Counts() != tc.counts || result.StopReason() != tc.stop || set.Len() != 100 || len(result.Explain().Entries()) != 42 {
				t.Fatalf("counts=%+v stop=%s; want %+v %s", result.Counts(), result.StopReason(), tc.counts, tc.stop)
			}
			var wantAttempts []rulite.RuleID
			if tc.last >= 22 {
				wantAttempts = append(wantAttempts, "payment/primary")
			}
			backupMutated := tc.last >= 23 && tc.backup != rulite.RuleSkipped && tc.name != "condition_panic"
			if backupMutated {
				wantAttempts = append(wantAttempts, "payment/backup")
			}
			if !slices.Equal(input.attempts, wantAttempts) || (input.provider == "backup") != backupMutated {
				t.Fatal("partial provider effects were lost or replayed")
			}
			wantDecision, wantAudit := "review", ""
			if tc.last == 99 {
				wantDecision, wantAudit = "ready", "provider=backup discount=25 applied-by=pricing/adjustment"
			}
			if input.decision != wantDecision || input.audit != wantAudit {
				t.Fatal("fail-safe decision or audit changed")
			}
			if tc.last >= 80 {
				if input.discount != 25 || input.appliedBy != "pricing/adjustment" {
					t.Fatal("later field writer lost provenance")
				}
			} else if input.discount != 0 || input.appliedBy != "" {
				t.Fatal("uncalled pricing action wrote state")
			}
			orders := businessReviewOrder()
			compiled := set.Rules()
			for order, id := range orders {
				info, known := set.Rule(id)
				rule, found := result.Rule(id)
				if !known || !found || info.Order() != order || rule.Order() != order || compiled[order].ID() != id {
					t.Fatal("consumer cannot reconstruct stable compiled order")
				}
				checkBusinessCoordinates(t, order, rule)
				localHole := tc.last >= 50 && order >= 24 && order < 50 || tc.last >= 80 && order >= 52 && order < 80
				wantState, wantReason := rulite.RuleUnmatched, rulite.NotEvaluatedNone
				if order > tc.last || localHole {
					wantState, wantReason = rulite.RuleNotEvaluated, rulite.NotEvaluatedExecutionStopped
					if localHole {
						wantReason = rulite.NotEvaluatedGroupResolved
					}
				} else {
					switch order {
					case 21, 22:
						wantState = rulite.RuleFailed
					case 23:
						wantState = tc.backup
					case 51, 80, 81:
						wantState = rulite.RuleFired
					}
				}
				if rule.State() != wantState || rule.NotEvaluatedReason() != wantReason {
					t.Fatalf("%s: state=%v reason=%v; want %v %v", id, rule.State(), rule.NotEvaluatedReason(), wantState, wantReason)
				}
			}
			for index, id := range []rulite.GroupID{"payment/providers", "pricing/offers"} {
				group, _ := result.Group(id)
				state, end, stop, selected := rulite.GroupNotEntered, rulite.GroupEndExecutionStopped, tc.stop, rulite.RuleID("")
				if index == 0 && tc.last >= 20 {
					state = rulite.GroupInterrupted
				}
				if index == 0 && tc.backup == rulite.RuleFired {
					state, selected = rulite.GroupResolved, "payment/backup"
				}
				if index == 1 && tc.last >= 80 {
					state, selected = rulite.GroupResolved, "pricing/vip"
				}
				if tc.last >= 80 {
					end, stop = rulite.GroupEndResolved, rulite.StopNone
				}
				actual, present := group.SelectedRule()
				if group.State() != state || group.EndReason() != end || group.StopReason() != stop || actual != selected || present != (selected != "") {
					t.Fatal("local and global decisions disagree")
				}
				if index == 1 && present && actual == input.appliedBy {
					t.Fatal("selection was mistaken for final field provenance")
				}
			}
			failures := result.Failures()
			var executionErr *rulite.ExecutionError
			if !errors.As(err, &executionErr) || len(failures) != tc.counts.Failed || errors.Is(err, exportError) {
				t.Fatal("business failures or diagnostic isolation changed")
			}
			for i, failure := range failures {
				id, cause, phase, allowed := rulite.RuleID("payment/evidence"), evidence, rulite.ConditionPhase, tc.policy.ConditionErrorMode() == rulite.ContinueOnError
				if i == 1 {
					id, cause, phase, allowed = "payment/primary", primary, rulite.ActionPhase, tc.policy.ActionErrorMode() == rulite.ContinueOnError
				}
				if i == 2 {
					id, allowed = "payment/backup", false
					if tc.name != "condition_panic" {
						phase = rulite.ActionPhase
					}
					var panicErr *rulite.PanicError
					if !errors.As(failure, &panicErr) || panicErr.Value() != payload || len(panicErr.Stack()) == 0 {
						t.Fatal("panic payload or stack lost")
					}
				} else if failure.Cause() != cause || !errors.Is(err, cause) {
					t.Fatal("original business cause lost")
				}
				if failure.RuleID() != id || failure.Phase() != phase || failure.Continued() != allowed {
					t.Fatal("failure observation order or disposition changed")
				}
			}
			var causes []error
			for _, failure := range failures {
				causes = append(causes, failure)
			}
			if runContext.Err() != nil {
				causes = append(causes, runContext.Err(), canceled)
			}
			if !reflect.DeepEqual(executionErr.Unwrap(), causes) || errors.Is(err, canceled) != (runContext.Err() != nil) {
				t.Fatal("context cause order or error aggregation changed")
			}
			facts := expectedEvents(result)
			if tc.name == "observer_error" || tc.name == "observer_panic" {
				if len(result.Diagnostics()) != 1 || events[len(events)-1].Kind() != rulite.EventGroupResolved {
					t.Fatal("observer did not disable after its first fault")
				}
				diagnostic := result.Diagnostics()[0]
				if !reflect.DeepEqual(diagnostic.Event(), events[len(events)-1]) {
					t.Fatal("diagnostic lost its event")
				}
				if tc.name == "observer_error" && !errors.Is(diagnostic, exportError) {
					t.Fatal("observer cause lost")
				}
				if tc.name == "observer_panic" {
					var panicErr *rulite.ObserverPanicError
					if !errors.As(diagnostic, &panicErr) || panicErr.Value() != payload {
						t.Fatal("observer panic became a business failure")
					}
				}
			} else if len(events) != len(facts) || len(result.Diagnostics()) != 0 {
				t.Fatal("ordered business event stream is incomplete")
			}
			for i, event := range events {
				checkObservedEvent(t, event, facts[i], result)
			}
			checkResultConsistency(t, result)
		})
	}
}

func checkBusinessCoordinates(t *testing.T, order int, rule rulite.RuleExecution) {
	t.Helper()
	top, registration, member, group := order, order+4, 0, rulite.GroupID("")
	if order >= 20 && order < 50 {
		top, registration, group, member = 20, 3, "payment/providers", 29-(order-20)
		if order >= 24 {
			member = order - 24
		}
	} else if order >= 50 && order < 80 {
		top, registration, group, member = 21, 2, "pricing/offers", 29-(order-50)
		if order >= 52 {
			member = order - 52
		}
	} else if order == 80 {
		top, registration = 22, 0
	} else if order == 81 {
		top, registration = 23, 1
	} else if order >= 82 {
		top, registration = order-58, order-58
	}
	actualGroup, grouped := rule.GroupID()
	actualMember, memberOK := rule.MemberIndex()
	if rule.TopLevelOrder() != top || rule.RegistrationIndex() != registration || actualGroup != group || grouped != (group != "") || memberOK != grouped || actualMember != member {
		t.Fatal("top-level order, registration and local coordinates became ambiguous")
	}
}
