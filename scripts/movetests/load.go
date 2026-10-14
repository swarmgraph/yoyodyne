package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// unit is one top-level declaration of a test file: a function, a method, or
// a type, const, or var declaration with all its specs.
type unit struct {
	file  string
	decl  ast.Decl
	start token.Pos // the start of its doc comment, if it has one
	end   token.Pos
	objs  []types.Object
	deps  map[*unit]bool
	// internals names the unexported identifiers of the package's own code the
	// declaration reaches directly.
	internals map[string]bool
	isTest    bool
	name      string
	recv      string // a method's receiver type
}

// model is the package's test variant, type-checked, with its test files cut
// into units.
type model struct {
	path        string
	fset        *token.FileSet
	pkg         *types.Package
	info        *types.Info
	units       []*unit
	byObj       map[types.Object]*unit
	sources     map[string][]byte
	files       map[string]*ast.File
	importNames map[string]string
}

func load(dir string) (*model, error) {
	path, err := importPath(dir)
	if err != nil {
		return nil, err
	}
	m := &model{
		path:        path,
		fset:        token.NewFileSet(),
		byObj:       map[types.Object]*unit{},
		sources:     map[string][]byte{},
		files:       map[string]*ast.File{},
		importNames: map[string]string{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var syntax []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if ok, _ := build.Default.MatchFile(dir, name); !ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(m.fset, filepath.Join(dir, name), src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(file.Name.Name, "_test") {
			continue
		}
		syntax = append(syntax, file)
		if strings.HasSuffix(name, "_test.go") {
			m.files[name] = file
			m.sources[name] = src
		}
	}
	m.info = &types.Info{
		Uses:      map[*ast.Ident]types.Object{},
		Defs:      map[*ast.Ident]types.Object{},
		Implicits: map[ast.Node]types.Object{},
	}
	conf := types.Config{Importer: importer.ForCompiler(m.fset, "source", nil)}
	m.pkg, err = conf.Check(path, m.fset, syntax, m.info)
	if err != nil {
		return nil, fmt.Errorf("type-check %s: %w", path, err)
	}
	for node, obj := range m.info.Implicits {
		if spec, ok := node.(*ast.ImportSpec); ok {
			m.importNames[strings.Trim(spec.Path.Value, `"`)] = obj.(*types.PkgName).Imported().Name()
		}
	}
	for _, file := range syntax {
		for _, spec := range file.Imports {
			if obj, ok := m.info.Defs[spec.Name].(*types.PkgName); ok && spec.Name != nil {
				m.importNames[strings.Trim(spec.Path.Value, `"`)] = obj.Imported().Name()
			}
		}
	}
	m.importNames[path] = m.pkg.Name()
	m.cut()
	m.link()
	return m, nil
}

// importPath reads the module path from go.mod at the working directory.
func importPath(dir string) (string, error) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("run from the repository root: %w", err)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		if module, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(module) + "/" + filepath.ToSlash(filepath.Clean(dir)), nil
		}
	}
	return "", fmt.Errorf("go.mod names no module")
}

func (m *model) isTestFile(pos token.Pos) bool {
	return strings.HasSuffix(m.fset.Position(pos).Filename, "_test.go")
}

func (m *model) packageLevel(obj types.Object) bool {
	return obj.Parent() == m.pkg.Scope()
}

// cut splits every test file into units.
func (m *model) cut() {
	var names []string
	for name := range m.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, decl := range m.files[name].Decls {
			u := &unit{file: name, decl: decl, start: decl.Pos(), end: decl.End(), deps: map[*unit]bool{}, internals: map[string]bool{}}
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Doc != nil {
					u.start = d.Doc.Pos()
				}
				u.name = d.Name.Name
				obj := m.info.Defs[d.Name]
				if d.Recv != nil {
					u.recv = receiverName(d.Recv.List[0].Type)
				} else {
					u.objs = append(u.objs, obj)
					u.isTest = isTestFunction(d)
				}
				if obj != nil {
					m.byObj[obj] = u
				}
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				if d.Doc != nil {
					u.start = d.Doc.Pos()
				}
				var named []string
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						u.objs = append(u.objs, m.info.Defs[s.Name])
						named = append(named, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.Name != "_" {
								u.objs = append(u.objs, m.info.Defs[n])
								named = append(named, n.Name)
							}
						}
					}
				}
				u.name = strings.Join(named, ",")
				for _, obj := range u.objs {
					m.byObj[obj] = u
				}
			}
			m.units = append(m.units, u)
		}
	}
}

func receiverName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

func isTestFunction(d *ast.FuncDecl) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(d.Name.Name, prefix) {
			return true
		}
	}
	return false
}

// link records what each unit reaches: the test declarations it depends on,
// and the unexported identifiers of the package's own code.
func (m *model) link() {
	methods := map[string][]*unit{}
	for _, u := range m.units {
		if u.recv != "" {
			methods[u.recv] = append(methods[u.recv], u)
		}
	}
	for _, u := range m.units {
		if u.recv != "" {
			if obj := m.pkg.Scope().Lookup(u.recv); obj != nil {
				if m.isTestFile(obj.Pos()) {
					u.deps[m.byObj[obj]] = true
				} else {
					u.internals["a method on "+u.recv] = true
				}
			}
		}
		if d, ok := u.decl.(*ast.GenDecl); ok && d.Tok == token.TYPE {
			for _, spec := range d.Specs {
				for _, method := range methods[spec.(*ast.TypeSpec).Name.Name] {
					u.deps[method] = true
				}
			}
		}
		ast.Inspect(u.decl, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			obj := m.info.Uses[id]
			if obj == nil || obj.Pkg() != m.pkg {
				return true
			}
			if m.packageLevel(obj) {
				if m.isTestFile(obj.Pos()) {
					if dep := m.byObj[obj]; dep != nil && dep != u {
						u.deps[dep] = true
					}
				} else if !obj.Exported() {
					u.internals[obj.Name()] = true
				}
				return true
			}
			if isMember(obj) && !m.isTestFile(obj.Pos()) && !obj.Exported() {
				u.internals[obj.Name()] = true
			}
			return true
		})
	}
	// A test type satisfying one of the package's interfaces that has an
	// unexported method cannot leave either.
	var hidden []*types.Interface
	for _, name := range m.pkg.Scope().Names() {
		obj := m.pkg.Scope().Lookup(name)
		if m.isTestFile(obj.Pos()) {
			continue
		}
		iface, ok := obj.Type().Underlying().(*types.Interface)
		if _, isType := obj.(*types.TypeName); !ok || !isType {
			continue
		}
		for i := 0; i < iface.NumMethods(); i++ {
			if !iface.Method(i).Exported() {
				hidden = append(hidden, iface)
				break
			}
		}
	}
	for _, u := range m.units {
		for _, obj := range u.objs {
			if _, ok := obj.(*types.TypeName); !ok {
				continue
			}
			for _, iface := range hidden {
				if types.Implements(obj.Type(), iface) || types.Implements(types.NewPointer(obj.Type()), iface) {
					u.internals["an interface with an unexported method"] = true
				}
			}
		}
	}
}

func isMember(obj types.Object) bool {
	switch o := obj.(type) {
	case *types.Var:
		return o.IsField()
	case *types.Func:
		return o.Type().(*types.Signature).Recv() != nil
	}
	return false
}

func closure(roots []*unit) map[*unit]bool {
	seen := map[*unit]bool{}
	var walk func(*unit)
	walk = func(u *unit) {
		if u == nil || seen[u] {
			return
		}
		seen[u] = true
		for dep := range u.deps {
			walk(dep)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	return seen
}

// reached is every unexported identifier u reaches, directly or through the
// helpers it calls.
func reached(u *unit) []string {
	set := map[string]bool{}
	for each := range closure([]*unit{u}) {
		for name := range each.internals {
			set[name] = true
		}
	}
	var names []string
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *model) census(out io.Writer, verbose bool) {
	byFile := map[string][]*unit{}
	var names []string
	for _, u := range m.units {
		if !u.isTest {
			continue
		}
		if byFile[u.file] == nil {
			names = append(names, u.file)
		}
		byFile[u.file] = append(byFile[u.file], u)
	}
	sort.Strings(names)
	tests, internal := 0, 0
	fmt.Fprintln(out, "file\ttests\treaching internals\tbytes")
	for _, name := range names {
		var lines []string
		for _, u := range byFile[name] {
			if r := reached(u); len(r) > 0 {
				lines = append(lines, fmt.Sprintf("\t%s: %s", u.name, strings.Join(r, ", ")))
			}
		}
		tests += len(byFile[name])
		internal += len(lines)
		fmt.Fprintf(out, "%s\t%d\t%d\t%d\n", name, len(byFile[name]), len(lines), len(m.sources[name]))
		if verbose {
			for _, line := range lines {
				fmt.Fprintln(out, line)
			}
		}
	}
	fmt.Fprintf(out, "total\t%d\t%d\n", tests, internal)
}
