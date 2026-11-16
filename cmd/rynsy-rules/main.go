// Command rynsy-rules writes the shared golangci-lint config and runs the checks it has no linter for.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	rules "github.com/rynsy/go-rules"
)

const usage = `usage:
  rynsy-rules config    (writes .golangci.rynsy.yml)
  rynsy-rules check
  rynsy-rules features [-openapi internal/api/openapi.json] [-features features.json]`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	openapi := flags.String("openapi", "internal/api/openapi.json", "the committed OpenAPI document")
	features := flags.String("features", "features.json", "the feature map")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	var problems []string
	var err error
	switch args[0] {
	case "config":
		err = os.WriteFile(".golangci.rynsy.yml", rules.BaseConfig, 0o600)
	case "check":
		problems, err = check()
	case "features":
		problems, err = rules.FeatureProblems(".", *openapi, *features)
	default:
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rynsy-rules:", err)
		return 2
	}
	for _, p := range problems {
		_, _ = fmt.Fprintln(stdout, p)
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

func check() ([]string, error) {
	listed, err := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var problems []string
	for path := range strings.SplitSeq(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		src, err := os.ReadFile(path) //nolint:gosec // G304: paths come from git ls-files in the repo
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		findings, err := rules.CheckSource(path, src)
		if err != nil {
			return nil, err
		}
		for _, f := range findings {
			problems = append(problems, f.String())
		}
	}
	return problems, nil
}
