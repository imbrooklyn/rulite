package dynamic

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
)

// Definition is a mutable configuration DTO, not an executable rule. JSON uses
// exact lowercase field names. ID, When, Action, and object Params are required;
// omitted priority and metadata use their zero values. Null is not accepted.
type Definition struct {
	// ID is the stable root RuleID, unique within the definition list.
	ID rulite.RuleID `json:"id"`
	// Priority uses the complete signed int32 range; higher values execute first.
	Priority rulite.Priority `json:"priority,omitempty"`
	// Description is optional descriptive metadata, normalized by the root builder.
	Description string `json:"description,omitempty"`
	// Tags are optional descriptive labels, normalized and copied by the root builder.
	Tags []string `json:"tags,omitempty"`
	// When is the CEL boolean expression compiled before execution.
	When string `json:"when"`
	// Action names one explicitly registered versioned capability.
	Action string `json:"action"`
	// Params contains a required JSON object decoded into the registered parameter struct.
	Params json.RawMessage `json:"params"`
}

// Decode reads one strict JSON array of definitions. It rejects unknown fields,
// duplicate object keys at every depth, invalid Unicode, nulls, trailing data,
// invalid field types and IDs, and the documented configuration limits. No CEL
// compilation or callback runs. Returned DTO storage is independent of source.
// Failure returns nil and *CompileError; an empty array is valid.
func Decode(source []byte) ([]Definition, error) {
	if err := checkJSON(source, '[', maxDocumentBytes, 18, 131072); err != nil {
		return nil, compileError("decode", -1, errors.Join(ErrInvalidDefinition, err))
	}
	var definitions []Definition
	if err := jsonv2.Unmarshal(source, &definitions, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, compileError("decode", -1, errors.Join(ErrInvalidDefinition, err))
	}
	if err := validateDefinitions(definitions); err != nil {
		return nil, err
	}
	return definitions, nil
}

// CompileJSON decodes strict JSON and compiles a complete ordinary RuleSet.
// Conditions come from an immutable CEL compiler; actions must be frozen.
// Registry preflight precedes decoding. All decoding, parameter validation and
// capability resolution happen here, never in Fire. Failure returns a nil set.
func CompileJSON[T any](source []byte, conditions *cel.Compiler[T], actions *Registry[T]) (*rulite.RuleSet[T], error) {
	capabilities, err := actions.snapshot()
	if err != nil {
		return nil, err
	}
	definitions, err := Decode(source)
	if err != nil {
		return nil, err
	}
	rules, err := compileValidated(definitions, conditions, capabilities)
	if err != nil {
		return nil, err
	}
	return rulite.Compile(rules...)
}

// Compile snapshots mutable DTO storage and compiles an ordinary RuleSet.
// It applies the same field, JSON parameter, ID and size checks as Decode.
// After validation, all conditions compile in registration order, followed by
// action resolution and parameter validation in that order. The first failure
// returns nil; root ID validation retains its aggregate ValidationError issues.
// Callers must not mutate definitions while this call copies them. Later edits
// to definitions, tags or raw parameters cannot change the compiled set.
func Compile[T any](definitions []Definition, conditions *cel.Compiler[T], actions *Registry[T]) (*rulite.RuleSet[T], error) {
	rules, err := CompileRules(definitions, conditions, actions)
	if err != nil {
		return nil, err
	}
	return rulite.Compile(rules...)
}

// CompileRules validates and snapshots definitions, compiles all CEL conditions,
// and resolves frozen typed actions using the same construction contract as
// Compile. It returns ordinary immutable rules in original registration order,
// before execution sorting or group assignment. Failure returns nil, never a
// partial list; an empty list still requires a valid compiler and frozen registry.
//
// Callers own the returned slice and may compose its rules with typed rules and
// selection groups using rulite.Compile or rulite.CompileEntries. That final
// construction validates identities across all combined rules and groups and
// freezes execution order. This does not extract callbacks from a compiled set
// or add group fields to the JSON schema. Use Decode first for strict transport.
// Later DTO or returned slice edits do not change already constructed groups or
// sets. Callback captures and shared nested action parameters remain read-only
// or caller-synchronized as documented by Registry.
func CompileRules[T any](definitions []Definition, conditions *cel.Compiler[T], actions *Registry[T]) ([]rulite.Rule[T], error) {
	capabilities, err := actions.snapshot()
	if err != nil {
		return nil, err
	}
	if err := validateDefinitions(definitions); err != nil {
		return nil, err
	}
	owned := slices.Clone(definitions)
	for i := range owned {
		owned[i].Tags = slices.Clone(owned[i].Tags)
		owned[i].Params = slices.Clone(owned[i].Params)
	}
	return compileValidated(owned, conditions, capabilities)
}

func validateDefinitions(definitions []Definition) error {
	if len(definitions) > maxDefinitions {
		return compileError("validate", -1, ErrLimit)
	}
	metadata := make([]rulite.Rule[struct{}], len(definitions))
	bytes := 0
	for i, d := range definitions {
		fail := func(err error) error { return compileError("validate", i, errors.Join(ErrInvalidDefinition, err)) }
		if len(d.Description) > 4096 || len(d.Tags) > 32 {
			return fail(ErrLimit)
		}
		if len(d.Action) > 128 || !actionName.MatchString(d.Action) {
			return fail(ErrInvalidAction)
		}
		// Count before allocation; each term is bounded by the remaining total.
		for _, text := range []string{string(d.ID), d.Description, d.When, d.Action} {
			if len(text) > maxDocumentBytes-bytes {
				return fail(ErrLimit)
			}
			bytes += len(text)
		}
		if !utf8.ValidString(d.Description) || !utf8.ValidString(d.When) || strings.TrimSpace(d.When) == "" {
			return fail(errors.New("invalid description or condition text"))
		}
		for _, tag := range d.Tags {
			if len(tag) > 128 || len(tag) > maxDocumentBytes-bytes {
				return fail(ErrLimit)
			}
			if !utf8.ValidString(tag) {
				return fail(errors.New("invalid tag text"))
			}
			bytes += len(tag)
		}
		if len(d.Params) > maxDocumentBytes-bytes {
			return fail(ErrLimit)
		}
		bytes += len(d.Params)
		if err := checkJSON(d.Params, '{', maxParamsBytes, 16, 4096); err != nil {
			return fail(errors.Join(ErrInvalidParams, err))
		}
		// Reuse canonical root ID and duplicate validation. These metadata-only
		// construction rules are never executed or returned to consumers.
		metadata[i] = rulite.NewRule[struct{}](d.ID).Priority(d.Priority).
			When(rulite.All[struct{}]()).Then(func(context.Context, *struct{}) error { return nil })
	}
	if _, err := rulite.Compile(metadata...); err != nil {
		return compileError("validate", -1, err)
	}
	return nil
}

func compileValidated[T any](definitions []Definition, compiler *cel.Compiler[T], capabilities map[string]actionFactory[T]) ([]rulite.Rule[T], error) {
	checks := make([]rulite.Condition[T], len(definitions))
	if len(definitions) == 0 {
		// Validate even an empty construction through the compiler's public contract.
		if _, err := compiler.Compile("true"); err != nil {
			return nil, compileError("condition", -1, err)
		}
	}
	for i, d := range definitions {
		check, err := compiler.Compile(d.When)
		if err != nil {
			return nil, compileError("condition", i, err)
		}
		checks[i] = check
	}
	rules := make([]rulite.Rule[T], len(definitions))
	for i, d := range definitions {
		factory, found := capabilities[d.Action]
		if !found {
			return nil, compileError("action", i, ErrUnknownAction)
		}
		action, err := factory(d.Params)
		if err != nil {
			return nil, compileError("action", i, err)
		}
		rules[i] = rulite.NewRule[T](d.ID).Priority(d.Priority).Description(d.Description).Tags(d.Tags...).When(checks[i]).Then(action)
	}
	return rules, nil
}
