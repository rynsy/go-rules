package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func inRepo(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func runCLI(args ...string) (int, string) {
	var out, errs bytes.Buffer
	code := run(args, &out, &errs)
	return code, out.String() + errs.String()
}

func TestConfigWritesTheBase(t *testing.T) {
	inRepo(t, nil)
	if code, out := runCLI("config"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	written, err := os.ReadFile(".golangci.rynsy.yml")
	if err != nil || !bytes.Contains(written, []byte("forbidigo")) {
		t.Fatalf("config not written: %v", err)
	}
}

func TestCheck(t *testing.T) {
	inRepo(t, map[string]string{
		"a.go":            "package a\n\nfunc F() {\n\t// why\n}\n",
		"ok.go":           "// Package a is a.\npackage a\n",
		"vendor/v.go":     "package v\n\n// ignored\n",
		".gitignore":      "vendor/\n",
		"with space/b.go": "package b\n\nvar V = 1 // why\n",
	})
	code, out := runCLI("check")
	if code != 1 || !strings.Contains(out, "a.go:4:2: RYG001") || !strings.Contains(out, "with space/b.go:3:11: RYG001") {
		t.Errorf("exit %d: %s", code, out)
	}
	if strings.Contains(out, "vendor") {
		t.Errorf("gitignored files must be skipped: %s", out)
	}
	if err := os.Remove("a.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("with space/b.go", []byte("package b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runCLI("check"); code != 0 {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestCheckRejectsBrokenGo(t *testing.T) {
	inRepo(t, map[string]string{"a.go": "package"})
	if code, _ := runCLI("check"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestFeatures(t *testing.T) {
	inRepo(t, map[string]string{
		"internal/api/openapi.json": `{"paths": {"/h": {"get": {"operationId": "health"}}}}`,
		"features.json":             `[]`,
	})
	code, out := runCLI("features")
	if code != 1 || !strings.Contains(out, "operation health (GET /h) belongs to no feature") {
		t.Errorf("exit %d: %s", code, out)
	}
	if code, _ := runCLI("features", "-features", "missing.json"); code != 2 {
		t.Errorf("a missing feature map: exit %d, want 2", code)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"check", "-bogus"}} {
		if code, _ := runCLI(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestCheckDoesNotFollowASymlinkOutOfTheRepo(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "o.go")
	if err := os.WriteFile(outside, []byte("package o\n\nvar V = 1 // why\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inRepo(t, map[string]string{"ok.go": "// Package a is a.\npackage a\n"})
	if err := os.Symlink(outside, "link.go"); err != nil {
		t.Fatal(err)
	}
	if code, out := runCLI("check"); code != 2 || !strings.Contains(out, "link.go") || strings.Contains(out, "RYG001") {
		t.Errorf("exit %d: %s", code, out)
	}
}
