package rulite

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidGroup identifies an invalid group identity.
	ErrInvalidGroup = errors.New("rulite: invalid group")
	// ErrDuplicateGroupID identifies a repeated, syntactically valid group ID.
	ErrDuplicateGroupID = errors.New("rulite: duplicate group ID")
	// ErrInvalidRuleSet identifies a nil or uninitialized RuleSet.
	ErrInvalidRuleSet = errors.New("rulite: invalid rule set")
	// ErrInvalidRule identifies an invalid rule ID or a nil action.
	ErrInvalidRule = errors.New("rulite: invalid rule")
	// ErrDuplicateRuleID identifies a repeated, syntactically valid rule ID.
	ErrDuplicateRuleID = errors.New("rulite: duplicate rule ID")
	// ErrInvalidCondition identifies a nil rule condition or a nil child
	// reached during condition composition.
	ErrInvalidCondition = errors.New("rulite: invalid condition")
)

// ValidationIssue is one immutable construction error, associated with an
// original registration index and, when syntactically valid, a rule ID.
// Unwrap and Cause preserve the underlying error for errors.Is and errors.As.
type ValidationIssue struct {
	index       int
	ruleID      RuleID
	cause       error
	groupID     GroupID
	memberIndex int
	inGroup     bool
	groupIssue  bool
}

// Error returns a description of the issue, including its registration index
// and its rule ID when available. The exact text is not a stable format.
func (i ValidationIssue) Error() string {
	var location string
	if i.groupIssue {
		location = fmt.Sprintf("group %q at registration index %d", i.groupID, i.index)
	} else if id, ok := i.RuleID(); ok {
		location = fmt.Sprintf("rule %q at registration index %d", id, i.index)
	} else {
		location = fmt.Sprintf("rule at registration index %d", i.index)
	}
	if i.inGroup {
		location += fmt.Sprintf(" in group %q at member index %d", i.groupID, i.memberIndex)
	}
	if i.cause == nil {
		return location
	}
	return location + ": " + i.cause.Error()
}

// Unwrap returns the underlying cause.
func (i ValidationIssue) Unwrap() error {
	return i.cause
}

// Index returns the original zero-based top-level registration index.
// For a member issue this is the group's CompileEntries argument position.
// An issue concerning an entire collection uses -1.
func (i ValidationIssue) Index() int {
	return i.index
}

// MemberIndex returns the zero-based local registration index for a member issue.
// Top-level rule and group issues return zero and false.
func (i ValidationIssue) MemberIndex() (int, bool) { return i.memberIndex, i.inGroup }

// GroupID returns a valid group identity for group and member issues.
// Invalid group IDs and top-level rule issues return an empty ID and false.
func (i ValidationIssue) GroupID() (GroupID, bool) { return i.groupID, i.groupID != "" }

// RuleID returns the rule ID and true if the ID is syntactically valid.
// Otherwise it returns an empty ID and false.
func (i ValidationIssue) RuleID() (RuleID, bool) {
	return i.ruleID, i.ruleID != ""
}

// Cause returns the underlying cause, also returned by Unwrap.
func (i ValidationIssue) Cause() error {
	return i.cause
}

// ValidationError aggregates all definition construction issues in registration
// order. Each rule contributes issues in this order: ID syntax, duplicate
// valid ID, nil condition, nil action. Only the second and later occurrences
// of a valid ID are duplicates. Invalid IDs do not participate in that check.
// CompileEntries checks a group's ID before its members in local registration
// order. Compile, CompileEntries, and NewEngine return *ValidationError on failure.
type ValidationError struct {
	issues []ValidationIssue
	byID   map[RuleID][]int
}

func newValidationError(issues []ValidationIssue) *ValidationError {
	e := &ValidationError{issues: issues, byID: make(map[RuleID][]int)}
	for index, issue := range issues {
		if id, ok := issue.RuleID(); ok {
			e.byID[id] = append(e.byID[id], index)
		}
	}
	return e
}

// IssuesForRule returns a defensive copy of issues for an exact, valid RuleID
// in validation order, using an index built during compilation. Unknown IDs,
// invalid IDs, and nil or zero errors return an empty collection, which may be
// nil. Issues without a valid ID remain available through Issues and Unwrap.
func (e *ValidationError) IssuesForRule(id RuleID) []ValidationIssue {
	if e == nil || len(e.byID[id]) == 0 {
		return nil
	}
	indexes := e.byID[id]
	issues := make([]ValidationIssue, len(indexes))
	for index, position := range indexes {
		issues[index] = e.issues[position]
	}
	return issues
}

// Error describes all issues in validation order.
// The exact text is not a stable format.
func (e *ValidationError) Error() string {
	var message strings.Builder
	message.WriteString("rulite: validation failed")
	if e != nil {
		for _, issue := range e.issues {
			message.WriteString("; ")
			message.WriteString(issue.Error())
		}
	}
	return message.String()
}

// Unwrap returns a new slice containing the issues as errors in validation
// order. It supports errors.Is and errors.As across every issue.
func (e *ValidationError) Unwrap() []error {
	if e == nil || len(e.issues) == 0 {
		return nil
	}
	causes := make([]error, len(e.issues))
	for index, issue := range e.issues {
		causes[index] = issue
	}
	return causes
}

// Issues returns a defensive copy of the issues in validation order.
// An empty collection may be nil.
func (e *ValidationError) Issues() []ValidationIssue {
	if e == nil {
		return nil
	}
	return append([]ValidationIssue(nil), e.issues...)
}
