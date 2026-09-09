// Package rulite defines type-safe business rules, immutable rule sets and engines.
//
// Build a rule with NewRule, optionally set its priority, supply a Condition
// with When, and supply an Action with Then. NewEngine validates the rules
// and freezes their order: higher priorities first, with registration order
// breaking ties.
// Compile exposes the same validated snapshot as a reusable RuleSet.
// NewEngineFromRuleSet shares that snapshot with independent engine defaults.
// CompileEntries accepts Rule.Entry and Group.Entry values for mixed definitions.
// FirstMatchGroup and FirstFireGroup select locally, then continue to the next
// top-level entry unless global policy, context, or panic terminates execution.
// Names, descriptions, and tags are descriptive metadata; RuleID alone is identity.
//
// Engine.Fire visits each condition and, when matched, its action sequentially.
// Earlier action mutations are visible to later conditions. The default policy
// visits all entries with local group selection, stopping on the first error. StopOnFirstMatch ends after
// a matched action attempt; StopOnFirstFire supports fallback past failed actions
// when action errors use ContinueOnError. Errors and recovered panics preserve a
// partial Result and never undo side effects.
//
// Result, its always-available Explanation, and opt-in Trace share one ledger.
// Explanation.Entries exposes top-level rules and groups with ordered member
// outcomes. GroupResult separates selection from the local end and global stop.
// Fired identifies successful action returns, not field writes. Applications
// needing field attribution should record provenance, such as AppliedBy RuleID,
// in their own state. Trace adds timing and built-in combinator child outcomes.
// Context cancellation is cooperative at callback boundaries; Fire never leaves
// a callback running in the background after returning.
// WithObserver adds synchronous ordered rule, group, and execution events.
// Group events capture selection and completion without retaining live state.
// Observer errors and recovered panics become independent Result diagnostics,
// without changing business policy or outcomes. Observers must manage their own
// latency and shared-state safety.
//
// The type parameter T should be a non-pointer business state type. Conditions
// observe *T without mutating it; actions may mutate it or perform external
// side effects. Rule definitions and engines are immutable, but captured
// callback and observer state remains the caller's responsibility.
package rulite
