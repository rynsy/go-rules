package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const openapiDoc = `{"paths": {
  "/health": {"get": {"operationId": "health"}, "parameters": []},
  "/items": {"post": {"operationId": "createItem"}, "put": {}}
}}`

const apiTests = `package api

import "testing"

func TestHealth(t *testing.T) {
	if 1 != 1 {
		t.Fatal("no")
	}
}

func TestViaHelper(t *testing.T) { expectOK(t) }

func expectOK(t *testing.T) { deeper(t) }

func deeper(t *testing.T) { t.Errorf("x") }

func TestSubtests(t *testing.T) {
	t.Run("a", func(t *testing.T) { t.Error("x") })
}

func TestLogsAnError(t *testing.T) { t.Log(errors.New("x").Error()) }

func TestLocalAssert(t *testing.T) {
	assert := struct{ Equal func(...any) }{}
	assert.Equal(t, 1, 2)
}

func TestNothing(t *testing.T) { _ = 1 }

func TestRecursiveHelper(t *testing.T) { loop(t) }

func loop(t *testing.T) { loop(t) }

func TestSkips(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	t.Fatal("x")
}

func TestSkipNow(t *testing.T) { t.SkipNow(); t.Fatal("x") }

func TestEmptySub(t *testing.T) {
	t.Run("empty case", func(t *testing.T) {})
	t.Run("real", func(t *testing.T) {
		t.Run("inner", func(t *testing.T) { t.Error("x") })
	})
	t.Fatal("the parent can fail")
}

func TestSkippingSub(t *testing.T) {
	t.Run("skips", func(t *testing.T) { t.Skip("later"); t.Fatal("x") })
	t.Fatal("x")
}

func notATest(t *testing.T) { t.Fatal("x") }

func TestSkipsInSetup(t *testing.T) { setup(t); t.Fatal("x") }

func setup(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no database")
	}
}

func TestBench(b *testing.B) { b.Fatal("x") }
`

const testifyTests = `package api

import (
	"testing"

	must "github.com/stretchr/testify/require"
)

func TestTestify(t *testing.T) { must.True(t, true) }
`

const taggedTests = `//go:build integration

package api

import "testing"

func TestTagged(t *testing.T) { t.Fatal("x") }
`

func repo(t *testing.T, features string) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, body string) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/api/openapi.json", openapiDoc)
	write("internal/api/api_test.go", apiTests)
	write("internal/api/tagged_test.go", taggedTests)
	write("internal/api/testify_test.go", testifyTests)
	write("features.json", features)
	return root
}

const runnableTests = `package api

import (
	"fmt"
	"os"
	"testing"
)

func TestHealth(t *testing.T) {
	if os.Getenv("FX_BREAK") != "" {
		t.Fatal("broken")
	}
}

func TestSubtests(t *testing.T) {
	t.Run("a b", func(t *testing.T) {
		if os.Getenv("FX_BREAK") != "" {
			t.Error("broken")
		}
	})
}

func TestFails(t *testing.T) {
	if os.Getenv("FX_PASS") == "" {
		t.Fatal("fails")
	}
}

func TestSpoofsAPass(t *testing.T) {
	fmt.Print("\x16--- PASS: TestSpoofsAPass (0.00s)\n")
	if os.Getenv("FX_PASS") == "" {
		t.Fatal("fails")
	}
}
`

const exitingMain = `package other

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(0) }

func TestHidden(t *testing.T) {
	if os.Getenv("FX_BREAK") != "" {
		t.Fatal("x")
	}
}
`

const brokenTests = `package broken

import "testing"

func TestBroken(t *testing.T) {
	if undefined {
		t.Fatal("x")
	}
}
`

func runnable(t *testing.T, openapi, features string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		"go.mod":                      "module example.com/fx\n\ngo 1.27\n",
		"internal/api/openapi.json":   openapi,
		"internal/api/api_test.go":    runnableTests,
		"internal/other/main_test.go": exitingMain,
		"internal/broken/b_test.go":   brokenTests,
		"features.json":               features,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func problems(t *testing.T, root string) []string {
	t.Helper()
	got, err := FeatureProblems(root, "internal/api/openapi.json", "features.json")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFeatureMapHolds(t *testing.T) {
	root := runnable(t, openapiDoc, `[
	  {"name": "Health", "operations": ["health"], "provenBy": ["internal/api:TestHealth", "internal/api:TestSubtests/a b"]},
	  {"name": "Items", "operations": ["createItem"], "provenBy": ["internal/api:TestHealth"]}
	]`)
	got := problems(t, root)
	if len(got) != 1 || !strings.Contains(got[0], "PUT /items has no operationId") {
		t.Errorf("want only the unnamed PUT, got %q", got)
	}
}

func TestProofsMustPassWhenRun(t *testing.T) {
	root := runnable(t, openapiDoc, `[
	  {"name": "Health", "operations": ["health", "createItem"], "provenBy": [
	    "internal/api:TestFails", "internal/api:TestSubtests/no such", "internal/api:TestSpoofsAPass",
	    "internal/other:TestHidden", "internal/broken:TestBroken"
	  ]}
	]`)
	got := strings.Join(problems(t, root), "\n")
	for _, want := range []string{
		"internal/api:TestFails: did not pass when run",
		"internal/api:TestSubtests/no such: no t.Run with that literal subtest name",
		"internal/api:TestSpoofsAPass: did not pass when run",
		"internal/other:TestHidden: did not pass when run",
		"internal/broken:TestBroken: did not pass when run (`go test -run` in internal/broken): it did not build",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestDuplicateOperationIDs(t *testing.T) {
	root := runnable(t, `{"paths": {"/a": {"get": {"operationId": "op"}}, "/b": {"post": {"operationId": "op"}}}}`,
		`[{"name": "A", "operations": ["op"], "provenBy": ["internal/api:TestHealth"]}]`)
	got := problems(t, root)
	if len(got) != 1 || got[0] != "operationId op is used by both GET /a and POST /b" {
		t.Errorf("got %q", got)
	}
}

func TestFeatureMapGaps(t *testing.T) {
	root := runnable(t, openapiDoc, `[
	  {"name": "Health", "operations": ["health", "gone"], "provenBy": []}
	]`)
	got := problems(t, root)
	for _, want := range []string{
		"operation createItem (POST /items) belongs to no feature",
		"feature 'Health': operation gone is not in",
		"feature 'Health': no test proves it",
	} {
		if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, want) }) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestProofProblems(t *testing.T) {
	root := repo(t, `[]`)
	cases := map[string]string{
		"internal/api:TestHealth":              "",
		"internal/api:TestViaHelper":           "",
		"internal/api:TestSubtests/a":          "",
		"internal/api:TestTestify":             "",
		"internal/api:TestNothing":             "cannot fail",
		"internal/api:TestRecursiveHelper":     "cannot fail",
		"internal/api:TestSkips":               "can skip itself",
		"internal/api:TestSkipNow":             "can skip itself",
		"internal/api:TestEmptySub/empty_case": "cannot fail",
		"internal/api:TestSkippingSub/skips":   "can skip itself",
		"internal/api:TestEmptySub/real/inner": "",
		"internal/api:TestEmptySub/real/none":  "no t.Run with that literal subtest name",
		"internal/api:TestSkipsInSetup":        "through a helper",
		"internal/api:TestTagged":              "build constraint (integration)",
		"internal/api:TestMissing":             "no such test function",
		"internal/api:TestLogsAnError":         "cannot fail",
		"internal/api:TestLocalAssert":         "cannot fail",
		"internal/api:notATest":                "not a proof of the form",
		"internal/api:TestBench":               "no such test function",
		"internal/none:TestHealth":             "no _test.go files",
		"TestHealth":                           "not a proof of the form",
		"../x:TestHealth":                      "not a proof of the form",
		"/abs:TestHealth":                      "not a proof of the form",
	}
	for proof, want := range cases {
		t.Run(proof, func(t *testing.T) {
			got := ProofProblem(root, proof)
			if want == "" && got != "" || !strings.Contains(got, want) {
				t.Errorf("got %q, want it to contain %q", got, want)
			}
		})
	}
}

func TestFeatureFilesMustParse(t *testing.T) {
	root := repo(t, `{not json`)
	if _, err := FeatureProblems(root, "internal/api/openapi.json", "features.json"); err == nil {
		t.Error("a broken features.json must be an error")
	}
	if _, _, err := Operations(root, "features.json"); err == nil {
		t.Error("a broken openapi.json must be an error")
	}
}

func TestFilesOutsideTheRepoAreNotRead(t *testing.T) {
	root := runnable(t, `{"paths":{}}`, `[]`)
	outside := t.TempDir()
	for name, body := range map[string]string{"openapi.json": `{"paths":{}}`, "features.json": `[]`} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, name), filepath.Join(root, "link-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	up, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{up, outside} {
		if _, _, err := Operations(root, filepath.Join(dir, "openapi.json")); err == nil {
			t.Errorf("openapi in %s is outside the repo, so reading it must be an error", dir)
		}
		if _, err := FeatureProblems(root, "internal/api/openapi.json", filepath.Join(dir, "features.json")); err == nil {
			t.Errorf("features in %s is outside the repo, so reading it must be an error", dir)
		}
	}
	if _, _, err := Operations(root, "link-openapi.json"); err == nil {
		t.Error("a symlink that leaves the repo must not be followed")
	}
	if _, err := FeatureProblems(root, "internal/api/openapi.json", "link-features.json"); err == nil {
		t.Error("a symlink that leaves the repo must not be followed")
	}
	if got, err := FeatureProblems(root, "internal/api/openapi.json", "features.json"); err != nil || len(got) != 0 {
		t.Errorf("files inside the repo must be read: %v %v", got, err)
	}
}
