package service

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServicePackageDoesNotDependOnHTTPMiddleware(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, file := range files {
		if filepath.Ext(file) != ".go" {
			continue
		}
		// Entry fixture test files intentionally exercise the real HTTP->Intake
		// boundary (gin context, middleware.TenantContext) to prove production
		// authorization/tenant behavior; they are not part of the service
		// package's own production dependency surface.
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, parseErr, file)
		for _, imported := range parsed.Imports {
			path, unquoteErr := strconv.Unquote(imported.Path.Value)
			require.NoError(t, unquoteErr, file)
			require.NotEqual(t, "itsm-backend/middleware", path, "%s must depend on lower-layer policy/authentication packages", file)
		}
	}
}
