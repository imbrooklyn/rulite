package rulite

import (
	"fmt"
	"slices"
)

type ruleMetadata struct {
	id                RuleID
	priority          Priority
	registrationIndex int
}

// snapshotMetadata owns only values and indexes, never executable callbacks.
// It can outlive the executable snapshot without retaining callback captures.
type snapshotMetadata struct {
	rules []ruleMetadata
	byID  map[RuleID]int
}

type ruleCallbacks[T any] struct {
	condition Condition[T]
	action    Action[T]
}

// Both slices use compiled order. All slices and maps are private and become
// read-only before the snapshot is returned.
type compiledSnapshot[T any] struct {
	metadata  *snapshotMetadata
	callbacks []ruleCallbacks[T]
}

func compileRules[T any](rules []Rule[T]) (*compiledSnapshot[T], error) {
	byID := make(map[RuleID]int, len(rules))
	var issues []ValidationIssue
	for index, rule := range rules {
		validID := validRuleID(rule.id)
		var issueID RuleID
		if validID {
			issueID = rule.id
		}
		addIssue := func(cause error) {
			issues = append(issues, ValidationIssue{index: index, ruleID: issueID, cause: cause})
		}
		if !validID {
			addIssue(fmt.Errorf("%w: ID must contain 1 to 128 bytes and match ^[a-z0-9][a-z0-9._/-]{0,127}$", ErrInvalidRule))
		} else if first, exists := byID[rule.id]; exists {
			addIssue(fmt.Errorf("%w: first registered at index %d", ErrDuplicateRuleID, first))
		} else {
			byID[rule.id] = index
		}
		if rule.condition == nil {
			addIssue(fmt.Errorf("%w: condition is nil", ErrInvalidCondition))
		}
		if rule.action == nil {
			addIssue(fmt.Errorf("%w: action is nil", ErrInvalidRule))
		}
	}
	if len(issues) != 0 {
		return nil, &ValidationError{issues: issues}
	}

	metadata := &snapshotMetadata{rules: make([]ruleMetadata, len(rules)), byID: byID}
	for index, rule := range rules {
		metadata.rules[index] = ruleMetadata{id: rule.id, priority: rule.priority, registrationIndex: index}
	}
	slices.SortStableFunc(metadata.rules, func(a, b ruleMetadata) int {
		switch {
		case a.priority > b.priority:
			return -1
		case a.priority < b.priority:
			return 1
		case a.registrationIndex < b.registrationIndex:
			return -1
		case a.registrationIndex > b.registrationIndex:
			return 1
		default:
			return 0
		}
	})
	callbacks := make([]ruleCallbacks[T], len(rules))
	for order, entry := range metadata.rules {
		rule := rules[entry.registrationIndex]
		callbacks[order] = ruleCallbacks[T]{condition: rule.condition, action: rule.action}
		metadata.byID[entry.id] = order
	}
	return &compiledSnapshot[T]{metadata: metadata, callbacks: callbacks}, nil
}

func validRuleID(id RuleID) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for index := 0; index < len(id); index++ {
		c := id[index]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if index > 0 && (c == '.' || c == '_' || c == '/' || c == '-') {
			continue
		}
		return false
	}
	return true
}
