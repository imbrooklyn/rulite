package cel

import (
	"errors"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/interpreter"
)

// cel-go v0.32 uses non-negative causes. Use a distinct cause so its evaluation
// boundary preserves this adapter's budget error without parsing error text.
const nativeLimitExceeded interpreter.CancellationCause = -1

func nativeOperationError(err error) ref.Val {
	if errors.Is(err, ErrNativeLimit) {
		// Some CEL collection equality/containment implementations suppress
		// element errors. Abort through the same recovery boundary as CEL's own
		// cost tracker so exhaustion cannot become a match or continue a scan.
		panic(interpreter.EvalCancelledError{Message: ErrNativeLimit.Error(), Cause: nativeLimitExceeded})
	}
	return types.WrapErr(err)
}

// Native conversions and equality run inside a single CEL operation. CEL's
// cost tracker cannot interrupt their work, so each operation has its own
// finite budget, independent of the compiler and other evaluations.
type nativeBudget struct {
	bytes, items, nodes int
	storage             uintptr
}

func newNativeBudget() nativeBudget {
	return nativeBudget{bytes: maxInputBytes, items: maxListItems, nodes: 65536, storage: 1 << 20}
}

func (b *nativeBudget) visit(depth int) error {
	if depth > 32 || b.nodes <= 0 {
		return ErrNativeLimit
	}
	b.nodes--
	return nil
}

func (b *nativeBudget) takeBytes(n int) error {
	if n < 0 || n > b.bytes {
		return ErrNativeLimit
	}
	b.bytes -= n
	return nil
}

func (b *nativeBudget) takeItems(n int64) error {
	if n < 0 || n > int64(b.items) {
		return ErrNativeLimit
	}
	b.items -= int(n)
	return nil
}

// Check multiplication before allocating, including on 32-bit targets. Storage
// accounts for Go value sizes, not allocator metadata or map bucket overhead.
func (b *nativeBudget) takeStorage(size uintptr, count int) error {
	if count < 0 || count > 0 && size > b.storage/uintptr(count) {
		return ErrNativeLimit
	}
	b.storage -= size * uintptr(count)
	return nil
}
