# go-rules

`github.com/rynsy/go-rules`: the shared lint rules and verification checks for every Go service built from `go-service-template`. A rule added here reaches every repo through a Renovate PR, and that PR's `make check` shows what the new rule breaks.

The idea: whenever an agent (or a person) gets something wrong, the correction becomes a rule here, not a line in AGENTS.md. A rule is enforced; a sentence in a prompt is not. The Python twin is [py-rules](https://github.com/rynsy/py-rules).

## Use it

```sh
go get -tool github.com/rynsy/go-rules/cmd/rynsy-rules@v0.1.0
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
- **banned APIs** (forbidigo, type-aware, so an import alias does not hide them): `time.Now`, `time.Since` and `time.Until` (inject a clock from `cmd/<name>/main.go`), `time.Sleep` (wait on the event or a context). Tests and `cmd/*/main.go` are exempt;
- no doc comment is required on exported names (a service, not a library); paths resolve from the git root.

## Checks

- `rynsy-rules check`: **RYG001**. Comments are errors except:
  - doc comments on package-level declarations, struct fields and interface methods;
  - `// TODO(#<issue>)` and `// ponytail: <ceiling>`, each with at most two continuation lines;
  - directives (`//go:`, `//line`, `//export`, `// +build`) and `//nolint` (linted by nolintlint);
  - `// Output:` in `Example` functions;
  - generated files.

  Trailing comments and comments inside function bodies count. Files come from `git ls-files`, so ignored files are skipped.
- `rynsy-rules features [-openapi internal/api/openapi.json] [-features features.json]`: every OpenAPI operation has an `operationId` and belongs to a feature; every feature names at least one proving test as `<package dir>:<TestName>` (a subtest may follow `/`). Each such test:
  - exists in a `_test.go` file of that directory with a `*testing.T` parameter;
  - is not behind a `//go:build` constraint;
  - cannot skip itself (no `t.Skip*` call);
  - can fail: a `t.Error*`, `t.Fatal*` or `t.Fail*` call, a testify `assert`/`require` call, or a call to a helper in the package's test files that does one of these.

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
