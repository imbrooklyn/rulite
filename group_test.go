package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func mustEntries[T any](t testing.TB, entries ...rulite.Entry[T]) *rulite.RuleSet[T] {
	t.Helper()
	set, err := rulite.CompileEntries(entries...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestGroupDefinitionOwnershipAndOrdering(t *testing.T) {
	rule := func(id rulite.RuleID, priority rulite.Priority, matched bool) rulite.Rule[executionInput] {
		return rulite.NewRule[executionInput](id).Priority(priority).When(constantCondition(matched, nil)).Then(successfulAction)
	}
	members := []rulite.Rule[executionInput]{rule("low", math.MinInt32, false), rule("high/first", math.MaxInt32, false), rule("high/second", math.MaxInt32, false)}
	base := rulite.FirstMatchGroup("offers", members...)
	changed := base.WithPriority(50).WithPriority(-1)
	clear(members)
	clear(base.Members())
	if base.ID() != "offers" || base.Kind() != rulite.GroupFirstMatch || base.Priority() != 0 || changed.Priority() != -1 || len(base.Members()) != 3 || base.Members()[0].ID() != "low" {
		t.Fatal("group definition was mutated")
	}
	entries := []rulite.Entry[executionInput]{rule("audit", -1, true).Entry(), base.Entry(), rule("before", 1, true).Entry(), rulite.FirstFireGroup("same-priority", rule("peer", 0, false)).Entry(), rule("after", 0, false).Entry()}
	set := mustEntries(t, entries...)
	clear(entries)
	engine := mustReuse(t, set)
	result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	if err != nil || set.Len() != 7 {
		t.Fatalf("execution=%v, size=%d", err, set.Len())
	}
	wantIDs := []rulite.RuleID{"before", "high/first", "high/second", "low", "peer", "after", "audit"}
	wantTop := []int{0, 1, 1, 1, 2, 3, 4}
	wantRegistration := []int{2, 1, 1, 1, 3, 4, 0}
	wantMembers := []int{0, 1, 2, 0, 0, 0, 0}
	for order, info := range set.Rules() {
		x, _ := result.Rule(info.ID())
		local, member := info.MemberIndex()
		group, grouped := info.GroupID()
		if info.ID() != wantIDs[order] || info.Order() != order || info.TopLevelOrder() != wantTop[order] || info.RegistrationIndex() != wantRegistration[order] || local != wantMembers[order] || member != (order >= 1 && order <= 4) || grouped != member {
			t.Fatalf("incorrect coordinates for %s", info.ID())
		}
		xGroup, _ := x.GroupID()
		xMember, _ := x.MemberIndex()
		if group != xGroup || xMember != local || x.TopLevelOrder() != info.TopLevelOrder() {
			t.Fatal("execution lost group coordinates")
		}
	}
	groups := result.Groups()
	if len(groups) != 2 || groups[0].ID() != "offers" || groups[0].Priority() != 0 || groups[0].Order() != 1 || groups[0].RegistrationIndex() != 1 || groups[0].State() != rulite.GroupExhausted || groups[1].Kind() != rulite.GroupFirstFire {
		t.Fatal("group metadata or stable top-level order changed")
	}
	if !reflect.DeepEqual(groups, result.Explain().Groups()) {
		t.Fatal("explanation differs from group ledger")
	}
	clear(groups)
	clear(result.Explain().Groups())
	if result.Groups()[0].ID() != "offers" {
		t.Fatal("group views expose storage")
	}
	checkResultConsistency(t, result)
}

func TestGroupValidationCoordinatesAndAggregate(t *testing.T) {
	valid := rulite.NewRule[int]("shared").When(func(context.Context, *int) (bool, error) { t.Fatal("compile called condition"); return true, nil }).Then(func(context.Context, *int) error { t.Fatal("compile called action"); return nil })
	bad := rulite.NewRule[int]("shared").When(nil).Then(nil)
	set, err := rulite.CompileEntries(valid.Entry(), rulite.FirstMatchGroup("shared", bad, rulite.Rule[int]{}).Entry(), rulite.FirstFireGroup[int]("shared").Entry(), rulite.Group[int]{}.Entry(), rulite.Entry[int]{})
	var validation *rulite.ValidationError
	if set != nil || !errors.As(err, &validation) {
		t.Fatal("invalid definitions accepted")
	}
	for _, cause := range []error{rulite.ErrInvalidRule, rulite.ErrInvalidCondition, rulite.ErrDuplicateRuleID, rulite.ErrInvalidGroup, rulite.ErrDuplicateGroupID} {
		if !errors.Is(err, cause) {
			t.Fatalf("missing cause %v", cause)
		}
	}
	issues := validation.Issues()
	if len(issues) != 11 {
		t.Fatalf("issues=%v", issues)
	}
	for i, issue := range issues {
		member, inGroup := issue.MemberIndex()
		group, hasGroup := issue.GroupID()
		wantIndex, wantMember := 1, 0
		if i >= 3 {
			wantMember = 1
		}
		if i == 6 {
			wantIndex = 2
		}
		if i == 7 {
			wantIndex = 3
		}
		if i >= 8 {
			wantIndex = 4
		}
		if issue.Index() != wantIndex || inGroup != (i < 6) || inGroup && member != wantMember || hasGroup != (i <= 6) || hasGroup && group != "shared" {
			t.Fatalf("issue %d has ambiguous coordinates: %v", i, issue)
		}
	}
	if len(validation.IssuesForRule("shared")) != 3 {
		t.Fatal("member failures missing from RuleID index")
	}
	clear(issues)
	if len(validation.Issues()) != 11 {
		t.Fatal("issues storage exposed")
	}
	// A group may share its ID with a rule because the namespaces are independent.
	if set := mustEntries(t, rulite.FirstMatchGroup("shared", valid).Entry()); set.Len() != 1 {
		t.Fatal("namespaces were combined")
	}
	for _, id := range []rulite.GroupID{"", "UPPER", " leading", "/leading", rulite.GroupID(strings.Repeat("a", 129))} {
		if set, err := rulite.CompileEntries(rulite.FirstFireGroup[int](id).Entry()); set != nil || !errors.Is(err, rulite.ErrInvalidGroup) {
			t.Fatalf("invalid group ID %q accepted", id)
		}
	}
	for _, id := range []rulite.GroupID{"a", "0._/-", rulite.GroupID(strings.Repeat("a", 128))} {
		mustEntries(t, rulite.FirstFireGroup[int](id).Entry())
	}
	for _, definitions := range [][]rulite.Entry[int]{
		{rulite.FirstMatchGroup("first", valid, valid).Entry()},
		{rulite.FirstMatchGroup("first", valid).Entry(), rulite.FirstFireGroup("second", valid).Entry()},
		{rulite.FirstMatchGroup("first", valid).Entry(), valid.Entry()},
	} {
		if _, err := rulite.CompileEntries(definitions...); !errors.Is(err, rulite.ErrDuplicateRuleID) {
			t.Fatal("member duplicate accepted")
		}
	}
}

func TestEmptyGroupsAndZeroViews(t *testing.T) {
	var members []rulite.Rule[int]
	set := mustEntries(t, rulite.FirstMatchGroup("empty/match", members...).Entry(), rulite.FirstFireGroup[int]("empty/fire").Entry())
	engine := mustReuse(t, set)
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		result, err := engine.Fire(ctx, new(int), rulite.WithTrace())
		cancel()
		if result.Counts() != (rulite.Counts{}) || (err != nil) != canceled {
			t.Fatal("containers counted as rules")
		}
		for _, group := range result.Groups() {
			want := rulite.GroupExhausted
			if canceled {
				want = rulite.GroupNotEntered
			}
			if group.State() != want || group.Entered() == canceled || group.Resolved() {
				t.Fatal("empty group state incorrect")
			}
		}
		checkResultConsistency(t, result)
	}
	empty := mustEntries[int](t)
	if !empty.Valid() || empty.Len() != 0 {
		t.Fatal("empty entry list invalid")
	}
	var zero rulite.Result
	if len(zero.Groups()) != 0 || len(zero.Explain().Groups()) != 0 {
		t.Fatal("zero result has groups")
	}
	if view, ok := zero.Group("unknown"); ok || view.ID() != "" || view.Entered() {
		t.Fatal("zero group lookup unsafe")
	}
	result, _ := engine.Fire(context.Background(), new(int))
	if _, ok := result.Group("unknown"); ok {
		t.Fatal("unknown group found")
	}
	for state, name := range []string{"not-entered", "resolved", "exhausted", "interrupted"} {
		if rulite.GroupState(state).String() != name {
			t.Fatal("group state name changed")
		}
	}
	if rulite.GroupState(255).String() != "unknown" || rulite.GroupKind(255).String() != "unknown" {
		t.Fatal("invalid enum has a valid name")
	}
}

func TestGroupPartialMutationAndPanicPropagation(t *testing.T) {
	cause := errors.New("reservation unavailable")
	for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
		first := rulite.NewRule[executionInput]("first").When(constantCondition(true, nil)).Then(func(_ context.Context, input *executionInput) error { input.value = 7; return cause })
		backup := rulite.NewRule[executionInput]("backup").When(func(_ context.Context, input *executionInput) (bool, error) {
			if kind == rulite.GroupFirstMatch {
				t.Fatal("first-match attempted fallback")
			}
			if input.value != 7 {
				t.Fatal("fallback lost partial mutation")
			}
			return true, nil
		}).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil })
		group := rulite.FirstMatchGroup("providers", first, backup)
		if kind == rulite.GroupFirstFire {
			group = rulite.FirstFireGroup("providers", first, backup)
		}
		audit := rulite.NewRule[executionInput]("audit").When(func(_ context.Context, input *executionInput) (bool, error) {
			want := 7
			if kind == rulite.GroupFirstFire {
				want++
			}
			if input.value != want {
				t.Fatal("audit lost previous action effects")
			}
			return true, nil
		}).Then(successfulAction)
		engine := mustReuse(t, mustEntries(t, group.Entry(), audit.Entry()))
		result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)))
		if !errors.Is(err, cause) || result.StopReason() != rulite.StopCompleted {
			t.Fatal("group swallowed failure or stopped audit")
		}
	}
	for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
		for _, phase := range []rulite.Phase{rulite.ConditionPhase, rulite.ActionPhase} {
			payload := &struct{}{}
			first := rulite.NewRule[executionInput]("panic").When(func(context.Context, *executionInput) (bool, error) {
				if phase == rulite.ConditionPhase {
					panic(payload)
				}
				return true, nil
			}).Then(func(_ context.Context, input *executionInput) error { input.value = 9; panic(payload) })
			group := rulite.FirstMatchGroup("providers", first)
			if kind == rulite.GroupFirstFire {
				group = rulite.FirstFireGroup("providers", first)
			}
			engine := mustReuse(t, mustEntries(t, group.Entry()))
			input := executionInput{}
			func() {
				defer func() {
					if recover() != payload {
						t.Error("group changed propagated panic")
					}
				}()
				_, _ = engine.Fire(context.Background(), &input, rulite.WithPanicMode(rulite.PropagatePanics))
				t.Error("panic did not propagate")
			}()
			if phase == rulite.ActionPhase && input.value != 9 {
				t.Fatal("panic rolled back mutation")
			}
		}
	}
}

func TestGroupSharedEngineAndSynchronizedInput(t *testing.T) {
	rule := func(id rulite.RuleID, match bool) rulite.Rule[executionInput] {
		return rulite.NewRule[executionInput](id).When(constantCondition(match, nil)).Then(func(_ context.Context, input *executionInput) error { input.value++; return nil })
	}
	set := mustEntries(t, rulite.FirstMatchGroup("offers", rule("winner", true), rule("uncalled", true)).Entry(), rule("audit", true).Entry())
	engine := mustReuse(t, set)
	retained, _ := engine.Fire(context.Background(), &executionInput{}, rulite.WithTrace())
	for _, concurrency := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", concurrency), func(t *testing.T) {
			var workers sync.WaitGroup
			for range concurrency {
				workers.Go(func() {
					for range 8 {
						input := executionInput{}
						result, err := engine.Fire(context.Background(), &input)
						if err != nil || input.value != 2 || result.Counts().NotEvaluated != 1 {
							t.Error("shared execution changed")
							return
						}
						clear(retained.Groups())
						clear(retained.Explain().Groups())
						checkResultConsistency(t, retained)
					}
				})
			}
			workers.Wait()
		})
	}
	var shared executionInput
	var mu sync.Mutex
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			mu.Lock()
			defer mu.Unlock()
			if _, err := engine.Fire(context.Background(), &shared); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if shared.value != 32 {
		t.Fatal("caller synchronization lost mutations")
	}
}
