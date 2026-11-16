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

func TestTestify(t *testing.T) { require.True(t, true) }

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

func notATest(t *testing.T) { t.Fatal("x") }

func TestSkipsInSetup(t *testing.T) { setup(t); t.Fatal("x") }

func setup(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no database")
	}
}

func TestBench(b *testing.B) { b.Fatal("x") }
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
	write("features.json", features)
	return root
}

func problems(t *testing.T, root string) []string {
	t.Helper()
	got, err := FeatureProblems(root, filepath.Join(root, "internal/api/openapi.json"), filepath.Join(root, "features.json"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFeatureMapHolds(t *testing.T) {
	root := repo(t, `[
	  {"name": "Health", "operations": ["health"], "provenBy": ["internal/api:TestHealth", "internal/api:TestSubtests/a"]},
	  {"name": "Items", "operations": ["createItem"], "provenBy": ["internal/api:TestViaHelper", "internal/api:TestTestify"]}
	]`)
	got := problems(t, root)
	if len(got) != 1 || !strings.Contains(got[0], "PUT /items has no operationId") {
		t.Errorf("want only the unnamed PUT, got %q", got)
	}
}

func TestFeatureMapGaps(t *testing.T) {
	root := repo(t, `[
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
		"internal/api:TestHealth":          "",
		"internal/api:TestViaHelper":       "",
		"internal/api:TestSubtests/a":      "",
		"internal/api:TestTestify":         "",
		"internal/api:TestNothing":         "cannot fail",
		"internal/api:TestRecursiveHelper": "cannot fail",
		"internal/api:TestSkips":           "can skip itself",
		"internal/api:TestSkipNow":         "can skip itself",
		"internal/api:TestSkipsInSetup":    "through a helper",
		"internal/api:TestTagged":          "build constraint (integration)",
		"internal/api:TestMissing":         "no such test function",
		"internal/api:notATest":            "not a proof of the form",
		"internal/api:TestBench":           "no such test function",
		"internal/none:TestHealth":         "no _test.go files",
		"TestHealth":                       "not a proof of the form",
		"../x:TestHealth":                  "not a proof of the form",
		"/abs:TestHealth":                  "not a proof of the form",
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
	if _, err := FeatureProblems(root, filepath.Join(root, "internal/api/openapi.json"), filepath.Join(root, "features.json")); err == nil {
		t.Error("a broken features.json must be an error")
	}
	if _, _, err := Operations(filepath.Join(root, "features.json")); err == nil {
		t.Error("a broken openapi.json must be an error")
	}
}
