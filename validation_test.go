package rulite_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func validRule(id rulite.RuleID) rulite.Rule[pricingState] {
	return rulite.NewRule[pricingState](id).When(eligible).Then(applyDiscount)
}

func validationIssues(t *testing.T, rules ...rulite.Rule[pricingState]) (*rulite.ValidationError, []rulite.ValidationIssue) {
	t.Helper()
	engine, err := rulite.NewEngine(rules...)
	if engine != nil || err == nil {
		t.Fatalf("NewEngine() = %v, %v; want nil engine and validation error", engine, err)
	}
	var validation *rulite.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error type = %T; want *ValidationError", err)
	}
	set, compileErr := rulite.Compile(rules...)
	var compiledValidation *rulite.ValidationError
	if set != nil || !errors.As(compileErr, &compiledValidation) || !reflect.DeepEqual(compiledValidation, validation) || compileErr.Error() != err.Error() {
		t.Fatal("Compile and NewEngine returned different validation issues")
	}
	return validation, validation.Issues()
}

func TestValidationIndex(t *testing.T) {
	validation, all := validationIssues(t, validRule("a"), validRule("b"), rulite.NewRule[pricingState]("a").When(nil).Then(nil), validRule("b"), rulite.Rule[pricingState]{})
	for _, id := range []rulite.RuleID{"a", "b", "missing", "", "INVALID"} {
		var want []rulite.ValidationIssue
		for _, issue := range all {
			if issueID, ok := issue.RuleID(); ok && issueID == id {
				want = append(want, issue)
			}
		}
		if !reflect.DeepEqual(validation.IssuesForRule(id), want) {
			t.Fatalf("incorrect indexed issues for %q", id)
		}
		clear(validation.IssuesForRule(id))
		if !reflect.DeepEqual(validation.IssuesForRule(id), want) {
			t.Fatal("indexed issues expose mutable storage")
		}
	}
	for _, empty := range []*rulite.ValidationError{nil, {}} {
		if len(empty.IssuesForRule("a")) != 0 {
			t.Fatal("zero error has indexed issues")
		}
	}
}

func TestRuleIDValidation(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		valid bool
	}{
		{"empty", "", false},
		{"one_letter", "a", true},
		{"one_digit", "0", true},
		{"all_allowed_characters", "abcdefghijklmnopqrstuvwxyz0123456789._/-", true},
		{"maximum_length", strings.Repeat("a", 128), true},
		{"over_maximum_length", strings.Repeat("a", 129), false},
		{"maximum_with_separator", strings.Repeat("a", 127) + "/", true},
		{"uppercase_first", "Pricing/discount", false},
		{"uppercase_later", "pricing/Discount", false},
		{"leading_space", " pricing/discount", false},
		{"trailing_space", "pricing/discount ", false},
		{"embedded_space", "pricing/ discount", false},
		{"leading_period", ".pricing", false},
		{"leading_underscore", "_pricing", false},
		{"leading_slash", "/pricing", false},
		{"leading_hyphen", "-pricing", false},
		{"trailing_separator", "pricing/", true},
		{"repeated_separator", "pricing//discount", true},
		{"colon", "pricing:discount", false},
		{"backslash", "pricing\\discount", false},
		{"newline", "pricing\n", false},
		{"tab", "pricing\t", false},
		{"nul", "pricing\x00", false},
		{"non_ascii", "pricing/\xc3\xa9", false},
		{"invalid_utf8", "pricing/\xff", false},
		{"byte_length_boundary", strings.Repeat("a", 127) + "\xc3\xa9", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := validRule(rulite.RuleID(tc.id))
			if rule.ID() != rulite.RuleID(tc.id) {
				t.Fatal("builder changed the supplied ID")
			}
			engine, err := rulite.NewEngine(rule)
			if tc.valid {
				if err != nil || engine == nil {
					t.Fatalf("valid ID %q rejected: %v", tc.id, err)
				}
				return
			}
			if engine != nil || !errors.Is(err, rulite.ErrInvalidRule) {
				t.Fatalf("invalid ID %q: got %v, %v; want ErrInvalidRule", tc.id, engine, err)
			}
			_, issues := validationIssues(t, rule)
			if len(issues) != 1 || issues[0].Index() != 0 {
				t.Fatalf("issues = %v; want one ID issue at index 0", issues)
			}
			if id, ok := issues[0].RuleID(); ok || id != "" {
				t.Fatalf("invalid ID available as %q, %t", id, ok)
			}
		})
	}
}

func TestRuleIDASCIICharacters(t *testing.T) {
	for value := 0; value < 128; value++ {
		character := string(byte(value))
		for _, prefix := range []string{"", "a"} {
			t.Run(fmt.Sprintf("prefix_%d_byte_%02x", len(prefix), value), func(t *testing.T) {
				allowed := "abcdefghijklmnopqrstuvwxyz0123456789"
				if prefix != "" {
					allowed += "._/-"
				}
				wantValid := strings.Contains(allowed, character)
				_, err := rulite.NewEngine(validRule(rulite.RuleID(prefix + character)))
				if (err == nil) != wantValid {
					t.Fatalf("ID %q: err = %v; want valid = %t", prefix+character, err, wantValid)
				}
			})
		}
	}
}

func TestDuplicateRuleID(t *testing.T) {
	rule := validRule("pricing/discount")
	validation, issues := validationIssues(t, rule, validRule("pricing/audit"), rule, rule)
	if len(issues) != 2 {
		t.Fatalf("issues = %v; want two duplicates", issues)
	}
	if !errors.Is(validation, rulite.ErrDuplicateRuleID) {
		t.Fatal("aggregate does not unwrap to ErrDuplicateRuleID")
	}
	for index, issue := range issues {
		if issue.Index() != index+2 || !errors.Is(issue, rulite.ErrDuplicateRuleID) {
			t.Fatalf("issue %d = %v; want duplicate at index %d", index, issue, index+2)
		}
		if id, ok := issue.RuleID(); !ok || id != rule.ID() {
			t.Fatalf("duplicate RuleID() = %q, %t", id, ok)
		}
		if !strings.Contains(issue.Error(), "first registered at index 0") {
			t.Errorf("duplicate lost its first registration: %v", issue)
		}
	}
}

func TestValidationIssueOrder(t *testing.T) {
	rules := []rulite.Rule[pricingState]{
		rulite.NewRule[pricingState]("INVALID").Priority(-100).When(nil).Then(nil),
		rulite.NewRule[pricingState]("pricing/duplicate").When(nil).Then(nil),
		rulite.NewRule[pricingState]("INVALID").Priority(100).When(nil).Then(nil),
		rulite.NewRule[pricingState]("pricing/duplicate").Priority(200).When(nil).Then(nil),
		{},
		{},
		validRule("pricing/valid"),
		validRule("pricing/duplicate"),
	}
	want := []struct {
		index int
		cause error
		field string
	}{
		{0, rulite.ErrInvalidRule, "ID must"},
		{0, rulite.ErrInvalidCondition, "condition is nil"},
		{0, rulite.ErrInvalidRule, "action is nil"},
		{1, rulite.ErrInvalidCondition, "condition is nil"},
		{1, rulite.ErrInvalidRule, "action is nil"},
		{2, rulite.ErrInvalidRule, "ID must"},
		{2, rulite.ErrInvalidCondition, "condition is nil"},
		{2, rulite.ErrInvalidRule, "action is nil"},
		{3, rulite.ErrDuplicateRuleID, "duplicate rule ID"},
		{3, rulite.ErrInvalidCondition, "condition is nil"},
		{3, rulite.ErrInvalidRule, "action is nil"},
		{4, rulite.ErrInvalidRule, "ID must"},
		{4, rulite.ErrInvalidCondition, "condition is nil"},
		{4, rulite.ErrInvalidRule, "action is nil"},
		{5, rulite.ErrInvalidRule, "ID must"},
		{5, rulite.ErrInvalidCondition, "condition is nil"},
		{5, rulite.ErrInvalidRule, "action is nil"},
		{7, rulite.ErrDuplicateRuleID, "duplicate rule ID"},
	}
	var previousMessage string
	for attempt := 0; attempt < 10; attempt++ {
		validation, issues := validationIssues(t, rules...)
		if len(issues) != len(want) {
			t.Fatalf("got %d issues; want %d: %v", len(issues), len(want), issues)
		}
		for index, expected := range want {
			issue := issues[index]
			if issue.Index() != expected.index || !errors.Is(issue, expected.cause) || !strings.Contains(issue.Error(), expected.field) {
				t.Errorf("issue %d = %v; want index %d, cause %v, field %q", index, issue, expected.index, expected.cause, expected.field)
			}
			wantID := rulite.RuleID("")
			if expected.index == 1 || expected.index == 3 || expected.index == 7 {
				wantID = "pricing/duplicate"
			}
			if id, ok := issue.RuleID(); id != wantID || ok != (wantID != "") {
				t.Errorf("issue %d: RuleID() = %q, %t; want %q", index, id, ok, wantID)
			}
		}
		if attempt > 0 && validation.Error() != previousMessage {
			t.Fatal("validation message order changed across identical constructions")
		}
		previousMessage = validation.Error()
	}
}

func TestNilCallbacks(t *testing.T) {
	cases := []struct {
		name      string
		condition rulite.Condition[pricingState]
		action    rulite.Action[pricingState]
		cause     error
	}{
		{"nil_condition", nil, applyDiscount, rulite.ErrInvalidCondition},
		{"nil_action", eligible, nil, rulite.ErrInvalidRule},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validation, issues := validationIssues(t, rulite.NewRule[pricingState]("pricing/discount").When(tc.condition).Then(tc.action))
			if len(issues) != 1 || !errors.Is(validation, tc.cause) {
				t.Fatalf("validation = %v; want one issue wrapping %v", validation, tc.cause)
			}
		})
	}
}

func TestValidationErrorAccessors(t *testing.T) {
	rule := validRule("pricing/discount")
	validation, issues := validationIssues(t, rule, rule, rulite.Rule[pricingState]{})
	message := validation.Error()
	var issue rulite.ValidationIssue
	if !errors.As(validation, &issue) || issue.Index() != 1 {
		t.Fatal("errors.As did not find the first validation issue")
	}
	if issue.Cause() == nil || issue.Unwrap() != issue.Cause() {
		t.Fatal("Cause and Unwrap do not preserve the same error")
	}
	for _, cause := range []error{rulite.ErrDuplicateRuleID, rulite.ErrInvalidRule, rulite.ErrInvalidCondition} {
		if !errors.Is(validation, cause) {
			t.Errorf("aggregate lost cause %v", cause)
		}
	}
	wrapped := validation.Unwrap()
	if len(wrapped) != len(issues) {
		t.Fatalf("Unwrap length = %d; want %d", len(wrapped), len(issues))
	}
	for index, child := range wrapped {
		var extracted rulite.ValidationIssue
		if !errors.As(child, &extracted) || extracted.Index() != issues[index].Index() || extracted.Cause() != issues[index].Cause() {
			t.Errorf("Unwrap child %d differs from Issues", index)
		}
	}
	issues[0] = rulite.ValidationIssue{}
	wrapped[0] = nil
	if validation.Error() != message || validation.Issues()[0].Index() != 1 || validation.Unwrap()[0] == nil {
		t.Fatal("mutating accessor slices changed the validation error")
	}
	if !errors.Is(validation, rulite.ErrDuplicateRuleID) {
		t.Fatal("mutating an accessor slice changed the unwrap tree")
	}
}

func TestValidationZeroValues(t *testing.T) {
	var issue rulite.ValidationIssue
	if issue.Error() == "" || issue.Unwrap() != nil || issue.Cause() != nil {
		t.Fatal("zero issue accessors are inconsistent")
	}
	if id, ok := issue.RuleID(); id != "" || ok {
		t.Fatal("zero issue has an ID")
	}
	for _, validation := range []*rulite.ValidationError{{}, nil} {
		if validation.Error() == "" || len(validation.Issues()) != 0 || len(validation.Unwrap()) != 0 {
			t.Fatal("empty validation error accessors are inconsistent")
		}
	}
}
