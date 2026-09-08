package rulite_test

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestREADMEExampleMatchesCompiledExample(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(readme), "```go\n")
	if !ok {
		t.Fatal("README has no Go example")
	}
	snippet, _, ok := strings.Cut(rest, "```")
	if !ok {
		t.Fatal("README Go example is not closed")
	}
	snippet = strings.Replace(snippet, "package main", "package rulite_test", 1)
	snippet = strings.Replace(snippet, "func main()", "func ExampleNewRule()", 1)
	compiled, err := os.ReadFile("example_test.go")
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(source string) string {
		files := token.NewFileSet()
		tree, err := parser.ParseFile(files, "example.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		var formatted bytes.Buffer
		if err := format.Node(&formatted, files, tree); err != nil {
			t.Fatal(err)
		}
		source = formatted.String()
		var tokens scanner.Scanner
		tokens.Init(files.AddFile("tokens.go", -1, len(source)), []byte(source), nil, 0)
		var output strings.Builder
		for {
			_, kind, literal := tokens.Scan()
			if kind == token.EOF {
				break
			}
			fmt.Fprintf(&output, "%s:%q\n", kind, literal)
		}
		return output.String()
	}
	if canonical(snippet) != canonical(string(compiled)) {
		t.Fatal("README differs from the compiled, output-checked example")
	}
}
