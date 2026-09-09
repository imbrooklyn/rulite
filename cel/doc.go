// Package cel compiles CEL boolean expressions into ordinary rulite conditions.
//
// NewCompiler binds one explicitly named native Go struct type. Compile parses,
// checks, and builds a reusable program before returning a condition. Conditions
// read the current *T on every call, so earlier Go actions remain visible.
// Compilers and conditions support concurrent use with independently owned input.
// Callers must synchronize shared mutable input around the entire Fire call.
//
// Only direct exported scalar fields and scalar slices are supported. Go field
// names are used exactly; struct tags do not create aliases. Unsupported schemas
// fail at construction. See the CEL guide for the mapping and resource limits.
// No Go methods, custom functions, actions, or policy runtime are exposed.
package cel
