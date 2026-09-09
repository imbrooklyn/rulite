package rulite

import (
	"fmt"
	"slices"
)

type ruleMetadata struct {
	id                RuleID
	priority          Priority
	registrationIndex int
	topLevelOrder     int
	memberIndex       int
	group             *groupMetadata
	details           *ruleDetails
}

// snapshotMetadata owns only values and indexes, never executable callbacks.
// It can outlive the executable snapshot without retaining callback captures.
type snapshotMetadata struct {
	rules     []ruleMetadata
	byID      map[RuleID]int
	groups    []groupMetadata
	byGroupID map[GroupID]int
}

type groupMetadata struct {
	id                GroupID
	kind              GroupKind
	priority          Priority
	registrationIndex int
	topLevelOrder     int
	index             int
	start, end        int
}

type compiledEntry struct {
	start, end int
	group      *groupMetadata
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
	entries   []compiledEntry
}

func compileRules[T any](rules []Rule[T]) (*compiledSnapshot[T], error) {
	return compileDefinitions(definitionList[T]{rules: rules})
}

// Both public construction paths share validation and ordering.
type definitionList[T any] struct {
	rules   []Rule[T]
	entries []Entry[T]
}

func (d definitionList[T]) len() int { return len(d.rules) + len(d.entries) }

func (d definitionList[T]) at(index int) Entry[T] {
	if d.entries != nil {
		return d.entries[index]
	}
	return d.rules[index].Entry()
}

func priorityComparison(a, b Priority) int {
	if a > b {
		return -1
	}
	if a < b {
		return 1
	}
	return 0
}

func compileDefinitions[T any](definitions definitionList[T]) (*compiledSnapshot[T], error) {
	byID := make(map[RuleID]int, definitions.len())
	var byGroupID map[GroupID]int
	var issues []ValidationIssue
	validateRule := func(rule Rule[T], index, member int, groupID GroupID, inGroup bool) {
		validID := validRuleID(rule.id)
		var issueID RuleID
		if validID {
			issueID = rule.id
		}
		addIssue := func(cause error) {
			issues = append(issues, ValidationIssue{index: index, ruleID: issueID, cause: cause, memberIndex: member, inGroup: inGroup, groupID: groupID})
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
	total, groupCount := 0, 0
	for index := 0; index < definitions.len(); index++ {
		entry := definitions.at(index)
		if entry.group == nil {
			validateRule(entry.rule, index, 0, "", false)
			total++
			continue
		}
		group := entry.group
		groupCount++
		total += len(group.members)
		if byGroupID == nil {
			byGroupID = make(map[GroupID]int)
		}
		var issueID GroupID
		if validRuleID(RuleID(group.id)) {
			issueID = group.id
		}
		addIssue := func(cause error) {
			issues = append(issues, ValidationIssue{index: index, groupID: issueID, groupIssue: true, cause: cause})
		}
		if issueID == "" {
			addIssue(fmt.Errorf("%w: ID must contain 1 to 128 bytes and match ^[a-z0-9][a-z0-9._/-]{0,127}$", ErrInvalidGroup))
		} else if first, exists := byGroupID[group.id]; exists {
			addIssue(fmt.Errorf("%w: first registered at index %d", ErrDuplicateGroupID, first))
		} else {
			byGroupID[group.id] = index
		}
		for member, rule := range group.members {
			validateRule(rule, index, member, issueID, true)
		}
	}
	if len(issues) != 0 {
		return nil, newValidationError(issues)
	}

	metadata := &snapshotMetadata{rules: make([]ruleMetadata, 0, total), byID: byID, byGroupID: byGroupID}
	if groupCount != 0 {
		metadata.groups = make([]groupMetadata, groupCount)
	}
	indexes := make([]int, definitions.len())
	for index := range indexes {
		indexes[index] = index
	}
	slices.SortStableFunc(indexes, func(a, b int) int {
		return priorityComparison(definitions.at(a).priority(), definitions.at(b).priority())
	})
	var entries []compiledEntry
	if groupCount != 0 {
		entries = make([]compiledEntry, 0, len(indexes))
	}
	callbacks := make([]ruleCallbacks[T], total)
	groupIndex := 0
	for topOrder, index := range indexes {
		entry := definitions.at(index)
		start := len(metadata.rules)
		if entry.group == nil {
			rule := entry.rule
			metadata.rules = append(metadata.rules, ruleMetadata{id: rule.id, priority: rule.priority, registrationIndex: index, topLevelOrder: topOrder, details: rule.details})
			callbacks[start] = ruleCallbacks[T]{condition: rule.condition, action: rule.action}
			metadata.byID[rule.id] = start
			if groupCount != 0 {
				entries = append(entries, compiledEntry{start: start, end: start + 1})
			}
			continue
		}
		definition := entry.group
		group := &metadata.groups[groupIndex]
		*group = groupMetadata{id: definition.id, kind: definition.kind, priority: definition.priority, registrationIndex: index, topLevelOrder: topOrder, index: groupIndex, start: start, end: start + len(definition.members)}
		metadata.byGroupID[group.id] = groupIndex
		groupIndex++
		for member, rule := range definition.members {
			metadata.rules = append(metadata.rules, ruleMetadata{id: rule.id, priority: rule.priority, registrationIndex: index, topLevelOrder: topOrder, memberIndex: member, group: group, details: rule.details})
		}
		slices.SortStableFunc(metadata.rules[start:group.end], func(a, b ruleMetadata) int { return priorityComparison(a.priority, b.priority) })
		for order := start; order < group.end; order++ {
			info := metadata.rules[order]
			rule := definition.members[info.memberIndex]
			callbacks[order] = ruleCallbacks[T]{condition: rule.condition, action: rule.action}
			metadata.byID[info.id] = order
		}
		entries = append(entries, compiledEntry{start: start, end: group.end, group: group})
	}
	return &compiledSnapshot[T]{metadata: metadata, callbacks: callbacks, entries: entries}, nil
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
