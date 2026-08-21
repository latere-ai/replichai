package runtimes_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeAdapterImportFirewall(t *testing.T) {
	root := filepath.Clean("../..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, spec := range file.Imports {
			name, _ := strconv.Unquote(spec.Path.Value)
			switch {
			case strings.HasPrefix(name, "github.com/anthropics/") && !strings.HasPrefix(rel, filepath.Join("internal", "provider", "anthropic")):
				t.Errorf("%s imports Anthropic SDK outside its adapter", rel)
			case (strings.HasSuffix(name, "/internal/provider/anthropic") || strings.HasSuffix(name, "/internal/provider/memory")) &&
				!strings.HasPrefix(rel, filepath.Join("internal", "runtimes", "builtin")) &&
				!strings.HasPrefix(rel, filepath.Join("internal", "provider")):
				t.Errorf("%s imports a concrete adapter outside the composition boundary", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk source tree: %v", err)
	}
}
