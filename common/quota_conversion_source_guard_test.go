package common

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotaConversionSourceGuardDetectsForbiddenForms(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "service"), 0o755))
	source := []byte(`package service
import (
	"math"
	"github.com/shopspring/decimal"
)
func bad(d decimal.Decimal, value float64) int {
	_ = d.IntPart()
	return int(math.Round(value))
}`)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(root, "service", "bad.go"), source, 0o600),
	)

	findings, err := findUnsafeQuotaConversions(root)
	require.NoError(t, err)
	assert.Len(t, findings, 2)
}

func findUnsafeQuotaConversions(root string) ([]string, error) {
	var findings []string
	for _, packageDir := range []string{"controller", "model", "relay", "service"} {
		dir := filepath.Join(root, packageDir)
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != dir && (entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(source, []byte("Code generated")) &&
				bytes.Contains(source, []byte("DO NOT EDIT")) {
				return nil
			}

			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, source, 0)
			if err != nil {
				return err
			}
			relativePath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}

			mathImportNames := map[string]struct{}{}
			for _, importSpec := range file.Imports {
				importPath, err := strconv.Unquote(importSpec.Path.Value)
				if err != nil || importPath != "math" {
					continue
				}
				importName := "math"
				if importSpec.Name != nil {
					importName = importSpec.Name.Name
				}
				if importName != "." && importName != "_" {
					mathImportNames[importName] = struct{}{}
				}
			}

			allowedDimensionRounds := map[*ast.CallExpr]struct{}{}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name != "getImageToken" || function.Body == nil {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					assignment, ok := node.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for index, right := range assignment.Rhs {
						if index >= len(assignment.Lhs) {
							continue
						}
						left, ok := assignment.Lhs[index].(*ast.Ident)
						if !ok {
							continue
						}
						switch left.Name {
						case "fitW", "fitH", "finalW", "finalH":
						default:
							continue
						}
						call, ok := right.(*ast.CallExpr)
						if ok && isIntMathRoundCall(call, mathImportNames) {
							allowedDimensionRounds[call] = struct{}{}
						}
					}
					return true
				})
			}

			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}

				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "IntPart" {
					position := fileSet.Position(call.Pos())
					findings = append(findings, fmt.Sprintf(
						"%s:%d: decimal.IntPart bypasses centralized quota conversion",
						relativePath,
						position.Line,
					))
				}

				if isIntMathRoundCall(call, mathImportNames) {
					if _, allowed := allowedDimensionRounds[call]; !allowed {
						position := fileSet.Position(call.Pos())
						findings = append(findings, fmt.Sprintf(
							"%s:%d: int(math.Round(...)) bypasses centralized quota conversion",
							relativePath,
							position.Line,
						))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(findings)
	return findings, nil
}

func isIntMathRoundCall(call *ast.CallExpr, mathImportNames map[string]struct{}) bool {
	conversion, ok := call.Fun.(*ast.Ident)
	if !ok || conversion.Name != "int" || len(call.Args) != 1 {
		return false
	}
	roundCall, ok := call.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := roundCall.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Round" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = mathImportNames[packageName.Name]
	return ok
}

func TestQuotaConversionSourceGuard(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(filepath.Dir(currentFile))

	findings, err := findUnsafeQuotaConversions(root)
	require.NoError(t, err)
	assert.Empty(t, findings, strings.Join(findings, "\n"))
}
