// Package policy holds repository-wide guarantees that are tested like
// behavior. paceline runs on every status-line refresh on the user's machine,
// so what the shipped binary can do is part of its contract.
package policy

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// forbiddenDeps lists packages that must not appear anywhere in a shipped
// binary's dependency graph, including through the standard library. It is
// narrower than forbidden because the graph always reaches packages such as
// syscall and unsafe through os, so only the capabilities paceline promises
// never to use are checked there. Each is also in forbidden, which gives the
// reason.
var forbiddenDeps = []string{"net", "net/http", "os/exec"}

// binaries lists the main packages that paceline ships.
var binaries = []string{"./cmd/paceline", "./cmd/paceline-mcp"}

// publicPackages sit at the module root because other modules import them.
var publicPackages = []string{"pace", "timefmt"}

// shippedDirs holds every directory whose non-test Go files end up in a
// shipped binary.
var shippedDirs = append([]string{"cmd", "internal"}, publicPackages...)

// shippedFiles lists non-test Go files that end up in the paceline binary.
func shippedFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range shippedDirs {
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

// TestShippedFilesReachPublicPackages proves the import walk sees the
// packages at the module root, so a forbidden import there would be caught.
func TestShippedFilesReachPublicPackages(t *testing.T) {
	files := shippedFiles(t)
	for _, dir := range publicPackages {
		prefix := filepath.Join(root, dir) + string(filepath.Separator)
		found := false
		for _, file := range files {
			if strings.HasPrefix(file, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("shippedFiles found no file under %s", dir)
		}
	}
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
	for _, dir := range append([]string{"tools"}, shippedDirs...) {
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

// TestBinariesDependOnNothingDangerous checks each binary's full dependency
// graph, which catches a forbidden package pulled in indirectly by another
// package that the per-file import check cannot see.
func TestBinariesDependOnNothingDangerous(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH, so the dependency graph cannot be listed")
	}
	for _, binary := range binaries {
		imports := binaryImports(t, goBin, binary)
		for _, dep := range forbiddenDeps {
			if _, linked := imports[dep]; linked {
				t.Errorf("%s depends on %s (%s), imported directly by: %s",
					binary, dep, forbidden[dep], strings.Join(importersOf(imports, dep), ", "))
			}
		}
	}
}

// binaryImports maps every package that binary links to the packages it
// imports directly. Test-only dependencies are excluded, because they never
// ship.
func binaryImports(t *testing.T, goBin, binary string) map[string][]string {
	t.Helper()
	// Each output line is a package path followed by its direct imports.
	cmd := exec.Command(goBin, "list", "-deps", "-f", `{{.ImportPath}} {{join .Imports " "}}`, binary)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", binary, err, stderr.String())
	}
	imports := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			imports[fields[0]] = fields[1:]
		}
	}
	if len(imports) == 0 {
		t.Fatalf("go list -deps %s listed no packages", binary)
	}
	return imports
}

// importersOf returns the packages in imports that import dep directly,
// sorted so failure messages are stable.
func importersOf(imports map[string][]string, dep string) []string {
	var importers []string
	for pkg, pkgImports := range imports {
		if slices.Contains(pkgImports, dep) {
			importers = append(importers, pkg)
		}
	}
	slices.Sort(importers)
	return importers
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
