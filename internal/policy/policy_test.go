// Package policy holds repository-wide guarantees that are tested like
// behavior. paceline runs on every status-line refresh on the user's machine,
// so what the shipped binary can do is part of its contract.
package policy

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const root = "../.."

// Packages the shipped binary must never import: no processes, no network,
// no dynamic code, no escape hatches from the type system.
var forbidden = map[string]string{
	"os/exec":    "starts processes",
	"syscall":    "raw system calls",
	"unsafe":     "bypasses memory safety",
	"plugin":     "loads code at runtime",
	"net":        "network access",
	"net/http":   "network access",
	"net/rpc":    "network access",
	"net/smtp":   "network access",
	"net/mail":   "network parsing",
	"net/url":    "network URLs",
	"crypto/tls": "network access",
}

// shippedFiles lists non-test Go files that end up in the paceline binary.
func shippedFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("found no source files; is root correct?")
	}
	return files
}

func TestShippedCodeImportsNothingDangerous(t *testing.T) {
	fset := token.NewFileSet()
	for _, file := range shippedFiles(t) {
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if why, bad := forbidden[path]; bad {
				t.Errorf("%s imports %s (%s)", file, path, why)
			}
			if strings.Contains(path, ".") && !strings.HasPrefix(path, "github.com/rogadev/paceline/") {
				t.Errorf("%s imports third-party package %s", file, path)
			}
		}
	}
}

// TestGoSourceIsASCII keeps invisible and look-alike characters out of the
// code ("Trojan Source"): a bidi override or zero-width space in a source file
// can make code read differently than it compiles. Non-ASCII text belongs in
// string literals as \u escapes, where reviewers can see it.
func TestGoSourceIsASCII(t *testing.T) {
	for _, dir := range []string{"cmd", "internal", "tools"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line := 1
			for _, b := range data {
				if b == '\n' {
					line++
				}
				if b > 127 {
					t.Errorf("%s:%d: non-ASCII byte 0x%02x; write it as a \\u escape", path, line, b)
					break
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoModuleDependencies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) > 0 && (f[0] == "require" || f[0] == "replace") {
			t.Errorf("go.mod declares a dependency: %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); err == nil {
		t.Error("go.sum exists, so something pulled in a dependency")
	}
}
