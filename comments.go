package rules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strings"
)

// MaxContinuation is how many comment lines may follow a TODO(#n) or ponytail: line.
const MaxContinuation = 2

// CommentMsg explains RYG001.
var CommentMsg = "comments are not allowed outside doc comments. Fix the root cause, or say it in a name, " +
	"a type or a test. Deferred work is `// TODO(#<issue>)`; a deliberate simplification is " +
	"`// ponytail: <ceiling>` (at most 2 continuation lines)"

// NolintBanMsg explains RYG002.
var NolintBanMsg = "a nolint for forbidigo hides a wall clock or a sleep. Inject a clock from main, or wait on the event; " +
	"an exception every service needs belongs in go-rules"

var (
	allowedStart = regexp.MustCompile(`^//\s*(TODO\(#[1-9]\d*\)|ponytail:)`)
	directive    = regexp.MustCompile(`^(//go:(build|generate|embed|noinline|nosplit|linkname|noescape|norace|nocheckptr|uintptrescapes|wasmimport|wasmexport|debug|fix) ` +
		`|//go:(noinline|nosplit|noescape|norace|nocheckptr|uintptrescapes)$` +
		`|//line \S+:\d+(:\d+)?$|/\*line \S+:\d+(:\d+)?\*/$|//export \w+$|// \+build )`)
	nolint     = regexp.MustCompile(`^//nolint:([\w-]+(,[\w-]+)*)( // \S.*)?$`)
	exampleOut = regexp.MustCompile(`^//\s*(Unordered output|Output):`)
)

// CheckSource returns the RYG001 findings in one Go file.
func CheckSource(path string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	allowed := docGroups(file, strings.HasSuffix(path, "_test.go"))
	var findings []Finding
	for _, group := range file.Comments {
		if allowed[group] {
			continue
		}
		for _, c := range disallowed(group) {
			pos := fset.Position(c.Pos())
			findings = append(findings, Finding{path, pos.Line, pos.Column, "RYG001", CommentMsg})
		}
	}
	for _, group := range file.Comments {
		for _, c := range group.List {
			if m := nolint.FindStringSubmatch(c.Text); m != nil && slices.Contains(strings.Split(m[1], ","), "forbidigo") {
				pos := fset.Position(c.Pos())
				findings = append(findings, Finding{path, pos.Line, pos.Column, "RYG002", NolintBanMsg})
			}
		}
	}
	slices.SortFunc(findings, func(a, b Finding) int { return a.Line - b.Line })
	return findings, nil
}

func docGroups(file *ast.File, testFile bool) map[*ast.CommentGroup]bool {
	docs := map[*ast.CommentGroup]bool{file.Doc: true}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			docs[d.Doc] = true
			if testFile && d.Recv == nil && strings.HasPrefix(d.Name.Name, "Example") {
				markExampleOutput(d, file, docs)
			}
		case *ast.GenDecl:
			docs[d.Doc] = true
			for _, spec := range d.Specs {
				markSpec(spec, docs)
			}
		}
	}
	delete(docs, nil)
	return docs
}

func markSpec(spec ast.Spec, docs map[*ast.CommentGroup]bool) {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		docs[s.Doc] = true
	case *ast.TypeSpec:
		docs[s.Doc] = true
		ast.Inspect(s.Type, func(n ast.Node) bool {
			if field, ok := n.(*ast.Field); ok {
				docs[field.Doc] = true
			}
			return true
		})
	case *ast.ImportSpec:
		docs[s.Doc] = true
	}
}

func markExampleOutput(fn *ast.FuncDecl, file *ast.File, docs map[*ast.CommentGroup]bool) {
	if fn.Body == nil {
		return
	}
	for _, group := range file.Comments {
		inside := group.Pos() > fn.Body.Lbrace && group.End() < fn.Body.Rbrace
		if inside && exampleOut.MatchString(group.List[0].Text) {
			docs[group] = true
		}
	}
}

func disallowed(group *ast.CommentGroup) []*ast.Comment {
	var bad []*ast.Comment
	continuation := -1
	for _, c := range group.List {
		switch {
		case directive.MatchString(c.Text), nolint.MatchString(c.Text):
			continuation = -1
		case allowedStart.MatchString(c.Text):
			continuation = 0
		case continuation >= 0 && continuation < MaxContinuation && strings.HasPrefix(c.Text, "//"):
			continuation++
		default:
			continuation = -1
			bad = append(bad, c)
		}
	}
	return bad
}
