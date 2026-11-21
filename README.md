# go-rules

`github.com/rynsy/go-rules`: the shared lint rules and verification checks for every Go service built from `go-service-template`. A rule added here reaches every repo through a Renovate PR, and that PR's `make check` shows what the new rule breaks.

The idea: whenever an agent (or a person) gets something wrong, the correction becomes a rule here, not a line in AGENTS.md. A rule is enforced; a sentence in a prompt is not. The Python twin is [py-rules](https://github.com/rynsy/py-rules).

## Use it

```sh
go get -tool github.com/rynsy/go-rules/cmd/rynsy-rules@v0.4.0
echo .golangci.rynsy.yml >> .gitignore
```

`Makefile` (CI runs these targets, not `golangci-lint-action`, so the shared config and the checks run there too):

```make
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

lint:
	go tool rynsy-rules config        # writes .golangci.rynsy.yml
	$(GOLANGCI) run -c .golangci.rynsy.yml ./...
	go tool rynsy-rules check

features:
	go tool rynsy-rules features
```

The repo keeps no `.golangci.yml` of its own. A one-off exception is `//nolint:<linter> // <reason>` on the line (nolintlint requires both); an exception every service needs belongs here.

## What it sets

`golangci.yml`, written as `.golangci.rynsy.yml`:

- the standard linters plus forbidigo, gocognit (15), gocyclo (12), gosec, misspell, nolintlint, revive and unparam; gofmt and goimports;
- **banned APIs** (forbidigo, type-aware, so an import alias or a function value does not hide them): `time.Now`, `time.Since` and `time.Until` (inject a clock from `cmd/<name>/main.go`), `time.Sleep` (wait on the event or a context). Files ending `_test.go` and `cmd/*/main.go` are exempt. Timers (`time.After`, `NewTimer`, `NewTicker`) stay allowed: a polling loop or a bounded wait in a `select` with a context needs one;
- generated files get no exemption (there is no generated code yet; revisit when there is);
- no doc comment is required on exported names (a service, not a library); paths resolve from the git root.

## Checks

- `rynsy-rules check`: **RYG001**. Comments are errors except:
  - doc comments on package-level declarations, struct fields and interface methods;
  - `// TODO(#<issue>)` and `// ponytail: <ceiling>`, each with at most two continuation lines;
  - directives, matched exactly (known `//go:` directives, `//line file:n`, `//export name`, `// +build`), and `//nolint:<linters> // <reason>` (linted by nolintlint);
  - `// Output:` in receiver-less `Example` functions in `_test.go` files.

  Block comments count, whatever they start with. A generated-code header exempts nothing.
- `rynsy-rules check`: **RYG002**. A `nolint` naming forbidigo is an error: the clock and sleep bans have no inline escape.

  Trailing comments and comments inside function bodies count. Files come from `git ls-files`, so ignored files are skipped.
- `rynsy-rules features [-openapi internal/api/openapi.json] [-features features.json]`: every OpenAPI operation has an `operationId` and belongs to a feature; every feature names at least one proving test as `<package dir>:<TestName>` (a subtest may follow `/`). Each such test:
  - exists in a `_test.go` file of that directory with a `*testing.T` parameter;
  - is not behind a `//go:build` constraint;
  - cannot skip itself (no `t.Skip*` call, directly or in a helper it calls);
  - can fail: a `t.Error*`, `t.Fatal*` or `t.Fail*` call on a `*testing.T` (or `B`, `F`, `TB`) parameter, a call on an imported testify package, or a call to a helper in the package's test files that does one of these;
  - **passes when run**: `features` runs the proofs with `go test -json -run` (one run per package directory, in the caller's environment) and requires a `pass` event for exactly that package and test or subtest (spaces become `_`). A `TestMain` that exits early, a `_windows_test.go` file, a `testdata/` directory, a skip by any route and a phantom subtest name all fail here.

Every OpenAPI `operationId` must be unique.

Every file is read through an `os.Root` opened on the repo: `-openapi` and `-features` are paths inside the repo, and a path that leaves it (`..`, an absolute path, a symlink out) is an error, in `check` too.

A subtest proof (`TestX/sub`) must name a `t.Run` with that literal name, and its function literal must be able to fail. When any proof in a package fails or `go test` exits non-zero, every proof in that package fails. A skip anywhere in a test, subtests included, disqualifies the whole test.

Known ceilings of the static part (each needs deliberate effort, and the run check does not catch them because the test does pass): an assertion in dead code (`if false { t.Fatal() }`), a helper that shares a name with one in the other test package or build-tagged file, a closure that shadows a helper, and an early `return` before the assertions. Fixing these needs go/types; add it when one shows up in a real PR.

`features.json`:

```json
[{"name": "Health", "operations": ["health"], "provenBy": ["internal/api:TestHealth"]}]
```

The verify report is language-neutral and comes from py-rules: `uvx --from git+https://github.com/rynsy/py-rules@v0.3.1 rynsy-rules report ...`.

## Rules ledger

| ID | Forbids | Fix | Came from |
|---|---|---|---|
| RYG001 | comments other than doc comments, `TODO(#n)`, `ponytail:` | a name, a type or a test | the .NET and Python comment rules (RYC/RYP001) |
| forbidigo | `time.Now/Since/Until/Sleep` outside main and tests | inject a clock; wait on the event | py-rules banned APIs |
| RYG002 | `//nolint:forbidigo` | inject a clock; wait on the event | adversarial review, 2026-11-16 |
