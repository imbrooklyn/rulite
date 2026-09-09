// Package dynamic compiles strict JSON definitions and explicitly registered
// typed Go actions into ordinary immutable rulite.RuleSet values. CEL decides
// conditions; only trusted registered callbacks execute actions. Decoding,
// parameter validation, capability resolution, and CEL compilation occur before
// Fire. The root package remains independent of this integration.
//
// CompileRules returns ordinary rules for code-owned composition with typed
// rules and groups before final rulite.CompileEntries. JSON remains a flat
// definition list; no execution ordering or group schema is inferred.
//
// Registries must be frozen before compilation. Compiled sets support concurrent
// engines with independently owned inputs. Actions may mutate input, but must
// treat their shared nested parameters as read-only and synchronize captures.
// Configuration replacement does not authorize arbitrary code execution or
// provide hot reload, rollback, retries, scripts, or a YAML runtime.
package dynamic
