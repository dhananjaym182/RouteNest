package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLegacyFailOpenAdminAuthorityCannotReturn(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "adminapi", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"Auth":           true,
		"AuthHandlers":   true,
		"SessionStore":   true,
		"HashPassword":   true,
		"VerifyPassword": true,
		"RandomHex":      true,
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if forbidden[declaration.Name.Name] {
					t.Errorf("obsolete administrator auth function %s returned in %s", declaration.Name.Name, name)
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if forbidden[spec.Name.Name] {
							t.Errorf("obsolete administrator auth type %s returned in %s", spec.Name.Name, name)
						}
					case *ast.ImportSpec:
						path, err := strconv.Unquote(spec.Path.Value)
						if err == nil && path == "crypto/sha256" {
							t.Errorf("fast password hashing import returned in %s", name)
						}
					}
				}
			}
		}
	}
}
