package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type moveRequest struct {
	dir, target, harness string
	files, tests, keep   []string
	renames              map[string]string
	dryRun               bool
}

// edit replaces del bytes at off with ins.
type edit struct {
	off, del int
	ins      string
}

// plan is what a move does to every unit.
type plan struct {
	moved    []*unit
	newSide  map[*unit]bool // reached by the moved tests, or moving with a file moved whole
	oldSide  map[*unit]bool // reached by the tests that stay
	harness  map[*unit]bool // goes into the shared helper package
	inMember map[types.Object]bool
	owner    map[types.Object]string // a member's type
	exported map[types.Object]bool   // package-level objects going into the harness
	renames  map[string]string
}

func (m *model) move(r moveRequest) error {
	p, err := m.plan(r)
	if err != nil {
		return err
	}
	harnessPath := m.path + "/" + r.harness
	harnessName := filepath.Base(r.harness)
	existing, err := declaredNames(filepath.Join(r.dir, r.harness))
	if err != nil {
		return err
	}
	commentRenames := map[string]string{}
	for obj := range p.exported {
		// A short lower-case name is too likely to be an ordinary word in a
		// comment to be renamed there.
		if strings.ToLower(obj.Name()) == obj.Name() && len(obj.Name()) < 8 {
			continue
		}
		if name := p.nameOf(obj); name != obj.Name() {
			commentRenames[obj.Name()] = name
		}
	}
	var names []string
	for name := range m.files {
		names = append(names, name)
	}
	sort.Strings(names)
	written := 0
	for _, name := range names {
		file, src := m.files[name], m.sources[name]
		base := m.fset.File(file.Pos()).Base()
		var groupUnits, harnessUnits []*unit
		for _, u := range m.units {
			if u.file != name {
				continue
			}
			switch {
			case p.harness[u]:
				if !p.alreadyIn(u, existing) {
					harnessUnits = append(harnessUnits, u)
				}
			case p.newSide[u]:
				groupUnits = append(groupUnits, u)
			}
		}
		if len(groupUnits) > 0 {
			out, err := m.groupFile(p, name, file, src, base, groupUnits, filepath.Base(r.target), harnessPath, harnessName, commentRenames)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			written += len(out)
			if err := write(filepath.Join(r.dir, r.target, name), out, r.dryRun); err != nil {
				return err
			}
		}
		if len(harnessUnits) > 0 {
			path := filepath.Join(r.dir, r.harness, strings.TrimSuffix(name, "_test.go")+".go")
			before, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			out, err := m.harnessFile(p, file, src, base, before, harnessUnits, harnessName, commentRenames)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			written += len(out) - len(before)
			if err := write(path, out, r.dryRun); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(os.Stderr, "moved %d tests, %d bytes written\n", len(p.moved), written)
	return m.trim(p, names, r)
}

func (m *model) plan(r moveRequest) (*plan, error) {
	keep := set(r.keep)
	single := set(r.tests)
	named, whole := map[string]bool{}, map[string]bool{}
	for _, name := range r.files {
		if m.files[name] == nil {
			return nil, fmt.Errorf("no test file %s in %s", name, r.dir)
		}
		named[name], whole[name] = true, true
	}
	p := &plan{renames: r.renames}
	inGroup := map[string]bool{}
	moved := map[*unit]bool{}
	for _, u := range m.units {
		if !u.isTest || !(named[u.file] || single[u.name]) || keep[u.name] {
			if u.isTest && named[u.file] {
				whole[u.file] = false
			}
			continue
		}
		if internals := reached(u); len(internals) > 0 {
			fmt.Fprintf(os.Stderr, "stays: %s in %s reaches %s\n", u.name, u.file, strings.Join(internals, ", "))
			whole[u.file] = false
			continue
		}
		p.moved = append(p.moved, u)
		moved[u] = true
		inGroup[u.file] = true
	}
	var staying []*unit
	for _, u := range m.units {
		if u.isTest && !moved[u] {
			staying = append(staying, u)
		}
	}
	p.newSide = closure(p.moved)
	p.oldSide = closure(staying)
	// Whatever else a file moved whole declares, and nothing left behind uses,
	// goes with it.
	for _, u := range m.units {
		if whole[u.file] && !u.isTest && !p.oldSide[u] {
			for each := range closure([]*unit{u}) {
				p.newSide[each] = true
			}
		}
	}
	for u := range p.newSide {
		if len(u.internals) > 0 {
			return nil, fmt.Errorf("helper %s in %s reaches %v, so the tests that use it cannot move", u.name, u.file, u.internals)
		}
	}
	// A helper declared outside the group, or one the tests left behind use
	// too, is shared: it goes into the harness, with everything it reaches.
	var shared []*unit
	for u := range p.newSide {
		if !u.isTest && (!inGroup[u.file] || p.oldSide[u]) {
			shared = append(shared, u)
		}
	}
	p.harness = closure(shared)
	p.exported = map[types.Object]bool{}
	p.owner = map[types.Object]string{}
	p.inMember = map[types.Object]bool{}
	for u := range p.harness {
		for _, obj := range u.objs {
			p.exported[obj] = true
			typeName, ok := obj.(*types.TypeName)
			if !ok {
				continue
			}
			if st, ok := typeName.Type().Underlying().(*types.Struct); ok {
				for i := 0; i < st.NumFields(); i++ {
					p.owner[st.Field(i)] = typeName.Name()
					p.inMember[st.Field(i)] = true
				}
			}
			if iface, ok := typeName.Type().Underlying().(*types.Interface); ok {
				for i := 0; i < iface.NumExplicitMethods(); i++ {
					p.owner[iface.ExplicitMethod(i)] = typeName.Name()
					p.inMember[iface.ExplicitMethod(i)] = true
				}
			}
			if named, ok := typeName.Type().(*types.Named); ok {
				for i := 0; i < named.NumMethods(); i++ {
					p.owner[named.Method(i)] = typeName.Name()
					p.inMember[named.Method(i)] = true
				}
			}
		}
	}
	return p, p.collisions()
}

func set(list []string) map[string]bool {
	s := map[string]bool{}
	for _, each := range list {
		s[each] = true
	}
	return s
}

// nameOf is the name obj has in the harness.
func (p *plan) nameOf(obj types.Object) string {
	if owner, ok := p.owner[obj]; ok {
		if name, ok := p.renames[owner+"."+obj.Name()]; ok {
			return name
		}
	}
	if obj.Exported() {
		return obj.Name()
	}
	r := []rune(obj.Name())
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// collisions refuses a plan in which exporting two names makes them one.
func (p *plan) collisions() error {
	var problems []string
	top := map[string]string{}
	members := map[string]map[string]string{}
	for obj := range p.exported {
		name := p.nameOf(obj)
		if previous, ok := top[name]; ok && previous != obj.Name() {
			problems = append(problems, fmt.Sprintf("%s and %s would both be %s", previous, obj.Name(), name))
		}
		top[name] = obj.Name()
	}
	for obj, owner := range p.owner {
		if members[owner] == nil {
			members[owner] = map[string]string{}
		}
		name := p.nameOf(obj)
		if previous, ok := members[owner][name]; ok && previous != obj.Name() {
			problems = append(problems, fmt.Sprintf("%s.%s and %s.%s would both be %s; name one with -rename %s.%s=NewName", owner, previous, owner, obj.Name(), name, owner, strings.ToLower(name[:1])+name[1:]))
		}
		members[owner][name] = obj.Name()
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("exporting the shared helpers collides:\n\t%s", strings.Join(problems, "\n\t"))
}

// alreadyIn reports whether the harness already declares u, from a group moved
// before this one.
func (p *plan) alreadyIn(u *unit, existing map[string]bool) bool {
	if u.recv != "" {
		return existing[strings.ToUpper(u.recv[:1])+u.recv[1:]]
	}
	if len(u.objs) == 0 {
		return false
	}
	for _, obj := range u.objs {
		if !existing[p.nameOf(obj)] {
			return false
		}
	}
	return true
}

// edits qualifies, in one unit, every name the code now reaches across a
// package boundary, and renames what the harness exports.
func (m *model) edits(p *plan, u *unit, base int, inHarness bool, harnessName string) []edit {
	var edits []edit
	ast.Inspect(u.decl, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		obj, isUse := m.info.Uses[id], true
		if obj == nil {
			obj, isUse = m.info.Defs[id], false
		}
		if obj == nil || obj.Pkg() != m.pkg {
			return true
		}
		off := int(id.Pos()) - base
		switch {
		case m.packageLevel(obj) && !m.isTestFile(obj.Pos()):
			if isUse {
				edits = append(edits, edit{off, 0, m.pkg.Name() + "."})
			}
		case m.packageLevel(obj) && p.exported[obj]:
			name := p.nameOf(obj)
			if !inHarness {
				name = harnessName + "." + name
			}
			if name != id.Name {
				edits = append(edits, edit{off, len(id.Name), name})
			}
		case p.inMember[obj]:
			if name := p.nameOf(obj); name != id.Name {
				edits = append(edits, edit{off, len(id.Name), name})
			}
		}
		return true
	})
	return edits
}

// commentEdits renames, in every comment of a file, the helpers the harness
// now exports under another name.
func commentEdits(m *model, file *ast.File, base int, renames map[string]string) []edit {
	var edits []edit
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text, off := comment.Text, int(comment.Pos())-base
			for i := 0; i < len(text); {
				if !identStart(text[i]) || (i > 0 && identChar(text[i-1])) {
					i++
					continue
				}
				j := i
				for j < len(text) && identChar(text[j]) {
					j++
				}
				if name, ok := renames[text[i:j]]; ok {
					edits = append(edits, edit{off + i, j - i, name})
				}
				i = j
			}
		}
	}
	return edits
}

func identStart(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func identChar(c byte) bool {
	return identStart(c) || ('0' <= c && c <= '9')
}

// groupFile is a moved test file: the original text, its package renamed,
// less the declarations that do not move, with every name qualified.
func (m *model) groupFile(p *plan, name string, file *ast.File, src []byte, base int, units []*unit, packageName, harnessPath, harnessName string, renames map[string]string) ([]byte, error) {
	moving := map[*unit]bool{}
	var edits []edit
	for _, u := range units {
		moving[u] = true
		edits = append(edits, m.edits(p, u, base, false, harnessName)...)
	}
	edits = append(edits, commentEdits(m, file, base, renames)...)
	for _, u := range m.units {
		if u.file == name && !moving[u] {
			start, end := wholeLines(src, int(u.start)-base, int(u.end)-base)
			edits = append(edits, edit{start, end - start, ""})
		}
	}
	imports := fmt.Sprintf("import %q\nimport %q\n", m.path, harnessPath)
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.IMPORT {
			edits = append(edits, edit{int(d.Pos()) - base, 0, imports})
			break
		}
	}
	var out bytes.Buffer
	out.Write(src[:int(file.Package)-base])
	out.WriteString("package " + packageName + "\n")
	out.Write(apply(src, lineEnd(src, int(file.Name.End())-base), len(src), edits))
	return tidyImports(out.Bytes(), m.importNames)
}

// harnessFile appends the shared helpers one test file declares to the harness
// file named after it.
func (m *model) harnessFile(p *plan, file *ast.File, src []byte, base int, before []byte, units []*unit, harnessName string, renames map[string]string) ([]byte, error) {
	var out bytes.Buffer
	if len(before) > 0 {
		out.Write(before)
	} else {
		out.WriteString("package " + harnessName + "\n\n")
	}
	fmt.Fprintf(&out, "import %q\n", m.path)
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.IMPORT {
			out.Write(src[int(d.Pos())-base : int(d.End())-base])
			out.WriteString("\n")
		}
	}
	comments := commentEdits(m, file, base, renames)
	for _, u := range units {
		edits := append(m.edits(p, u, base, true, harnessName), comments...)
		out.WriteString("\n")
		out.Write(apply(src, int(u.start)-base, int(u.end)-base, edits))
		out.WriteString("\n")
	}
	return tidyImports(out.Bytes(), m.importNames)
}

// trim takes out of the package's own test files what moved and nothing left
// behind uses, and removes a file that empties.
func (m *model) trim(p *plan, names []string, r moveRequest) error {
	for _, name := range names {
		file, src := m.files[name], m.sources[name]
		base := m.fset.File(file.Pos()).Base()
		var edits []edit
		left := 0
		for _, u := range m.units {
			if u.file != name {
				continue
			}
			if p.newSide[u] && !p.oldSide[u] {
				start, end := wholeLines(src, int(u.start)-base, int(u.end)-base)
				edits = append(edits, edit{start, end - start, ""})
			} else {
				left++
			}
		}
		if len(edits) == 0 {
			continue
		}
		path := filepath.Join(r.dir, name)
		if left == 0 {
			fmt.Fprintf(os.Stderr, "remove %s\n", path)
			if !r.dryRun {
				if err := os.Remove(path); err != nil {
					return err
				}
			}
			continue
		}
		out, err := tidyImports(apply(src, 0, len(src), edits), m.importNames)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := write(path, out, r.dryRun); err != nil {
			return err
		}
	}
	return nil
}

// wholeLines widens a declaration to the lines it occupies and one blank line
// after it.
func wholeLines(src []byte, start, end int) (int, int) {
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end = lineEnd(src, end)
	if end < len(src) && src[end] == '\n' {
		end++
	}
	return start, end
}

func lineEnd(src []byte, off int) int {
	for off < len(src) && src[off] != '\n' {
		off++
	}
	if off < len(src) {
		off++
	}
	return off
}

// apply writes src[start:end] with the edits inside it made. Edits are ordered
// by offset, a larger deletion first, so a declaration cut out wins over an
// edit inside it.
func apply(src []byte, start, end int, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].off != edits[j].off {
			return edits[i].off < edits[j].off
		}
		return edits[i].del > edits[j].del
	})
	var out bytes.Buffer
	cur := start
	for _, e := range edits {
		if e.off < cur || e.off >= end {
			continue
		}
		out.Write(src[cur:e.off])
		out.WriteString(e.ins)
		cur = e.off + e.del
	}
	out.Write(src[cur:end])
	return out.Bytes()
}

// newFileBound is the size past which a reviewer is not shown a new file
// whole (gitworktree.DefaultMaxDiffFileBytes).
const newFileBound = 64 << 10

func write(path string, content []byte, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "write %s (%d bytes)\n", path, len(content))
	if len(content) > newFileBound {
		fmt.Fprintf(os.Stderr, "warning: %s is past the %d bytes a reviewer is shown of a new file; split it\n", path, newFileBound)
	}
	if dryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// declaredNames is every top-level name a package directory's non-test files
// declare.
func declaredNames(dir string) (map[string]bool, error) {
	names := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
	return names, nil
}
