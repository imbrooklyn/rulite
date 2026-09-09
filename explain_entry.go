package rulite

// EntryExplanation is an immutable view of one compiled top-level rule or group.
// Rule and Group report which definition is present; Members returns copied
// execution views for a group's members. Its zero value has neither definition.
// It retains only the same callback-free ledger and metadata as Explanation.
type EntryExplanation struct {
	result Result
	entry  compiledEntry
	order  int
}

// Entries returns a defensive copy of top-level views in compiled order,
// including empty and unentered groups. Members retain their group-local order.
// A zero Explanation returns an empty collection.
func (e Explanation) Entries() []EntryExplanation {
	if e.result.metadata == nil {
		return nil
	}
	entries := make([]EntryExplanation, e.entryCount())
	for order := range entries {
		entries[order] = e.entryAt(order)
	}
	return entries
}

func (e Explanation) entryCount() int {
	if len(e.result.metadata.entries) != 0 {
		return len(e.result.metadata.entries)
	}
	return e.result.counts.Total
}

func (e Explanation) entryAt(order int) EntryExplanation {
	entry := compiledEntry{start: order, end: order + 1}
	if len(e.result.metadata.entries) != 0 {
		entry = e.result.metadata.entries[order]
	}
	return EntryExplanation{result: e.result, entry: entry, order: order}
}

// Order returns the zero-based compiled top-level position, excluding members.
func (e EntryExplanation) Order() int { return e.order }

// Rule returns a top-level rule outcome, or a zero view and false for a group or zero entry.
func (e EntryExplanation) Rule() (RuleExecution, bool) {
	if e.result.metadata == nil || e.entry.group != nil {
		return RuleExecution{}, false
	}
	return e.result.ruleAt(e.entry.start), true
}

// Group returns a group outcome, or a zero view and false for a rule or zero entry.
func (e EntryExplanation) Group() (GroupResult, bool) {
	if e.entry.group == nil {
		return GroupResult{}, false
	}
	return e.result.groupAt(e.entry.group.index), true
}

// Members returns a defensive copy of all member outcomes in compiled local
// order, including unevaluated members. Rules, empty groups and zero entries
// return an empty collection. Each member keeps its flattened executable Order.
func (e EntryExplanation) Members() []RuleExecution {
	if e.entry.group == nil {
		return nil
	}
	members := make([]RuleExecution, e.entry.end-e.entry.start)
	for index := range members {
		members[index] = e.result.ruleAt(e.entry.start + index)
	}
	return members
}
