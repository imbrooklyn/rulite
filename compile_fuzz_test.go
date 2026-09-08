package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func FuzzCompileMetadata(f *testing.F) {
	f.Add([]byte{}, "", "", "")
	f.Add([]byte{1, 3}, "pricing/vip", "VIP offer", "pricing,audit")
	f.Add([]byte{0, 0, 3, 7, 11, 15}, "pricing/vip", " VIP offer ", " a , ,a,VIP,vip,\t")
	f.Add([]byte{0, 0, 1}, strings.Repeat("a", 128), "\u2003description\n", "\u00a0review\u2003,review")
	f.Add([]byte{0, 4, 8}, strings.Repeat("a", 129), "invalid\xff", "bad\xff,bad\xff")
	f.Add([]byte{3, 3, 3}, "INVALID", "", ",")
	syntax := regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
	f.Fuzz(func(t *testing.T, stream []byte, id, text, tagText string) {
		// Bound rules, all string bytes, and tag count; there is no recursion.
		if len(stream) > 32 {
			stream = stream[:32]
		}
		if len(id) > 256 {
			id = id[:256]
		}
		if len(text) > 512 {
			text = text[:512]
		}
		if len(tagText) > 1024 {
			tagText = tagText[:1024]
		}
		tags := strings.SplitN(tagText, ",", 16)
		var wantTags []string
		for _, tag := range tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed != "" && !slices.Contains(wantTags, trimmed) {
				wantTags = append(wantTags, trimmed)
			}
		}
		type expectedIssue struct {
			index int
			id    rulite.RuleID
			cause error
		}
		var want []expectedIssue
		seen := make(map[rulite.RuleID]bool)
		rules := make([]rulite.Rule[int], len(stream))
		for i, flags := range stream {
			ruleID := rulite.RuleID(id)
			if flags&1 != 0 {
				ruleID = rulite.RuleID(fmt.Sprintf("rule/%d", flags%8))
			}
			condition := rulite.Condition[int](func(context.Context, *int) (bool, error) { t.Fatal("Compile invoked condition"); return false, nil })
			action := rulite.Action[int](func(context.Context, *int) error { t.Fatal("Compile invoked action"); return nil })
			valid := syntax.MatchString(string(ruleID))
			issueID := ruleID
			if !valid {
				issueID = ""
				want = append(want, expectedIssue{i, "", rulite.ErrInvalidRule})
			} else if seen[ruleID] {
				want = append(want, expectedIssue{i, ruleID, rulite.ErrDuplicateRuleID})
			}
			if valid {
				seen[ruleID] = true
			}
			if flags&4 != 0 {
				condition = nil
				want = append(want, expectedIssue{i, issueID, rulite.ErrInvalidCondition})
			}
			if flags&8 != 0 {
				action = nil
				want = append(want, expectedIssue{i, issueID, rulite.ErrInvalidRule})
			}
			rules[i] = rulite.NewRule[int](ruleID).Name(text).Description(text).Tags(tags...).Priority(rulite.Priority(int8(flags))).When(condition).Then(action)
		}
		clear(tags)
		set, err := rulite.Compile(rules...)
		engine, engineErr := rulite.NewEngine(rules...)
		if (err == nil) != (len(want) == 0) || (set != nil) != (err == nil) || (engine != nil) != (err == nil) || fmt.Sprint(err) != fmt.Sprint(engineErr) {
			t.Fatal("construction disagrees with validation oracle")
		}
		for range 2 {
			repeated, repeatedErr := rulite.Compile(rules...)
			if fmt.Sprint(repeatedErr) != fmt.Sprint(err) || !reflect.DeepEqual(repeated.Rules(), set.Rules()) {
				t.Fatal("compile order changed")
			}
		}
		if err != nil {
			var validation *rulite.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("unexpected error type %T", err)
			}
			issues := validation.Issues()
			if len(issues) != len(want) {
				t.Fatalf("issues=%d; want %d", len(issues), len(want))
			}
			for i, issue := range issues {
				gotID, ok := issue.RuleID()
				if issue.Index() != want[i].index || gotID != want[i].id || ok != (want[i].id != "") || !errors.Is(issue, want[i].cause) {
					t.Fatal("issue order or identity differs from oracle")
				}
			}
			for ruleID := range seen {
				var filtered []rulite.ValidationIssue
				for _, issue := range issues {
					if id, ok := issue.RuleID(); ok && id == ruleID {
						filtered = append(filtered, issue)
					}
				}
				if !reflect.DeepEqual(filtered, validation.IssuesForRule(ruleID)) {
					t.Fatal("validation index differs from aggregate")
				}
				clear(validation.IssuesForRule(ruleID))
				if !reflect.DeepEqual(filtered, validation.IssuesForRule(ruleID)) {
					t.Fatal("validation index leaked storage")
				}
			}
			return
		}
		if !set.Valid() || set.Len() != len(rules) {
			t.Fatal("successful set is incomplete")
		}
		views := set.Rules()
		for order, info := range views {
			registered := rules[info.RegistrationIndex()]
			indexed, ok := set.Rule(info.ID())
			if !ok || !reflect.DeepEqual(indexed, info) || info.ID() != registered.ID() || info.Order() != order || info.Priority() != registered.Priority() || info.Name() != strings.TrimSpace(text) || info.Description() != strings.TrimSpace(text) || !slices.Equal(info.Tags(), wantTags) {
				t.Fatal("compiled metadata differs from oracle")
			}
			if order > 0 && (views[order-1].Priority() < info.Priority() || views[order-1].Priority() == info.Priority() && views[order-1].RegistrationIndex() >= info.RegistrationIndex()) {
				t.Fatal("compiled order is unstable")
			}
			clear(info.Tags())
			if !slices.Equal(info.Tags(), wantTags) {
				t.Fatal("metadata leaked storage")
			}
		}
	})
}
