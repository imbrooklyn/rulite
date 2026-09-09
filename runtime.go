package rulite

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrInvalidRuntime reports a nil Runtime or Fire before its first publication.
var ErrInvalidRuntime = errors.New("rulite: invalid runtime")

// ErrRevisionExhausted reports that another publication would overflow uint64.
// The current snapshot remains available and its revision never wraps.
var ErrRevisionExhausted = errors.New("rulite: snapshot revision exhausted")

// Runtime atomically publishes complete immutable engines, including their
// execution defaults. Each Fire captures one publication for its entire call.
// A Runtime must not be copied after first use. Its zero value accepts Publish;
// Fire before that first publication returns ErrInvalidRuntime.
//
// Publishers and Fire calls may run concurrently. Callers synchronize shared
// mutable input, callback captures, and observers, just as for Engine.Fire.
// Runtime starts no background work and has no Close method. Callers acquire
// sources, finish compilation before publishing, and own external resources.
// Superseded callbacks become collectible after active executions and other
// executable owners release them; retained results keep only metadata/facts.
type Runtime[T any] struct {
	publishMu sync.Mutex
	current   atomic.Pointer[publication[T]]
}

type publication[T any] struct {
	engine   *Engine[T]
	identity SnapshotInfo
}

// NewRuntime publishes initial at revision one. Type arguments are inferred
// from initial. A nil or zero engine returns nil and ErrInvalidEngine; a valid
// empty engine is accepted. The engine and its compiled set remain immutable.
func NewRuntime[T any](initial *Engine[T]) (*Runtime[T], error) {
	r := &Runtime[T]{}
	if _, err := r.Publish(initial); err != nil {
		return nil, err
	}
	return r, nil
}

// Publish atomically replaces the entire execution snapshot and returns its
// assigned identity. It accepts only an already constructed, valid engine.
// It performs no source loading, decoding, compilation, or callback invocation.
// Nil receiver is ErrInvalidRuntime; nil or zero next is ErrInvalidEngine.
// Failure returns zero metadata and leaves the current publication unchanged.
//
// The atomic store is the linearization point. Publishers serialize revision
// assignment and that store together, so visible revisions cannot go backward.
// The first publication is one; every success increments, even when publishing
// the same engine, version, or digest again. Overflow is ErrRevisionExhausted.
// Return order of overlapping callers need not equal publication order; use
// the returned revisions. There is no stale-source detection or rollout policy.
func (r *Runtime[T]) Publish(next *Engine[T]) (SnapshotInfo, error) {
	if r == nil {
		return SnapshotInfo{}, ErrInvalidRuntime
	}
	if next == nil || next.snapshot == nil {
		return SnapshotInfo{}, ErrInvalidEngine
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	var revision SnapshotRevision
	if old := r.current.Load(); old != nil {
		revision = old.identity.revision
	}
	if revision == ^SnapshotRevision(0) {
		return SnapshotInfo{}, ErrRevisionExhausted
	}
	identity := next.identity
	identity.revision = revision + 1
	r.current.Store(&publication[T]{engine: next, identity: identity})
	return identity, nil
}

// Snapshot atomically reads the current identity without retaining executable
// code. Nil and unpublished runtimes return zero metadata. A later Fire may
// capture a newer publication; Result.Snapshot is authoritative for that call.
func (r *Runtime[T]) Snapshot() SnapshotInfo {
	if r != nil {
		if current := r.current.Load(); current != nil {
			return current.identity
		}
	}
	return SnapshotInfo{}
}

// Fire loads one complete publication and executes it using Engine.Fire's
// sequential semantics, error model, context boundaries, and option handling.
// Publications during callbacks cannot change this call's rules, defaults, or
// identity. Preflight order is nil context, nil input, invalid runtime, options;
// failures return a zero Result and emit no events. Completed executions report
// the captured identity in Result, Trace, Explanation, and every Observer event.
func (r *Runtime[T]) Fire(ctx context.Context, input *T, options ...FireOption) (Result, error) {
	var captured *publication[T]
	if r != nil {
		captured = r.current.Load()
	}
	var engine *Engine[T]
	var identity SnapshotInfo
	if captured != nil {
		engine, identity = captured.engine, captured.identity
	}
	return engine.fire(ctx, input, identity, ErrInvalidRuntime, options)
}
