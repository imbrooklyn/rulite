package rulite

// GroupState describes local selection independently of the global StopReason.
type GroupState uint8

const (
	// GroupNotEntered means execution stopped before entering the group.
	GroupNotEntered GroupState = iota
	// GroupResolved means the group's selection criterion was observed.
	// Global termination can still prevent its action or local advancement.
	GroupResolved
	// GroupExhausted means every member was visited without resolving the group.
	// An entered empty group is exhausted. Continued failures remain in Result.
	GroupExhausted
	// GroupInterrupted means global termination prevented an unresolved group
	// from completing, even if the final member was evaluated.
	GroupInterrupted
)

// String returns the local selection state's name.
func (s GroupState) String() string {
	switch s {
	case GroupNotEntered:
		return "not-entered"
	case GroupResolved:
		return "resolved"
	case GroupExhausted:
		return "exhausted"
	case GroupInterrupted:
		return "interrupted"
	default:
		return "unknown"
	}
}

// One record per group owns both selection facts and any uncalled suffix.
// No per-member records are needed for ordinary misses or local holes.
type groupRecord struct {
	state       GroupState
	selected    RuleID
	skippedFrom int
}

func (x *execution) resolveGroup(group *groupMetadata, id RuleID) {
	record := &x.result.groups[group.index]
	record.state, record.selected = GroupResolved, id
}

// GroupResult is an immutable, callback-free group metadata and outcome view.
// Resolution records a first true, nil condition or successful action according
// to Kind, even when global termination wins at the same callback boundary.
// Consult Result.StopReason and individual rules for action and stop outcomes.
// Its zero value has no identity, selection, or entry facts.
type GroupResult struct {
	metadata groupMetadata
	record   groupRecord
}

// ID returns the group identity.
func (g GroupResult) ID() GroupID { return g.metadata.id }

// Kind returns the local selection criterion.
func (g GroupResult) Kind() GroupKind { return g.metadata.kind }

// Priority returns the group's independent top-level priority.
func (g GroupResult) Priority() Priority { return g.metadata.priority }

// Order returns the zero-based compiled top-level entry position.
// It is not a flattened executable rule Order or a member registration index.
func (g GroupResult) Order() int { return g.metadata.topLevelOrder }

// RegistrationIndex returns the group's original CompileEntries argument position.
func (g GroupResult) RegistrationIndex() int { return g.metadata.registrationIndex }

// State returns the local outcome independently of global termination.
func (g GroupResult) State() GroupState { return g.record.state }

// Entered reports that execution passed the group's initial context boundary.
func (g GroupResult) Entered() bool { return g.record.state != GroupNotEntered }

// Resolved reports that the local selection criterion was observed.
func (g GroupResult) Resolved() bool { return g.record.state == GroupResolved }

// SelectedRule returns the selected rule ID, or an empty ID and false.
// First-match selection proves eligibility only; it does not prove action success.
func (g GroupResult) SelectedRule() (RuleID, bool) { return g.record.selected, g.record.selected != "" }

// Group returns a group's captured outcome by exact identity.
// Unknown IDs and a zero Result return a zero view and false.
func (r Result) Group(id GroupID) (GroupResult, bool) {
	if r.metadata == nil {
		return GroupResult{}, false
	}
	index, ok := r.metadata.byGroupID[id]
	if !ok {
		return GroupResult{}, false
	}
	return r.groupAt(index), true
}

func (r Result) groupAt(index int) GroupResult {
	g := GroupResult{metadata: r.metadata.groups[index]}
	if index < len(r.groups) {
		g.record = r.groups[index]
	}
	return g
}

// Groups returns a defensive copy of group views in compiled top-level order.
// Containers are excluded from Counts.Total; all their members are included.
func (r Result) Groups() []GroupResult {
	if r.metadata == nil || len(r.metadata.groups) == 0 {
		return nil
	}
	groups := make([]GroupResult, len(r.metadata.groups))
	for index := range groups {
		groups[index] = r.groupAt(index)
	}
	return groups
}

// Groups returns the same group facts as Result.Groups, in top-level order.
func (e Explanation) Groups() []GroupResult { return e.result.Groups() }
