package rulite

import "slices"

// GroupID identifies a top-level selection group. It follows RuleID's syntax
// and exact comparison rules, in a separate namespace unique within a RuleSet.
type GroupID string

// GroupKind identifies a group's local selection criterion.
type GroupKind uint8

const (
	// GroupFirstMatch selects the first condition returning true, nil.
	GroupFirstMatch GroupKind = iota
	// GroupFirstFire selects the first action returning nil.
	GroupFirstFire
)

// String returns the selection criterion's name.
func (k GroupKind) String() string {
	switch k {
	case GroupFirstMatch:
		return "first-match"
	case GroupFirstFire:
		return "first-fire"
	default:
		return "unknown"
	}
}

// Group is an immutable definition of local selection over typed rules.
// Construct it with FirstMatchGroup or FirstFireGroup. Its zero value is invalid.
// Empty groups are valid. Members cannot be groups; callback captures remain
// caller-owned. Validation and ordering occur in CompileEntries.
type Group[T any] struct {
	id       GroupID
	kind     GroupKind
	priority Priority
	members  []Rule[T]
}

// FirstMatchGroup copies members into a group with default priority zero.
// The first true, nil condition selects its rule. Its action is attempted once,
// subject to global context and panic boundaries. A continued action error does
// not permit another member to run. All error modes come from the global policy.
func FirstMatchGroup[T any](id GroupID, members ...Rule[T]) Group[T] {
	return Group[T]{id: id, kind: GroupFirstMatch, members: slices.Clone(members)}
}

// FirstFireGroup copies members into a group with default priority zero.
// Only a successful action selects a rule. ContinueOnError permits fallback
// after failed actions, whose partial effects remain visible. This guarantees
// at most one successful action, not exactly one external effect.
func FirstFireGroup[T any](id GroupID, members ...Rule[T]) Group[T] {
	return Group[T]{id: id, kind: GroupFirstFire, members: slices.Clone(members)}
}

// ID returns the group identity.
func (g Group[T]) ID() GroupID { return g.id }

// Kind returns the local selection criterion.
func (g Group[T]) Kind() GroupKind { return g.kind }

// Priority returns the top-level priority, independent of member priorities.
func (g Group[T]) Priority() Priority { return g.priority }

// WithPriority returns an independent group with the supplied top-level priority.
// Every Priority value is valid; repeated calls use the last supplied value.
func (g Group[T]) WithPriority(priority Priority) Group[T] {
	g.priority = priority
	return g
}

// Members returns a defensive copy of definitions in local registration order.
func (g Group[T]) Members() []Rule[T] { return slices.Clone(g.members) }

// Entry is an immutable typed top-level rule or group definition for CompileEntries.
// Obtain it with Rule.Entry or Group.Entry. Its zero value is an invalid zero rule.
// It exposes neither compiled nodes nor mutable definition storage.
type Entry[T any] struct {
	rule  Rule[T]
	group *Group[T]
}

// Entry returns this rule as a top-level definition without changing it.
func (r Rule[T]) Entry() Entry[T] { return Entry[T]{rule: r} }

// Entry returns this group as a top-level definition without changing it.
func (g Group[T]) Entry() Entry[T] { return Entry[T]{group: &g} }

func (e Entry[T]) priority() Priority {
	if e.group != nil {
		return e.group.priority
	}
	return e.rule.priority
}
