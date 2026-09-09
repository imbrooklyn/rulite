package rulite

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"unsafe"
)

func TestRuntimeRevisionOverflow(t *testing.T) {
	engine, err := NewEngine[int]()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(engine)
	if err != nil {
		t.Fatal(err)
	}
	last := &publication[int]{engine: engine, identity: SnapshotInfo{revision: ^SnapshotRevision(0)}}
	runtime.current.Store(last)
	if info, err := runtime.Publish(engine); err != ErrRevisionExhausted || info != (SnapshotInfo{}) || runtime.current.Load() != last {
		t.Fatal("overflow changed publication")
	}
	result, err := runtime.Fire(context.Background(), new(int))
	if err != nil || result.Snapshot() != last.identity {
		t.Fatal("exhausted runtime unavailable")
	}
}

func TestRuntimeSharesExecutablesAndCopiesIdentityStrings(t *testing.T) {
	set, err := Compile[int]()
	if err != nil {
		t.Fatal(err)
	}
	buffer := strings.Repeat("x", 1<<20)
	version, digest := buffer[1:10], buffer[20:30]
	named, err := set.WithIdentity(RuleSetVersion(version), SourceDigest(digest))
	if err != nil {
		t.Fatal(err)
	}
	if named.snapshot != set.snapshot || unsafe.StringData(string(named.identity.version)) == unsafe.StringData(version) || unsafe.StringData(string(named.identity.digest)) == unsafe.StringData(digest) {
		t.Fatal("identity retained source buffer or copied executables")
	}
	engine, err := NewEngineFromRuleSet(named)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(engine)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.current.Load().engine != engine || engine.snapshot != set.snapshot {
		t.Fatal("publication copied executable nodes")
	}
}

func TestRuntimeFireLoadsOnce(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Fire" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Load" {
					loads++
				}
			}
			return true
		})
	}
	if loads != 1 {
		t.Fatalf("Runtime.Fire contains %d loads", loads)
	}
}
