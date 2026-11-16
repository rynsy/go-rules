package rules

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var modulePath = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// RunProofs runs the proving tests with `go test -json`, one run per package directory, and returns
// each proof that did not pass, with the reason. A proof passes only on a "pass" event for exactly
// that package and test (or subtest).
func RunProofs(root string, proofs []string) (map[string]string, error) {
	failed := map[string]string{}
	if len(proofs) == 0 {
		return failed, nil
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod")) //nolint:gosec // G304: the repo's own go.mod
	if err != nil {
		return nil, err
	}
	match := modulePath.FindSubmatch(gomod)
	if match == nil {
		return nil, fmt.Errorf("%s/go.mod has no module line", root)
	}
	byDir := map[string][]string{}
	for _, proof := range proofs {
		dir, _, _ := strings.Cut(proof, ":")
		byDir[dir] = append(byDir[dir], proof)
	}
	for dir, dirProofs := range byDir {
		passed, output, err := runDir(root, dir, dirProofs)
		if err != nil {
			return nil, err
		}
		pkg := path.Join(string(match[1]), filepath.ToSlash(dir))
		for _, proof := range dirProofs {
			_, test, _ := strings.Cut(proof, ":")
			if !passed[pkg+" "+strings.ReplaceAll(test, " ", "_")] {
				failed[proof] = "did not pass when run (`go test -run` in " + dir + ")" + output
			}
		}
	}
	return failed, nil
}

func runDir(root, dir string, proofs []string) (map[string]bool, string, error) {
	var tops []string
	for _, proof := range proofs {
		_, test, _ := strings.Cut(proof, ":")
		top, _, _ := strings.Cut(test, "/")
		if !slices.Contains(tops, top) {
			tops = append(tops, regexp.QuoteMeta(top))
		}
	}
	//nolint:gosec // G204: go itself; the arguments are the repo's own package dir and test names
	cmd := exec.Command("go", "test", "-json", "-count=1", "-run", "^("+strings.Join(tops, "|")+")$", "./"+filepath.ToSlash(dir))
	cmd.Dir = root
	out, _ := cmd.Output()
	passed := map[string]bool{}
	var buildOutput strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		var event struct{ Action, Package, Test, Output string }
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		switch event.Action {
		case "pass":
			passed[event.Package+" "+event.Test] = true
		case "build-output":
			buildOutput.WriteString(event.Output)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", err
	}
	if buildOutput.Len() > 0 {
		return passed, ": it did not build:\n" + buildOutput.String(), nil
	}
	return passed, "", nil
}
