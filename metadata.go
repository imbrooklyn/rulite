package rulite

import (
	"slices"
	"strings"
)

// ruleDetails is frozen by the builder and never refers to executable code.
type ruleDetails struct {
	name        string
	description string
	tags        []string
}

func (d *ruleDetails) value() ruleDetails {
	if d == nil {
		return ruleDetails{}
	}
	return *d
}

// Name returns a builder with a display name trimmed using strings.TrimSpace.
// Empty names are valid. Names never supply or change the rule's identity.
// Repeated calls replace the name, leaving b and completed rules unchanged.
func (b RuleBuilder[T]) Name(name string) RuleBuilder[T] {
	d := b.details.value()
	d.name = strings.TrimSpace(name)
	b.details = &d
	return b
}

// Description returns a builder with a description trimmed using strings.TrimSpace.
// Empty descriptions are valid. Repeated calls replace the description.
func (b RuleBuilder[T]) Description(description string) RuleBuilder[T] {
	d := b.details.value()
	d.description = strings.TrimSpace(description)
	b.details = &d
	return b
}

// Tags returns a builder with an independently owned, normalized tag collection.
// Each tag is trimmed using strings.TrimSpace; empty tags are dropped and exact,
// case-sensitive duplicates keep only their first occurrence. Order and internal
// whitespace are otherwise preserved. No case conversion or Unicode normalization
// is performed. Repeated calls replace all tags; Tags() clears them.
// The supplied slice can be changed after this method returns.
func (b RuleBuilder[T]) Tags(tags ...string) RuleBuilder[T] {
	d := b.details.value()
	d.tags = nil
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if _, exists := seen[tag]; tag == "" || exists {
			continue
		}
		seen[tag] = struct{}{}
		d.tags = append(d.tags, tag)
	}
	b.details = &d
	return b
}

// Name returns the normalized display name, or empty when unspecified.
func (r Rule[T]) Name() string { return r.details.value().name }

// Description returns the normalized description, or empty when unspecified.
func (r Rule[T]) Description() string { return r.details.value().description }

// Tags returns a defensive copy of the normalized tags in first-occurrence order.
// An empty collection may be nil.
func (r Rule[T]) Tags() []string { return slices.Clone(r.details.value().tags) }

// RuleInfo is an immutable, callback-free view of a compiled rule's metadata.
// Obtain it from RuleSet.Rule or RuleSet.Rules. Its zero value has empty metadata.
type RuleInfo struct {
	metadata ruleMetadata
	order    int
}

// ID returns the stable rule identity, independent of descriptive metadata.
func (i RuleInfo) ID() RuleID { return i.metadata.id }

// Priority returns the compiled priority.
func (i RuleInfo) Priority() Priority { return i.metadata.priority }

// Order returns the zero-based compiled execution position.
func (i RuleInfo) Order() int { return i.order }

// RegistrationIndex returns the original zero-based Compile or NewEngine argument position.
func (i RuleInfo) RegistrationIndex() int { return i.metadata.registrationIndex }

// Name returns the normalized display name, or empty when unspecified.
func (i RuleInfo) Name() string { return i.metadata.details.value().name }

// Description returns the normalized description, or empty when unspecified.
func (i RuleInfo) Description() string { return i.metadata.details.value().description }

// Tags returns a defensive copy of the normalized tags in first-occurrence order.
func (i RuleInfo) Tags() []string { return slices.Clone(i.metadata.details.value().tags) }
