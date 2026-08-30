// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package runtimes_test

import (
	"go/ast"
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

// The environment is read at the composition edge, in cmd, and handed down as
// values. A package inside the seam that reads it directly is a second,
// unscoped source of configuration — which is how a credential or an API root
// meant for one runtime reaches another, whatever the variable is called.
func TestRuntimeSeamNeverReadsTheEnvironment(t *testing.T) {
	root := filepath.Clean("../..")
	seam := []string{
		filepath.Join("internal", "provider"),
		filepath.Join("internal", "managed"),
		filepath.Join("internal", "runtimes"),
	}
	for _, dir := range seam {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := call.X.(*ast.Ident)
				if !ok || pkg.Name != "os" {
					return true
				}
				switch call.Sel.Name {
				case "Getenv", "LookupEnv", "Environ", "ExpandEnv":
					t.Errorf("%s calls os.%s; configuration must arrive from cmd", rel, call.Sel.Name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
