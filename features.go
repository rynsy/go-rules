package rules

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Feature is one entry of features.json: the operations it owns and the tests that prove it.
type Feature struct {
	Name       string   `json:"name"`
	Operations []string `json:"operations"`
	ProvenBy   []string `json:"provenBy"`
}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Operations reads an OpenAPI document: operationId to "METHOD /path", and the operations with no id.
func Operations(openapi string) (map[string]string, []string, error) {
	raw, err := os.ReadFile(openapi) //nolint:gosec // G304: the path is a CLI argument from the repo's Makefile
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", openapi, err)
	}
	named := map[string]string{}
	var unnamed []string
	for route, ops := range doc.Paths {
		for method, body := range ops {
			if !slices.Contains(httpMethods, method) {
				continue
			}
			var op struct {
				OperationID string `json:"operationId"`
			}
			if err := json.Unmarshal(body, &op); err != nil {
				return nil, nil, fmt.Errorf("%s %s: %w", method, route, err)
			}
			label := strings.ToUpper(method) + " " + route
			if op.OperationID == "" {
				unnamed = append(unnamed, label)
			} else {
				named[op.OperationID] = label
			}
		}
	}
	slices.Sort(unnamed)
	return named, unnamed, nil
}

// FeatureProblems lists everything wrong with the feature map; empty means it holds.
func FeatureProblems(root, openapi, featuresPath string) ([]string, error) {
	ops, unnamed, err := Operations(openapi)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(featuresPath) //nolint:gosec // G304: the path is a CLI argument from the repo's Makefile
	if err != nil {
		return nil, err
	}
	var features []Feature
	if err := json.Unmarshal(raw, &features); err != nil {
		return nil, fmt.Errorf("%s: %w", featuresPath, err)
	}
	var problems []string
	for _, op := range unnamed {
		problems = append(problems, fmt.Sprintf("operation %s has no operationId, so no feature can claim it", op))
	}
	covered := map[string]bool{}
	for _, f := range features {
		for _, op := range f.Operations {
			covered[op] = true
		}
	}
	var ids []string
	for id := range ops {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if !covered[id] {
			problems = append(problems, fmt.Sprintf("operation %s (%s) belongs to no feature in %s", id, ops[id], featuresPath))
		}
	}
	for _, f := range features {
		problems = append(problems, featureProblems(root, openapi, f, ops)...)
	}
	return problems, nil
}

func featureProblems(root, openapi string, f Feature, ops map[string]string) []string {
	var problems []string
	for _, op := range f.Operations {
		if _, ok := ops[op]; !ok {
			problems = append(problems, fmt.Sprintf("feature '%s': operation %s is not in %s", f.Name, op, openapi))
		}
	}
	if len(f.ProvenBy) == 0 {
		problems = append(problems, fmt.Sprintf("feature '%s': no test proves it", f.Name))
	}
	for _, proof := range f.ProvenBy {
		if problem := ProofProblem(root, proof); problem != "" {
			problems = append(problems, fmt.Sprintf("feature '%s': %s", f.Name, problem))
		}
	}
	return problems
}

// ProofProblem checks one proof, written "<package dir>:<TestName>" (a subtest may follow a "/").
// It returns "" when the test exists, always runs, and can fail.
func ProofProblem(root, proof string) string {
	dir, name, ok := strings.Cut(proof, ":")
	name, _, _ = strings.Cut(name, "/")
	if !ok || !strings.HasPrefix(name, "Test") || filepath.IsAbs(dir) || strings.Contains(dir, "..") {
		return proof + ": not a proof of the form <package dir>:<TestName> (internal/api:TestHealth)"
	}
	pkg, err := loadTests(filepath.Join(root, dir))
	if err != nil {
		return fmt.Sprintf("%s: %v", proof, err)
	}
	fn, ok := pkg.funcs[name]
	if !ok || !isTest(fn) {
		return proof + ": no such test function in a _test.go file of " + dir
	}
	if constrained := pkg.constrained[fn]; constrained != "" {
		return fmt.Sprintf("%s: the test file has a build constraint (%s), so `go test` skips it", proof, constrained)
	}
	if skips(fn) {
		return proof + ": the test can skip itself, so it proves nothing"
	}
	if !pkg.asserts(fn, map[*ast.FuncDecl]bool{}) {
		return proof + ": the test cannot fail (no t.Error, t.Fatal or t.Fail, directly or through a helper), so it proves nothing"
	}
	return ""
}

type testPackage struct {
	funcs       map[string]*ast.FuncDecl
	constrained map[*ast.FuncDecl]string
}

func loadTests(dir string) (testPackage, error) {
	pkg := testPackage{funcs: map[string]*ast.FuncDecl{}, constrained: map[*ast.FuncDecl]string{}}
	paths, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil || len(paths) == 0 {
		return pkg, fmt.Errorf("no _test.go files in %s", dir)
	}
	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return pkg, err
		}
		constraint := buildConstraint(file)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				pkg.funcs[fn.Name.Name] = fn
				pkg.constrained[fn] = constraint
			}
		}
	}
	return pkg, nil
}

func buildConstraint(file *ast.File) string {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//go:build ") {
				return strings.TrimPrefix(c.Text, "//go:build ")
			}
		}
	}
	return ""
}

func isTest(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

func calledName(call *ast.CallExpr) (receiver, name string) {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return "", f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name, f.Sel.Name
		}
		return "?", f.Sel.Name
	}
	return "", ""
}

func skips(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			receiver, name := calledName(call)
			found = found || (receiver != "" && strings.HasPrefix(name, "Skip"))
		}
		return !found
	})
	return found
}

var failing = []string{"Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow"}

func (pkg testPackage) asserts(fn *ast.FuncDecl, seen map[*ast.FuncDecl]bool) bool {
	if fn.Body == nil || seen[fn] {
		return false
	}
	seen[fn] = true
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		receiver, name := calledName(call)
		switch {
		case receiver == "assert" || receiver == "require":
			found = true
		case receiver != "" && slices.Contains(failing, name):
			found = true
		case receiver == "":
			if helper, ok := pkg.funcs[name]; ok {
				found = pkg.asserts(helper, seen)
			}
		}
		return !found
	})
	return found
}
