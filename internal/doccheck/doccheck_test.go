// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package doccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// readElsewhere lists the variables documented in example.env that no
// non-test code reads through os.Getenv, os.LookupEnv, or a helper over them,
// with the reason each is read another way. Every other variable example.env
// names must be read by the code, or the line is stale.
var readElsewhere = map[string]string{
	"ANTHROPIC_API_KEY":  "read through the runtimes.LookupEnv seam, so a test can hand it a fixed map",
	"ANTHROPIC_BASE_URL": "read through the runtimes.LookupEnv seam",
	"TEST_DATABASE_URL":  "read only by the Postgres store contracts, which are tests",
	"REPLICHAI_E2E":      "read only by the opt-in end-to-end tests",
}

// TestEveryEnvironmentVariableIsInExampleEnv checks example.env against the
// code in both directions: every name the code reads is documented, and every
// name documented is read.
func TestEveryEnvironmentVariableIsInExampleEnv(t *testing.T) {
	root := repoRoot(t)
	read := environmentReads(t, root)
	if len(read) == 0 {
		t.Fatal("no environment read found under cmd/ and internal/; the collector is broken, not the code")
	}
	documented := exampleEnvNames(t, root)

	missing := make([]string, 0, len(read))
	for name := range read {
		if !documented[name] {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	for _, name := range missing {
		t.Errorf("%s is read at %s but example.env does not name it; add a line (commented out is fine) saying what it is for", name, read[name])
	}

	stale := make([]string, 0, len(documented))
	for name := range documented {
		if _, ok := read[name]; ok {
			continue
		}
		if _, ok := readElsewhere[name]; ok {
			continue
		}
		stale = append(stale, name)
	}
	slices.Sort(stale)
	for _, name := range stale {
		t.Errorf("example.env names %s, which no non-test code under cmd/ or internal/ reads through os.Getenv, os.LookupEnv, or a helper over them; remove the line if the variable is gone, or list it in readElsewhere with the reason it is read another way", name)
	}
}

// TestEverySubcommandIsDocumented reads the CLI's subcommands from the
// registry that dispatch and help are generated from, and checks that each is
// named as a command in docs/cli.md, the command reference, and in
// docs/architecture.md.
func TestEverySubcommandIsDocumented(t *testing.T) {
	root := repoRoot(t)
	names := subcommands(t, filepath.Join(root, "cmd", "replichai", "commands.go"))
	for _, doc := range []string{filepath.Join("docs", "cli.md"), filepath.Join("docs", "architecture.md")} {
		text, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			t.Fatal(err)
		}
		mentioned := commandSpans(string(text))
		for _, name := range names {
			if !mentioned[name] {
				t.Errorf("%s does not name the %q subcommand in a code span (`%s` or `replichai %s ...`), and cmd/replichai/commands.go registers it", doc, name, name, name)
			}
		}
	}
}

// repoRoot returns the directory two levels above this package and checks
// that it holds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found at %s: %v", root, err)
	}
	return root
}

// sourceFiles lists every non-test Go file under cmd/ and internal/.
func sourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no Go source found under cmd/ and internal/")
	}
	return files
}

// reader identifies a function that passes one of its parameters to
// os.Getenv, os.LookupEnv, or another reader in its package. A string literal
// at that parameter position names an environment variable, just as an
// argument to the os call does. envOr and envBool in the composition roots
// are the current readers. The set is discovered by analysis rather than
// listed, so a new helper is covered as soon as it is written.
type reader struct{ pkg, name string }

// environmentReads returns every environment variable named by a string
// literal at a read site, with the position of one such site. A read site
// whose name is not a literal fails the test, because this check cannot see
// which variable it reads.
func environmentReads(t *testing.T, root string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, path := range sourceFiles(t, root) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files[path] = f
	}

	// Collect readers until no new one is found. A helper may forward its
	// parameter to another helper rather than to the os call.
	readers := map[reader]int{}
	for changed := true; changed; {
		changed = false
		for path, f := range files {
			pkg := filepath.Dir(path)
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				key := reader{pkg, fn.Name.Name}
				if _, known := readers[key]; known {
					continue
				}
				if idx := forwardedParam(fn, pkg, readers); idx >= 0 {
					readers[key] = idx
					changed = true
				}
			}
		}
	}

	reads := map[string]string{}
	for path, f := range files {
		pkg := filepath.Dir(path)
		for _, decl := range f.Decls {
			// Inside a reader, its own parameters are allowed as dynamic
			// names, because forwarding them is what makes it a reader.
			var forwarding []string
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				if _, isReader := readers[reader{pkg, fn.Name.Name}]; isReader {
					forwarding = paramNames(fn.Type)
				}
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				idx := readIndex(call, pkg, readers)
				if idx < 0 || idx >= len(call.Args) {
					return true
				}
				arg := call.Args[idx]
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
					}
					if _, seen := reads[name]; !seen {
						reads[name] = fset.Position(lit.Pos()).String()
					}
					return true
				}
				if id, ok := arg.(*ast.Ident); ok && slices.Contains(forwarding, id.Name) {
					return true
				}
				t.Errorf("%s: environment read with a name this test cannot see; name the variable with a string literal, or read it through a helper that forwards its parameter", fset.Position(arg.Pos()))
				return true
			})
		}
	}
	return reads
}

// readIndex reports which argument of call names an environment variable, or
// -1 when call is not a read: 0 for os.Getenv and os.LookupEnv, and a reader's
// recorded parameter position for a reader in the same package.
func readIndex(call *ast.CallExpr, pkg string, readers map[reader]int) int {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok && x.Name == "os" && (fn.Sel.Name == "Getenv" || fn.Sel.Name == "LookupEnv") {
			return 0
		}
	case *ast.Ident:
		if idx, ok := readers[reader{pkg, fn.Name}]; ok {
			return idx
		}
	}
	return -1
}

// forwardedParam reports which of fn's parameters reaches a read site inside
// its body, or -1 when none does.
func forwardedParam(fn *ast.FuncDecl, pkg string, readers map[reader]int) int {
	params := paramNames(fn.Type)
	found := -1
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found >= 0 {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		idx := readIndex(call, pkg, readers)
		if idx < 0 || idx >= len(call.Args) {
			return true
		}
		if id, ok := call.Args[idx].(*ast.Ident); ok {
			found = slices.Index(params, id.Name)
		}
		return true
	})
	return found
}

// paramNames flattens a signature's parameters in declaration order, so
// `key, fallback string` yields both names at their positions.
func paramNames(ft *ast.FuncType) []string {
	var names []string
	if ft.Params == nil {
		return names
	}
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 {
			names = append(names, "")
			continue
		}
		for _, id := range field.Names {
			names = append(names, id.Name)
		}
	}
	return names
}

// envLine matches a variable line in example.env, NAME=..., whether set or
// commented out.
var envLine = regexp.MustCompile(`^\s*#?\s*([A-Z][A-Z0-9_]*)=`)

// exampleEnvNames returns every variable example.env names, whether the line
// is set or commented out. A commented line is documentation too.
func exampleEnvNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(root, "example.env"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for line := range strings.SplitSeq(string(text), "\n") {
		if m := envLine.FindStringSubmatch(line); m != nil {
			names[m[1]] = true
		}
	}
	if len(names) == 0 {
		t.Fatal("example.env names no variable; the parser is broken, not the file")
	}
	return names
}

// subcommands returns the names the CLI dispatches on: the name field of each
// command literal in the registry returned by func commands in commandsGo. A
// registry that cannot be found fails the test with a reason rather than
// passing with no names.
func subcommands(t *testing.T, commandsGo string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, commandsGo, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", commandsGo, err)
	}
	// The registry is func commands() []command, which returns one composite
	// literal whose elements are command literals; the first field of each is
	// the name. Dispatch and help are both generated from it, so every
	// subcommand is declared there.
	var registry *ast.CompositeLit
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "commands" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if registry != nil {
				return false
			}
			ret, ok := n.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				return true
			}
			if lit, ok := ret.Results[0].(*ast.CompositeLit); ok {
				registry = lit
			}
			return true
		})
	}
	if registry == nil {
		t.Fatalf("no `func commands()` returning a composite literal in %s: the CLI's registry has moved, so move this search with it rather than letting the docs drift", commandsGo)
	}
	var names []string
	for _, elt := range registry.Elts {
		entry, ok := elt.(*ast.CompositeLit)
		if !ok || len(entry.Elts) == 0 {
			t.Fatalf("%s: a registry element that is not a command literal", fset.Position(elt.Pos()))
		}
		first := entry.Elts[0]
		if kv, ok := first.(*ast.KeyValueExpr); ok {
			first = kv.Value
		}
		lit, ok := first.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatalf("%s: a command whose name is not a string literal; this test reads only literal subcommand names", fset.Position(first.Pos()))
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatalf("the registry in %s names no subcommand", commandsGo)
	}
	return names
}

// codeSpan is one backticked span inside a line of markdown.
var codeSpan = regexp.MustCompile("`([^`]+)`")

// commandSpans returns the words a document names as commands: the first word
// of each code span, or the second where the first is `replichai`, and the
// same for each line of a fenced block. `replichai audit <paper>`, `audit`,
// and `replichai sessions ls` name audit, audit, and sessions. A mention in
// running prose does not count, since a word such as run is also English.
//
// It works a line at a time, because a fence is a whole line and an inline
// span never crosses one. Matching spans across the whole document would pair
// the odd backticks of a fence with the next span and read the prose between
// spans as code from there on.
func commandSpans(text string) map[string]bool {
	found := map[string]bool{}
	name := func(span string) {
		fields := strings.Fields(span)
		switch {
		case len(fields) == 0:
		case fields[0] == "replichai" && len(fields) > 1:
			found[fields[1]] = true
		default:
			found[fields[0]] = true
		}
	}
	fenced := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			name(line)
			continue
		}
		for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
			name(m[1])
		}
	}
	return found
}
