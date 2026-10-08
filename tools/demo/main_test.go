package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the repo root as seen from this package's directory, where go
// test runs.
const repoRoot = "../.."

func readRepoFile(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
}

func fakeRepo(t *testing.T, goMod string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGenerateWritesThenLeavesUpToDateFilesAlone(t *testing.T) {
	root := fakeRepo(t, "module github.com/rogadev/paceline\n\ngo 1.26.0\n")
	var log bytes.Buffer
	if err := generate(root, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "wrote "+ScenesPath) {
		t.Errorf("first run log: %q", log.String())
	}
	files, err := outputs(buildDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s was not written as built (err %v)", path, err)
		}
	}

	log.Reset()
	if err := generate(root, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "wrote") || !strings.Contains(log.String(), ScenesPath+" is up to date") {
		t.Errorf("second run log: %q", log.String())
	}
}

func TestGenerateRefusesAnotherDirectory(t *testing.T) {
	for name, root := range map[string]string{
		"no go.mod":      t.TempDir(),
		"another module": fakeRepo(t, "module example.com/other\n"),
	} {
		var log bytes.Buffer
		if err := generate(root, &log); err == nil {
			t.Errorf("%s: generate succeeded", name)
		}
		if _, err := os.Stat(filepath.Join(root, "docs")); err == nil {
			t.Errorf("%s: generate wrote into the wrong directory", name)
		}
	}
}

func TestOutputsListsScenesJSON(t *testing.T) {
	files, err := outputs(buildDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(files[ScenesPath]) == 0 {
		t.Errorf("outputs has no %s", ScenesPath)
	}
	for path := range files {
		if strings.Contains(path, `\`) || filepath.IsAbs(path) {
			t.Errorf("output path %q is not a relative slash path", path)
		}
	}
}
