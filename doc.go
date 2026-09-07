// Package rulite defines type-safe business rules and immutable engines.
//
// Build a rule with NewRule, optionally set its priority, supply a Condition
// with When, and supply an Action with Then. NewEngine validates the rules
// and freezes their order: higher priorities first, with registration order
// breaking ties.
//
// The type parameter T should be a non-pointer business state type. Conditions
// observe *T without mutating it; actions may mutate it or perform external
// side effects. Rule definitions and engines are immutable, but captured
// callback state remains the caller's responsibility.
package rulite
