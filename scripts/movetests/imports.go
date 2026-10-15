package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// tidyImports gathers every import declaration of src into one block where the
// first one stood, drops the imports nothing uses, groups the standard library
// ahead of everything else, and formats the result. names maps an import path
// to its package's name, for a path whose last element is not the name.
func tidyImports(src []byte, names map[string]string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := selector.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	type spec struct{ name, path string }
	var standard, others []spec
	seen := map[spec]bool{}
	var spans [][2]int
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.GenDecl)
		if !ok || d.Tok != token.IMPORT {
			continue
		}
		start := fset.Position(d.Pos()).Offset
		if d.Doc != nil {
			start = fset.Position(d.Doc.Pos()).Offset
		}
		spans = append(spans, [2]int{start, fset.Position(d.End()).Offset})
		for _, s := range d.Specs {
			s := s.(*ast.ImportSpec)
			path := strings.Trim(s.Path.Value, `"`)
			each := spec{path: path}
			local := names[path]
			if local == "" {
				local = path[strings.LastIndex(path, "/")+1:]
			}
			if s.Name != nil {
				each.name, local = s.Name.Name, s.Name.Name
			}
			if seen[each] || (local != "_" && !used[local]) {
				continue
			}
			seen[each] = true
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				others = append(others, each)
			} else {
				standard = append(standard, each)
			}
		}
	}
	if len(spans) == 0 {
		return format.Source(src)
	}
	var block bytes.Buffer
	if len(standard)+len(others) > 0 {
		block.WriteString("import (\n")
		for i, group := range [][]spec{standard, others} {
			sort.Slice(group, func(a, b int) bool { return group[a].path < group[b].path })
			if i == 1 && len(standard) > 0 && len(others) > 0 {
				block.WriteString("\n")
			}
			for _, each := range group {
				block.WriteString("\t")
				if each.name != "" {
					block.WriteString(each.name + " ")
				}
				fmt.Fprintf(&block, "%q\n", each.path)
			}
		}
		block.WriteString(")")
	}
	// Anything standing between two import declarations is kept, after the
	// one block.
	var between bytes.Buffer
	for i := 1; i < len(spans); i++ {
		if piece := bytes.TrimSpace(src[spans[i-1][1]:spans[i][0]]); len(piece) > 0 {
			between.WriteString("\n\n")
			between.Write(piece)
		}
	}
	var out bytes.Buffer
	out.Write(src[:spans[0][0]])
	out.Write(block.Bytes())
	out.Write(between.Bytes())
	out.Write(src[spans[len(spans)-1][1]:])
	return format.Source(out.Bytes())
}
