package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"

	"github.com/imbrooklyn/rulite"
)

type groupRuleSpec struct {
	id       rulite.RuleID
	priority rulite.Priority
	code     byte
}

type groupEntrySpec struct {
	id       rulite.GroupID
	kind     rulite.GroupKind
	priority rulite.Priority
	rules    []groupRuleSpec
}

type groupRuleWant struct {
	state                            rulite.RuleState
	reason                           rulite.NotEvaluatedReason
	phase                            rulite.Phase
	continued                        bool
	panicked                         bool
	order, top, registration, member int
	group                            rulite.GroupID
}

// The oracle traverses sorted definitions and predicts calls and outcomes without
// consulting Result, compiled metadata, or the runtime's sparse representation.
func checkGroupProgram(t testing.TB, program []groupEntrySpec, policy rulite.ExecutionPolicy) {
	t.Helper()
	indexes := make([]int, len(program))
	for i := range indexes {
		indexes[i] = i
	}
	sort.SliceStable(indexes, func(i, j int) bool { return program[indexes[i]].priority > program[indexes[j]].priority })
	wantRules := make(map[rulite.RuleID]groupRuleWant)
	wantGroups := make(map[rulite.GroupID]rulite.GroupState)
	selected := make(map[rulite.GroupID]rulite.RuleID)
	var wantCalls []string
	var wantFailures []rulite.RuleID
	stop := rulite.StopCompleted
	order, actions := 0, 0
	canceled := false
	for top, index := range indexes {
		entry := program[index]
		members := make([]int, len(entry.rules))
		for i := range members {
			members[i] = i
		}
		sort.SliceStable(members, func(i, j int) bool { return entry.rules[members[i]].priority > entry.rules[members[j]].priority })
		active := stop == rulite.StopCompleted
		if entry.id != "" {
			wantGroups[entry.id] = rulite.GroupNotEntered
			if active {
				wantGroups[entry.id] = rulite.GroupExhausted
			}
		}
		localDone := false
		for _, member := range members {
			rule := entry.rules[member]
			w := groupRuleWant{order: order, top: top, registration: index, member: member, group: entry.id, reason: rulite.NotEvaluatedExecutionStopped}
			order++
			if localDone {
				w.reason = rulite.NotEvaluatedGroupResolved
			}
			if stop != rulite.StopCompleted || localDone {
				wantRules[rule.id] = w
				continue
			}
			w.reason = rulite.NotEvaluatedNone
			kind := (rule.code & 63) % 6
			conditionCancel, actionCancel := rule.code&64 != 0, rule.code&128 != 0
			wantCalls = append(wantCalls, "condition:"+string(rule.id))
			w.state = rulite.RuleUnmatched
			if kind == 1 || kind == 4 {
				w.state, w.phase, w.panicked = rulite.RuleFailed, rulite.ConditionPhase, kind == 4
				w.continued = !w.panicked && policy.ConditionErrorMode() == rulite.ContinueOnError
				if !w.continued {
					stop = rulite.StopConditionError
				}
			} else if kind != 0 {
				if entry.id != "" && entry.kind == rulite.GroupFirstMatch {
					selected[entry.id] = rule.id
				}
				w.state = rulite.RuleSkipped
				if !conditionCancel {
					wantCalls = append(wantCalls, "action:"+string(rule.id))
					actions++
					w.state = rulite.RuleFired
					if kind == 3 || kind == 5 {
						w.state, w.phase, w.panicked = rulite.RuleFailed, rulite.ActionPhase, kind == 5
						w.continued = !w.panicked && policy.ActionErrorMode() == rulite.ContinueOnError
						if !w.continued {
							stop = rulite.StopActionError
						}
					} else if entry.id != "" && entry.kind == rulite.GroupFirstFire {
						selected[entry.id] = rule.id
					}
					if stop == rulite.StopCompleted {
						if policy.StopMode() == rulite.StopOnFirstMatch {
							stop = rulite.StopFirstMatch
						}
						if policy.StopMode() == rulite.StopOnFirstFire && w.state == rulite.RuleFired {
							stop = rulite.StopFirstFire
						}
					}
					if actionCancel {
						canceled = true
					}
				}
			}
			if conditionCancel {
				canceled = true
			}
			if canceled {
				stop = rulite.StopContextCanceled
			}
			if w.panicked {
				stop = rulite.StopPanic
			}
			if w.state == rulite.RuleFailed {
				wantFailures = append(wantFailures, rule.id)
			}
			if entry.id != "" {
				if selected[entry.id] != "" {
					wantGroups[entry.id] = rulite.GroupResolved
					localDone = stop == rulite.StopCompleted
				} else if stop != rulite.StopCompleted {
					wantGroups[entry.id] = rulite.GroupInterrupted
				}
			}
			wantRules[rule.id] = w
		}
	}
	cause := errors.New("provider unavailable")
	cancelCause := errors.New("request withdrawn")
	observationCause := errors.New("export unavailable")
	for mode := range 3 {
		ctx, cancel := context.WithCancelCause(context.Background())
		var calls []string
		entries := make([]rulite.Entry[executionInput], len(program))
		for i, entry := range program {
			rules := make([]rulite.Rule[executionInput], len(entry.rules))
			for member, spec := range entry.rules {
				kind := (spec.code & 63) % 6
				rules[member] = rulite.NewRule[executionInput](spec.id).Priority(spec.priority).When(func(context.Context, *executionInput) (bool, error) {
					calls = append(calls, "condition:"+string(spec.id))
					if spec.code&64 != 0 {
						cancel(cancelCause)
					}
					if kind == 4 {
						panic("condition unavailable")
					}
					if kind == 1 {
						return true, cause
					}
					return kind != 0, nil
				}).Then(func(_ context.Context, input *executionInput) error {
					calls = append(calls, "action:"+string(spec.id))
					input.value++
					if spec.code&128 != 0 {
						cancel(cancelCause)
					}
					if kind == 5 {
						panic("action unavailable")
					}
					if kind == 3 {
						return cause
					}
					return nil
				})
			}
			if entry.id == "" {
				entries[i] = rules[0].Entry()
				continue
			}
			group := rulite.FirstMatchGroup(entry.id, rules...)
			if entry.kind == rulite.GroupFirstFire {
				group = rulite.FirstFireGroup(entry.id, rules...)
			}
			entries[i] = group.WithPriority(entry.priority).Entry()
		}
		set := mustEntries(t, entries...)
		options := []rulite.FireOption{rulite.WithPolicy(policy)}
		var events []rulite.Event
		if mode > 0 {
			options = append(options, rulite.WithTrace(), rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
				events = append(events, event)
				if mode == 2 {
					return observationCause
				}
				return nil
			})))
		}
		input := executionInput{}
		result, err := mustReuse(t, set, options...).Fire(ctx, &input)
		cancel(nil)
		if result.StopReason() != stop || !slices.Equal(calls, wantCalls) || input.value != actions || (err != nil) != (len(wantFailures) > 0 || canceled) {
			t.Fatalf("program=%+v policy=%v: stop=%s calls=%v actions=%d error=%v; want %s %v %d", program, policy, result.StopReason(), calls, input.value, err, stop, wantCalls, actions)
		}
		if errors.Is(err, cancelCause) != canceled || errors.Is(err, context.Canceled) != canceled {
			t.Fatal("context causes lost")
		}
		for id, w := range wantRules {
			x, ok := result.Rule(id)
			member, grouped := x.MemberIndex()
			group, _ := x.GroupID()
			if !ok || x.State() != w.state || x.NotEvaluatedReason() != w.reason || x.Order() != w.order || x.TopLevelOrder() != w.top || x.RegistrationIndex() != w.registration || group != w.group || grouped != (group != "") || grouped && member != w.member {
				t.Fatalf("rule=%s got=%+v want=%+v", id, x, w)
			}
		}
		if len(result.Failures()) != len(wantFailures) {
			t.Fatal("failure count changed")
		}
		for i, failure := range result.Failures() {
			w := wantRules[wantFailures[i]]
			if failure.RuleID() != wantFailures[i] || failure.Phase() != w.phase || failure.Continued() != w.continued {
				t.Fatal("failure order or disposition changed")
			}
			if !w.panicked && (!errors.Is(err, cause) || failure.Cause() != cause) {
				t.Fatal("business cause identity lost")
			}
		}
		for id, want := range wantGroups {
			group, ok := result.Group(id)
			chosen, hasSelection := group.SelectedRule()
			if !ok || group.State() != want || group.Resolved() != (selected[id] != "") || chosen != selected[id] || hasSelection != (chosen != "") {
				t.Fatalf("group %s: %s selected=%s; want %s %s", id, group.State(), chosen, want, selected[id])
			}
		}
		if mode == 1 {
			facts := expectedEvents(result)
			if len(events) != len(facts) {
				t.Fatal("group holes emitted rule events")
			}
			for i, event := range events {
				checkObservedEvent(t, event, facts[i], result)
			}
		}
		if mode == 2 && (len(events) != 1 || len(result.Diagnostics()) != 1 || !errors.Is(result.Diagnostics()[0], observationCause) || errors.Is(err, observationCause)) {
			t.Fatal("diagnostic changed group semantics")
		}
		checkResultConsistency(t, result)
	}
}

func TestGroupPolicyMatrix(t *testing.T) {
	for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
		for stop := range 3 {
			for conditionMode := range 2 {
				for actionMode := range 2 {
					for _, codes := range [][]byte{{}, {0, 0}, {3, 3}, {0, 1, 3, 2, 2}, {2, 2}, {4, 2}, {5, 2}, {66, 2}, {130, 2}, {133, 2}} {
						program := []groupEntrySpec{{id: "providers", kind: kind}, {rules: []groupRuleSpec{{id: "audit", code: 2}}}, {id: "later", kind: kind, rules: []groupRuleSpec{{id: "later/member", code: 2}}}}
						for i, code := range codes {
							program[0].rules = append(program[0].rules, groupRuleSpec{id: rulite.RuleID(fmt.Sprintf("provider/%d", i)), code: code})
						}
						policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(stop)).WithConditionErrors(rulite.ErrorMode(conditionMode)).WithActionErrors(rulite.ErrorMode(actionMode))
						checkGroupProgram(t, program, policy)
					}
				}
			}
		}
	}
}

// At most eight top-level entries, four members per group, and 48 input bytes.
// Header bytes select rule/group kind, priority, and group size; member bytes
// select outcomes, partial effects, cancellation boundaries, and local priorities.
func decodeGroupProgram(data []byte) []groupEntrySpec {
	if len(data) > 48 {
		data = data[:48]
	}
	var program []groupEntrySpec
	for cursor := 0; cursor < len(data) && len(program) < 8; {
		header := data[cursor]
		cursor++
		entry := groupEntrySpec{priority: rulite.Priority(int8(header))}
		count := 1
		if header%3 != 0 {
			entry.id = rulite.GroupID(fmt.Sprintf("group/%d", len(program)))
			entry.kind = rulite.GroupKind(header%3 - 1)
			count = int(header / 3 % 5)
		}
		for member := 0; member < count; member++ {
			var code byte
			if cursor < len(data) {
				code = data[cursor]
				cursor++
			}
			priority := rulite.Priority(int8(code))
			if entry.id == "" {
				priority = entry.priority
			}
			entry.rules = append(entry.rules, groupRuleSpec{id: rulite.RuleID(fmt.Sprintf("rule/%d/%d", len(program), member)), priority: priority, code: code})
		}
		program = append(program, entry)
	}
	return program
}

func TestGroupOutcomeProperties(t *testing.T) {
	random := rand.New(rand.NewPCG(89, 137))
	for range 150 {
		data := make([]byte, random.IntN(49))
		for i := range data {
			data[i] = byte(random.UintN(256))
		}
		policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(random.IntN(3))).WithConditionErrors(rulite.ErrorMode(random.IntN(2))).WithActionErrors(rulite.ErrorMode(random.IntN(2)))
		checkGroupProgram(t, decodeGroupProgram(data), policy)
	}
}

func FuzzGroupExecution(f *testing.F) {
	for _, seed := range [][]byte{{}, {1, 2}, {7, 3, 2, 0, 2}, {8, 3, 2, 0, 2}, {14, 0, 1, 3, 2, 0, 2}, {4, 66}, {5, 130}, {5, 133}, {4, 4}, {5, 5}} {
		f.Add(seed, byte(0), byte(1), byte(1))
	}
	f.Fuzz(func(t *testing.T, data []byte, stop, condition, action byte) {
		policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(stop % 3)).WithConditionErrors(rulite.ErrorMode(condition % 2)).WithActionErrors(rulite.ErrorMode(action % 2))
		checkGroupProgram(t, decodeGroupProgram(data), policy)
	})
}
