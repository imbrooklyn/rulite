package rulite

import "context"

// Action performs a rule's work on the caller-owned state.
// T should be a non-pointer business state type. An action may mutate the
// state or perform external side effects; returning an error does not undo
// either. Callers are responsible for synchronization of shared state and
// callback captures, as well as idempotency and transactions where needed.
type Action[T any] func(context.Context, *T) error
