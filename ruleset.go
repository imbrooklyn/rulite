package rulite

// RuleSet holds an immutable, validated and sorted snapshot of typed rules.
// Construct it with Compile or CompileEntries. A nil pointer and the zero value are invalid,
// distinct from a valid empty set. All queries are safe on nil and zero sets.
// Copies share the snapshot; getters never expose callbacks or mutable storage.
// A set supports concurrent queries and construction of independent engines.
// Callers must synchronize any shared mutable callback captures.
type RuleSet[T any] struct {
	snapshot *compiledSnapshot[T]
	identity SnapshotInfo
}

// Compile validates rules and freezes their metadata, registration indexes,
// descending priority order, and ID lookup index. Equal priorities preserve
// registration order. It never calls conditions or actions, accepts no sample
// input, and does not inspect business fields. Metadata is already normalized
// and owned by the immutable rule builders.
//
// The supplied rule slice is neither changed nor retained. Validation aggregates
// all issues in registration order: ID syntax, duplicate valid ID, nil condition,
// then nil action. Invalid IDs do not participate in duplicate detection.
// Failure returns nil and *ValidationError; no partial set is returned.
// An empty list is valid; with no arguments write Compile[T]().
func Compile[T any](rules ...Rule[T]) (*RuleSet[T], error) {
	snapshot, err := compileRules(rules)
	if err != nil {
		return nil, err
	}
	return &RuleSet[T]{snapshot: snapshot}, nil
}

// CompileEntries validates and freezes mixed top-level rules and selection groups.
// Use rule.Entry() and group.Entry() to keep the business state type consistent.
// Top-level entries sort by descending priority, then registration order;
// members sort independently within each group using the same ordering.
// Group priority defaults to zero and never derives from member priorities.
// Rule IDs are unique across all rules and members; Group IDs are unique in
// their own namespace. Empty groups and an empty entry list are valid.
//
// Validation visits top-level registration order, checking a group's ID before
// its members in local registration order. Rule issue order matches Compile.
// Invalid IDs do not participate in duplicate detection. Failure returns nil
// and *ValidationError. No callbacks run, and the supplied slice is not retained.
// Zero entries are invalid zero rules; zero groups are rejected by their ID.
func CompileEntries[T any](entries ...Entry[T]) (*RuleSet[T], error) {
	snapshot, err := compileDefinitions(definitionList[T]{entries: entries})
	if err != nil {
		return nil, err
	}
	return &RuleSet[T]{snapshot: snapshot}, nil
}

// Valid reports whether the set was successfully compiled, including an empty set.
func (s *RuleSet[T]) Valid() bool { return s != nil && s.snapshot != nil }

// Len counts top-level rules and all members, excluding group containers.
// It returns zero for a nil or uninitialized set.
func (s *RuleSet[T]) Len() int {
	if !s.Valid() {
		return 0
	}
	return len(s.snapshot.metadata.rules)
}

// Rule looks up metadata by exact RuleID. Unknown IDs return a zero view and false.
func (s *RuleSet[T]) Rule(id RuleID) (RuleInfo, bool) {
	if !s.Valid() {
		return RuleInfo{}, false
	}
	order, ok := s.snapshot.metadata.byID[id]
	if !ok {
		return RuleInfo{}, false
	}
	return RuleInfo{metadata: s.snapshot.metadata.rules[order], order: order}, true
}

// Rules returns a defensive copy of metadata views in compiled execution order.
// An empty collection may be nil.
func (s *RuleSet[T]) Rules() []RuleInfo {
	if !s.Valid() {
		return nil
	}
	views := make([]RuleInfo, s.Len())
	for order, metadata := range s.snapshot.metadata.rules {
		views[order] = RuleInfo{metadata: metadata, order: order}
	}
	return views
}
