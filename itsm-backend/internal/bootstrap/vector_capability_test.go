package bootstrap

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingVectorCapabilityProbe struct {
	called bool
	err    error
}

func (probe *recordingVectorCapabilityProbe) CheckAvailability(context.Context) error {
	probe.called = true
	return probe.err
}

func TestProbeVectorCapabilityUsesReadOnlyAvailabilityBoundary(t *testing.T) {
	probe := &recordingVectorCapabilityProbe{}
	require.NoError(t, probeVectorCapability(context.Background(), probe))
	require.True(t, probe.called)

	missing := errors.New("missing vector capability")
	probe = &recordingVectorCapabilityProbe{err: missing}
	require.ErrorIs(t, probeVectorCapability(context.Background(), probe), missing)
	require.True(t, probe.called)
}

func TestAPIAndWorkerRuntimeConstructorsContainNoSchemaMutationCalls(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	for fileName, functionName := range map[string]string{
		"app.go":        "NewApplication",
		"kaf_worker.go": "NewKAFWorkerApplication",
	} {
		t.Run(functionName, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(currentFile), fileName), nil, 0)
			require.NoError(t, err)
			var target *ast.FuncDecl
			for _, declaration := range parsed.Decls {
				candidate, isFunction := declaration.(*ast.FuncDecl)
				if isFunction && candidate.Name.Name == functionName {
					target = candidate
					break
				}
			}
			require.NotNil(t, target)
			ast.Inspect(target.Body, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CallExpr:
					if selector, isSelector := value.Fun.(*ast.SelectorExpr); isSelector {
						require.NotContains(t, []string{"EnsureExtension", "Exec", "ExecContext", "RunFreshBootstrap", "RunUpgrade"}, selector.Sel.Name)
						if selector.Sel.Name == "Create" {
							owner, isOwner := selector.X.(*ast.SelectorExpr)
							require.False(t, isOwner && owner.Sel.Name == "Schema", "runtime constructor calls Schema.Create")
						}
					}
				case *ast.BasicLit:
					if value.Kind == token.STRING {
						normalized := strings.ToUpper(value.Value)
						for _, statement := range []string{
							"CREATE TABLE", "CREATE EXTENSION", "CREATE INDEX", "CREATE SCHEMA",
							"ALTER TABLE", "ALTER EXTENSION", "DROP TABLE", "DROP EXTENSION", "DROP INDEX", "DROP SCHEMA",
						} {
							require.NotContains(t, normalized, statement)
						}
					}
				}
				return true
			})
		})
	}
}
