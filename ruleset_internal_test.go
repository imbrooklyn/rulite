package rulite

import (
	"context"
	"reflect"
	"testing"
)

func TestRuleSetSharesExecutableSnapshot(t *testing.T) {
	rule := NewRule[int]("shared").Name("Shared").Tags("snapshot").When(func(context.Context, *int) (bool, error) {
		t.Fatal("construction called condition")
		return false, nil
	}).Then(func(context.Context, *int) error { t.Fatal("construction called action"); return nil })
	set, err := Compile(rule)
	if err != nil {
		t.Fatal(err)
	}
	copyOfSet := *set
	for _, source := range []*RuleSet[int]{set, &copyOfSet} {
		engine, err := NewEngineFromRuleSet(source, WithTrace())
		if err != nil {
			t.Fatal(err)
		}
		if engine.snapshot != set.snapshot || &engine.snapshot.callbacks[0] != &set.snapshot.callbacks[0] || engine.snapshot.metadata != set.snapshot.metadata {
			t.Fatal("reusable constructor copied executable or metadata storage")
		}
	}
	if failed, err := Compile(rule, rule); failed != nil || err == nil {
		t.Fatal("duplicate accepted")
	}
	composed := NewRule[int]("composed").When(All[int](nil)).Then(rule.action)
	if _, err := Compile(composed); err != nil {
		t.Fatal("Compile inspected callback behavior")
	}
}

func TestMetadataDoesNotRetainExecutables(t *testing.T) {
	// Traverse owned field types, treating caller-owned errors as opaque values.
	// This checks the retention boundary without relying on garbage collection timing.
	errorType := reflect.TypeFor[error]()
	seen := make(map[reflect.Type]bool)
	var inspect func(reflect.Type)
	inspect = func(typ reflect.Type) {
		if seen[typ] || typ == errorType {
			return
		}
		seen[typ] = true
		switch typ.Kind() {
		case reflect.Func, reflect.Interface, reflect.UnsafePointer:
			t.Fatalf("owned metadata or ledger can retain arbitrary executable state: %v", typ)
		case reflect.Pointer, reflect.Slice, reflect.Array:
			inspect(typ.Elem())
		case reflect.Map:
			inspect(typ.Key())
			inspect(typ.Elem())
		case reflect.Struct:
			for index := range typ.NumField() {
				inspect(typ.Field(index).Type)
			}
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[snapshotMetadata](), reflect.TypeFor[RuleInfo](), reflect.TypeFor[GroupResult](), reflect.TypeFor[Result](), reflect.TypeFor[Explanation](), reflect.TypeFor[EntryExplanation](), reflect.TypeFor[Trace](), reflect.TypeFor[Event](), reflect.TypeFor[Diagnostic]()} {
		inspect(typ)
	}
}
