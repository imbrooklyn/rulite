package dynamic

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"reflect"
	"regexp"
	"sync"

	"github.com/imbrooklyn/rulite"
)

type actionFactory[T any] func([]byte) (rulite.Action[T], error)

type registryState[T any] struct {
	mu      sync.RWMutex
	frozen  bool
	actions map[string]actionFactory[T]
}

// Registry holds explicit versioned Go capabilities. Register during construction,
// then Freeze before compiling definitions. All methods are synchronized. Copies
// share the same registration and freeze state. Nil and zero registries are invalid.
// Frozen registries support concurrent compilation; Fire never consults a registry.
type Registry[T any] struct{ state *registryState[T] }

// NewRegistry creates an empty mutable registry for business input T.
func NewRegistry[T any]() *Registry[T] {
	return &Registry[T]{state: &registryState[T]{actions: make(map[string]actionFactory[T])}}
}

var actionName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*/v[1-9][0-9]*$`)

// Register adds one typed capability, with an optional read-only validator.
// P must be a non-pointer struct with the data-only schema in the dynamic guide.
// Names contain at most 128 bytes, dot-separated lowercase identifier components,
// and a positive decimal version suffix, for example pricing.apply_discount/v1.
// At most 256 actions are allowed. Duplicate names, nil callbacks, explicit nil
// validators, multiple validators, and unsupported schemas leave the registry unchanged.
//
// Neither callback runs during registration. Compilation strictly decodes params,
// invokes the validator once, and decodes independent execution parameters. A
// validator cannot normalize those final values and must not mutate its argument.
// Validators are trusted bounded code; their errors retain their cause and their
// panics propagate from compilation. Actions receive P by value; nested slices,
// maps, and pointers are shared read-only across executions. Providers synchronize
// callback captures and must copy nested params before placing them in mutable input.
func (r *Registry[T]) Register[P any](name string, action func(context.Context, *T, P) error, validators ...func(P) error) error {
	if r == nil || r.state == nil {
		return ErrInvalidRegistry
	}
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frozen {
		return ErrRegistryFrozen
	}
	if len(name) > 128 || !actionName.MatchString(name) || action == nil || len(validators) > 1 || len(validators) == 1 && validators[0] == nil {
		return ErrInvalidAction
	}
	if _, exists := s.actions[name]; exists {
		return ErrDuplicateAction
	}
	if len(s.actions) >= 256 {
		return ErrLimit
	}
	tp := reflect.TypeFor[P]()
	if tp.Kind() != reflect.Struct {
		return ErrInvalidAction
	}
	if err := validateParamType(tp, 0, make(map[reflect.Type]bool), make(map[reflect.Type]bool)); err != nil {
		return errors.Join(ErrInvalidAction, err)
	}
	var probe P
	if err := json.Unmarshal([]byte("{}"), &probe, json.RejectUnknownMembers(true)); err != nil {
		return errors.Join(ErrInvalidAction, err)
	}
	var validator func(P) error
	if len(validators) == 1 {
		validator = validators[0]
	}
	s.actions[name] = func(raw []byte) (rulite.Action[T], error) {
		var params P
		if err := json.Unmarshal(raw, &params, json.RejectUnknownMembers(true)); err != nil {
			return nil, errors.Join(ErrInvalidParams, err)
		}
		if validator != nil {
			if err := validator(params); err != nil {
				return nil, errors.Join(ErrInvalidParams, err)
			}
			// The validator may retain references. Its private decode never becomes
			// the shared execution value; no JSON or validation runs during Fire.
			var execution P
			if err := json.Unmarshal(raw, &execution, json.RejectUnknownMembers(true)); err != nil {
				return nil, errors.Join(ErrInvalidParams, err)
			}
			params = execution
		}
		return func(ctx context.Context, input *T) error { return action(ctx, input, params) }, nil
	}
	return nil
}

// Freeze permanently closes registration. It is idempotent, including on an
// empty registry. Compilations can subsequently read its immutable capabilities.
func (r *Registry[T]) Freeze() error {
	if r == nil || r.state == nil {
		return ErrInvalidRegistry
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.frozen = true
	return nil
}

func (r *Registry[T]) snapshot() (map[string]actionFactory[T], error) {
	if r == nil || r.state == nil {
		return nil, ErrInvalidRegistry
	}
	r.state.mu.RLock()
	defer r.state.mu.RUnlock()
	if !r.state.frozen {
		return nil, ErrRegistryNotFrozen
	}
	return r.state.actions, nil
}
