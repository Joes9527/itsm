package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// These APIs used to open independent base-record transactions. Keeping even an
// uncalled public method makes a future adapter able to bypass the Intake graph.
func TestIntakeHasNoStandaloneCreationAPIs(t *testing.T) {
	retired := map[string][]string{
		"service/ticket_service.go":                   {"CreateTicket", "triggerWorkflowForTicket", "TriggerWorkflowForExistingTicket"},
		"service/incident_service.go":                 {"CreateIncident", "generateIncidentNumber", "generateIncidentNumberWithDB"},
		"handlers/problem/service.go":                 {"Create"},
		"handlers/problem/repository_impl.go":         {"Create", "createInTx"},
		"handlers/problem/conversion.go":              {"CreateFromIncident"},
		"handlers/change/service.go":                  {"CreateChange", "CreateChangeForWorkflow"},
		"handlers/change/repository_impl.go":          {"Create"},
		"handlers/service_request/service.go":         {"Create", "createWorkItemAndExtension", "createIncidentFromCatalog", "triggerWorkflowAfterServiceRequestCommit"},
		"handlers/service_request/repository_impl.go": {"Create"},
		"repository/ticket/repository_impl.go":        {"Create", "CreateInTransaction", "NewTransactionalCreator"},
	}
	for file, names := range retired {
		t.Run(file, func(t *testing.T) {
			source, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../..", file), nil, 0)
			require.NoError(t, err)
			for _, decl := range source.Decls {
				if function, ok := decl.(*ast.FuncDecl); ok {
					require.NotContains(t, names, function.Name.Name, "standalone creation path must be removed; creation belongs to shared Intake")
				}
			}
		})
	}
}
