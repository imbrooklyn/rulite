// Package cel compiles CEL boolean expressions into ordinary rulite conditions.
//
// NewCompiler binds one explicitly named native Go struct type. NewBuilder adds
// typed projectors, descriptor-based protobuf bindings and trusted unary functions.
// Build freezes registration into an immutable compiler. Compile parses,
// checks, and builds a reusable program before returning a condition. Conditions
// read the current *T on every call, so earlier Go actions remain visible.
// Compilers and conditions support concurrent use with independently owned input.
// Callers must synchronize shared mutable input around the entire Fire call.
//
// Exported Go names are the default; WithJSONFieldNames explicitly enables native
// JSON tags. Protobuf always uses descriptor field names. Unsupported schemas
// fail at construction. See the CEL guide for presence and resource limits.
// Trusted callbacks are synchronous and read-only; their providers bound work
// and synchronize captured state. No Go methods, actions or policy runtime are exposed.
package cel
