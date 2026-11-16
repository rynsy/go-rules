// Package rules holds the shared lint config and the checks golangci-lint has no linter for.
package rules

import (
	_ "embed"
	"fmt"
)

// BaseConfig is the golangci-lint config every service lints with.
//
//go:embed golangci.yml
var BaseConfig []byte

// Finding is one rule violation at a position in a file.
type Finding struct {
	Path   string
	Line   int
	Column int
	Rule   string
	Msg    string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: %s %s", f.Path, f.Line, f.Column, f.Rule, f.Msg)
}
