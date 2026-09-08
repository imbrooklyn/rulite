// Package rulite defines type-safe business rules and immutable engines.
//
// Build a rule with NewRule, optionally set its priority, supply a Condition
// with When, and supply an Action with Then. NewEngine validates the rules
// and freezes their order: higher priorities first, with registration order
// breaking ties.
//
// Engine.Fire visits each condition and, when matched, its action sequentially.
// Earlier action mutations are visible to later conditions. The default policy
// evaluates all rules but stops on the first error. StopOnFirstMatch ends after
// a matched action attempt; StopOnFirstFire supports fallback past failed actions
// when action errors use ContinueOnError. Errors and recovered panics preserve a
// partial Result and never undo side effects.
//
// Result, its always-available Explanation, and opt-in Trace share one ledger.
// Fired identifies successful action returns, not field writes. Applications
// needing field attribution should record provenance, such as AppliedBy RuleID,
// in their own state. Trace adds timing and built-in combinator child outcomes.
// Context cancellation is cooperative at callback boundaries; Fire never leaves
// a callback running in the background after returning.
//
// The type parameter T should be a non-pointer business state type. Conditions
// observe *T without mutating it; actions may mutate it or perform external
// side effects. Rule definitions and engines are immutable, but captured
// callback state remains the caller's responsibility.
package rulite
