package rulite

import "strings"

// RuleSetVersion is a caller-supplied business version, compared byte for byte.
// Empty means unspecified. Rulite neither generates nor orders business versions.
type RuleSetVersion string

// SnapshotRevision is a monotonically increasing publication number within one
// Runtime. Zero means no publication, including direct Engine execution.
// It is neither a cross-runtime nor a distributed sequence number.
type SnapshotRevision uint64

// SourceDigest is an optional caller-supplied source digest. Its format and
// algorithm belong to the caller; Rulite does not verify it or hash Go closures.
// Empty means unspecified. A digest does not replace a business version.
type SourceDigest string

// SnapshotInfo is immutable execution identity with no executable references.
// Its zero value has unspecified version/digest and revision zero. All fields
// are returned by value and may be retained independently of a Runtime.
type SnapshotInfo struct {
	version  RuleSetVersion
	revision SnapshotRevision
	digest   SourceDigest
}

// Version returns the caller's business version without normalization.
func (s SnapshotInfo) Version() RuleSetVersion { return s.version }

// Revision returns the Runtime publication number, or zero for a direct Engine.
func (s SnapshotInfo) Revision() SnapshotRevision { return s.revision }

// SourceDigest returns the optional caller-supplied digest without normalization.
func (s SnapshotInfo) SourceDigest() SourceDigest { return s.digest }

// WithIdentity returns a new set sharing s's compiled rules with independent
// version and digest metadata. It never changes s, recompiles rules, or invokes
// callbacks. Both strings are opaque, may be empty, and are copied so substrings
// do not retain larger source buffers. No syntax, authenticity, or uniqueness
// is implied. A nil or zero set returns nil and ErrInvalidRuleSet.
func (s *RuleSet[T]) WithIdentity(version RuleSetVersion, digest SourceDigest) (*RuleSet[T], error) {
	if !s.Valid() {
		return nil, ErrInvalidRuleSet
	}
	return &RuleSet[T]{snapshot: s.snapshot, identity: SnapshotInfo{
		version: RuleSetVersion(strings.Clone(string(version))),
		digest:  SourceDigest(strings.Clone(string(digest))),
	}}, nil
}

// Snapshot returns the set's business identity with revision zero.
// Nil and zero sets return zero metadata.
func (s *RuleSet[T]) Snapshot() SnapshotInfo {
	if s == nil {
		return SnapshotInfo{}
	}
	return s.identity
}

// Snapshot returns the engine's business identity with revision zero.
// Nil and zero engines return zero metadata.
func (e *Engine[T]) Snapshot() SnapshotInfo {
	if e == nil {
		return SnapshotInfo{}
	}
	return e.identity
}

// Snapshot returns the identity captured before execution started.
// Preflight failures and the zero Result have zero metadata.
func (r Result) Snapshot() SnapshotInfo { return r.identity }

// Snapshot returns the identity from the same execution ledger as the trace.
func (t Trace) Snapshot() SnapshotInfo { return t.result.Snapshot() }

// Snapshot returns the identity from the same execution ledger as the explanation.
func (e Explanation) Snapshot() SnapshotInfo { return e.result.Snapshot() }

// Snapshot returns the captured identity for every delivered event, including
// execution start/finish, rule, and group events. A zero Event has zero metadata.
func (e Event) Snapshot() SnapshotInfo { return e.identity }
