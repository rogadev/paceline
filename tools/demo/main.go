// Command demo builds the scenes of paceline's demo graphic by calling the
// real renderer, and writes them where the README and the website read them.
//
//	go run ./tools/demo [-root dir]    default root: the current directory
//
// Every status line comes from render.Render with a fixed Now, config, and
// snapshot, so the output is reproducible and changes only when the renderer
// or the scene script does. A test fails when a committed output is stale.
//
// This is a development tool, not part of the paceline binary.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ScenesPath is where scenes.json lives, relative to the repo root.
const ScenesPath = "docs/demo/scenes.json"

const modulePath = "github.com/rogadev/paceline"

// outputs is every file the tool generates, keyed by slash-separated path
// relative to the repo root. Add a generated file here and both the tool and
// the staleness test pick it up.
func outputs(doc *Document) (map[string][]byte, error) {
	scenes, err := encodeJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", ScenesPath, err)
	}
	svg, err := renderSVG(doc)
	if err != nil {
		return nil, fmt.Errorf("drawing %s: %w", SVGPath, err)
	}
	return map[string][]byte{ScenesPath: scenes, SVGPath: svg}, nil
}

// encodeJSON writes v with two-space indents, LF line endings, a trailing
// newline, and glyphs as raw UTF-8, so the output is byte-stable.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkRoot fails unless root is the paceline repo, so a run from the wrong
// directory never scatters a docs/ folder somewhere else.
func checkRoot(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("%s is not the paceline repo root (no go.mod); run from the root or pass -root: %w", root, err)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	if strings.TrimSpace(first) != "module "+modulePath {
		return fmt.Errorf("%s is not the paceline repo root: its go.mod is not module %s", root, modulePath)
	}
	return nil
}

// generate builds the document and writes every output under root. A file
// that already holds the right bytes is left alone, so a second run is a
// no-op.
func generate(root string, log io.Writer) error {
	if err := checkRoot(root); err != nil {
		return err
	}
	doc, err := BuildDocument()
	if err != nil {
		return err
	}
	files, err := outputs(doc)
	if err != nil {
		return err
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		data := files[path]
		target := filepath.Join(root, filepath.FromSlash(path))
		old, err := os.ReadFile(target)
		switch {
		case err == nil && bytes.Equal(old, data):
			fmt.Fprintf(log, "%s is up to date\n", path)
			continue
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("reading %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating the folder for %s: %w", path, err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(log, "wrote %s\n", path)
	}
	return nil
}

func main() {
	root := flag.String("root", ".", "the paceline repo root")
	flag.Parse()
	if err := generate(*root, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}
