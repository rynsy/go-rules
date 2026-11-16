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

// Operations reads an OpenAPI document: operationId to "METHOD /path", and the problems in it
// (an operation with no id, or an id used twice).
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
	var problems []string
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
			switch other, taken := named[op.OperationID]; {
			case op.OperationID == "":
				problems = append(problems, fmt.Sprintf("operation %s has no operationId, so no feature can claim it", label))
			case taken:
				first, second := min(other, label), max(other, label)
				problems = append(problems, fmt.Sprintf("operationId %s is used by both %s and %s", op.OperationID, first, second))
			default:
				named[op.OperationID] = label
			}
		}
	}
	slices.Sort(problems)
	return named, problems, nil
}

// FeatureProblems lists everything wrong with the feature map; empty means it holds.
func FeatureProblems(root, openapi, featuresPath string) ([]string, error) {
	ops, problems, err := Operations(openapi)
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
	var runnable []string
	for _, f := range features {
		fp, ok := featureProblems(root, openapi, f, ops)
		problems = append(problems, fp...)
		runnable = append(runnable, ok...)
	}
	failures, err := runFailures(root, features, runnable)
	return append(problems, failures...), err
}

func runFailures(root string, features []Feature, runnable []string) ([]string, error) {
	failed, err := RunProofs(root, runnable)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, f := range features {
		for _, proof := range f.ProvenBy {
			if why, bad := failed[proof]; bad {
				problems = append(problems, fmt.Sprintf("feature '%s': %s: %s", f.Name, proof, why))
			}
		}
	}
	return problems, nil
}

func featureProblems(root, openapi string, f Feature, ops map[string]string) (problems, runnable []string) {
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
		} else {
			runnable = append(runnable, proof)
		}
	}
	return problems, runnable
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
	if pkg.reaches(fn, isSkip, map[*ast.FuncDecl]bool{}) {
		return proof + ": the test can skip itself (directly or through a helper), so it proves nothing"
	}
	if !pkg.reaches(fn, isFailure, map[*ast.FuncDecl]bool{}) {
		return proof + ": the test cannot fail (no t.Error, t.Fatal or t.Fail on a *testing.T, and no testify call, directly or through a helper), so it proves nothing"
	}
	return ""
}

type testPackage struct {
	funcs       map[string]*ast.FuncDecl
	constrained map[*ast.FuncDecl]string
	testify     map[*ast.FuncDecl]map[string]bool
}

func loadTests(dir string) (testPackage, error) {
	pkg := testPackage{
		funcs:       map[string]*ast.FuncDecl{},
		constrained: map[*ast.FuncDecl]string{},
		testify:     map[*ast.FuncDecl]map[string]bool{},
	}
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
		testify := testifyNames(file)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				pkg.funcs[fn.Name.Name] = fn
				pkg.constrained[fn] = constraint
				pkg.testify[fn] = testify
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

// testifyNames is the local names of the testify assert and require packages a file imports.
func testifyNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(path, "github.com/stretchr/testify/") {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = true
	}
	return names
}

// testingVars is the names declared as *testing.T, *testing.B, *testing.F or testing.TB parameters in fn,
// including the parameters of function literals inside it (t.Run subtests).
func testingVars(fn *ast.FuncDecl) map[string]bool {
	vars := map[string]bool{}
	collect := func(fields *ast.FieldList) {
		for _, field := range fields.List {
			typ := field.Type
			if star, ok := typ.(*ast.StarExpr); ok {
				typ = star.X
			}
			sel, ok := typ.(*ast.SelectorExpr)
			if !ok || !slices.Contains([]string{"T", "B", "F", "TB"}, sel.Sel.Name) {
				continue
			}
			for _, name := range field.Names {
				vars[name.Name] = true
			}
		}
	}
	collect(fn.Type.Params)
	ast.Inspect(fn, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			collect(lit.Type.Params)
		}
		return true
	})
	return vars
}

// callScope is what a call is judged against: the testing variables in scope and the testify packages imported.
type callScope struct {
	testing, testify map[string]bool
}

func isSkip(receiver, name string, _ callScope) bool {
	return receiver != "" && strings.HasPrefix(name, "Skip")
}

var failing = []string{"Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow"}

func isFailure(receiver, name string, scope callScope) bool {
	return scope.testify[receiver] || (scope.testing[receiver] && slices.Contains(failing, name))
}

// reaches reports whether fn, or a helper in the package's test files that it calls, makes a call matching match.
func (pkg testPackage) reaches(fn *ast.FuncDecl, match func(receiver, name string, scope callScope) bool, seen map[*ast.FuncDecl]bool) bool {
	if fn.Body == nil || seen[fn] {
		return false
	}
	seen[fn] = true
	scope := callScope{testing: testingVars(fn), testify: pkg.testify[fn]}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		receiver, name := calledName(call)
		if match(receiver, name, scope) {
			found = true
		} else if helper, ok := pkg.funcs[name]; ok && receiver == "" {
			found = pkg.reaches(helper, match, seen)
		}
		return !found
	})
	return found
}
