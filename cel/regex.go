package cel

import (
	"regexp/syntax"

	celgo "cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
)

const maxRegexSize = 512

// Bound expansion before Simplify allocates repeated subexpressions. Counting
// instructions after simplification also handles counted repeats correctly.
type regexBudget struct{}

// Name identifies the validator within the CEL environment.
func (regexBudget) Name() string { return "rulite.regex_budget" }

// Validate bounds literal patterns before program construction expands them.
func (regexBudget) Validate(_ *celgo.Env, _ celgo.ValidatorConfig, tree *ast.AST, issues *celgo.Issues) {
	for _, call := range ast.MatchDescendants(ast.NavigateAST(tree), ast.FunctionMatcher("matches")) {
		args := call.AsCall().Args()
		pattern := args[len(args)-1]
		if pattern.Kind() != ast.LiteralKind {
			issues.ReportErrorAtID(call.ID(), "regex pattern must be a string literal")
			continue
		}
		literal, ok := pattern.AsLiteral().(types.String)
		if !ok || len(literal) > 128 {
			issues.ReportErrorAtID(call.ID(), "regex pattern exceeds 128 bytes")
			continue
		}
		parsed, err := syntax.Parse(string(literal), syntax.Perl)
		if err != nil {
			// Program construction compiles constant patterns and reports syntax
			// errors through its original diagnostic.
			continue
		}
		if regexSize(parsed) > maxRegexSize {
			issues.ReportErrorAtID(call.ID(), "regex expansion exceeds size limit")
			continue
		}
		compiled, err := syntax.Compile(parsed.Simplify())
		if err != nil || len(compiled.Inst) > maxRegexSize {
			issues.ReportErrorAtID(call.ID(), "regex program exceeds size limit")
		}
	}
}

func regexSize(node *syntax.Regexp) int {
	size := 1 + len(node.Rune)
	for _, child := range node.Sub {
		size += regexSize(child)
		if size > maxRegexSize {
			return maxRegexSize + 1
		}
	}
	if node.Op == syntax.OpRepeat {
		count := node.Max
		if count < 0 {
			count = node.Min + 1
		}
		if count > 0 && size > maxRegexSize/count {
			return maxRegexSize + 1
		}
		size *= count
	}
	return size
}
